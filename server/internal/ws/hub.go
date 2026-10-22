// Package ws implements the WebSocket control plane: device registration,
// candidate endpoint reporting and liveness tracking.
//
// The hub owns every connection and is the only component allowed to touch a
// websocket. Each connection has exactly one reader (the HTTP handler
// goroutine) and one writer (the write pump), so frames are never written
// concurrently.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/molirain/molisync/server/internal/auth"
	"github.com/molirain/molisync/server/internal/config"
	"github.com/molirain/molisync/server/internal/registry"
)

// Options configures connection limits, liveness and origin checking.
type Options struct {
	HandshakeTimeout      time.Duration
	HelloTimeout          time.Duration
	PingInterval          time.Duration
	PongTimeout           time.Duration
	WriteTimeout          time.Duration
	ReadLimitBytes        int64
	SendBuffer            int
	HeartbeatInterval     time.Duration
	MaxMessagesPerSecond  int
	AllowedOrigins        []string
	MaxEndpointsPerDevice int
	OfflineAfter          time.Duration
	SweepInterval         time.Duration
}

// Hub accepts control-plane connections and fans presence events out to every
// other registered device.
type Hub struct {
	options        Options
	devices        *registry.Registry
	logger         *slog.Logger
	now            func() time.Time
	upgrader       websocket.Upgrader
	allowedOrigins map[string]struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.RWMutex
	conns    map[*connection]struct{}
	byDevice map[string]map[*connection]struct{}
	closed   bool
}

// NewHub validates the options and starts the stale-device sweeper.
func NewHub(options Options, devices *registry.Registry, logger *slog.Logger) (*Hub, error) {
	if devices == nil {
		return nil, errors.New("ws: device registry is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if err := validateOptions(options); err != nil {
		return nil, err
	}

	allowedOrigins := make(map[string]struct{}, len(options.AllowedOrigins))
	for _, raw := range options.AllowedOrigins {
		normalized, err := config.NormalizeOrigin(raw)
		if err != nil {
			return nil, fmt.Errorf("ws: %w", err)
		}
		allowedOrigins[normalized] = struct{}{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	hub := &Hub{
		options:        options,
		devices:        devices,
		logger:         logger,
		now:            time.Now,
		allowedOrigins: allowedOrigins,
		ctx:            ctx,
		cancel:         cancel,
		conns:          make(map[*connection]struct{}),
		byDevice:       make(map[string]map[*connection]struct{}),
	}
	hub.upgrader = websocket.Upgrader{
		HandshakeTimeout: options.HandshakeTimeout,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		CheckOrigin:      hub.checkOrigin,
	}

	hub.wg.Add(1)
	go hub.sweepLoop()
	return hub, nil
}

// ServeHTTP upgrades an authenticated request to a control-plane connection.
// It must be mounted behind auth.Middleware.RequireDevice.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.FromContext(r.Context())
	if !ok {
		writeHTTPError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
		return
	}

	wsConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has already written an HTTP error response.
		h.logger.Warn("websocket upgrade failed", "device_id", identity.DeviceID, "remote", r.RemoteAddr, "error", err)
		return
	}

	c := newConnection(h, wsConn, identity, r.RemoteAddr)
	if !h.addConnection(c) {
		_ = wsConn.Close()
		return
	}
	h.logger.Debug("control connection opened", "device_id", identity.DeviceID, "remote", r.RemoteAddr)

	go func() {
		defer h.wg.Done()
		c.writePump()
	}()
	c.readPump()
	h.removeConnection(c)
}

// Devices returns a snapshot of all registered devices for HTTP consumers.
func (h *Hub) Devices() []DeviceView {
	devices := h.devices.Snapshot()
	views := make([]DeviceView, 0, len(devices))
	for _, device := range devices {
		views = append(views, newDeviceView(device))
	}
	return views
}

// Shutdown closes every control connection and waits for the write pumps to
// finish, bounded by ctx.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	connections := make([]*connection, 0, len(h.conns))
	for c := range h.conns {
		connections = append(connections, c)
	}
	h.mu.Unlock()

	h.cancel()
	for _, c := range connections {
		c.close(websocket.CloseGoingAway, "server shutting down")
	}

	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hub) addConnection(c *connection) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.conns[c] = struct{}{}
	h.wg.Add(1)
	return true
}

func (h *Hub) attachDevice(c *connection) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	connections, ok := h.byDevice[c.identity.DeviceID]
	if !ok {
		connections = make(map[*connection]struct{})
		h.byDevice[c.identity.DeviceID] = connections
	}
	connections[c] = struct{}{}
	return true
}

func (h *Hub) detachDevice(c *connection) {
	deviceID := c.identity.DeviceID
	h.mu.Lock()
	defer h.mu.Unlock()
	connections, ok := h.byDevice[deviceID]
	if !ok {
		return
	}
	delete(connections, c)
	if len(connections) == 0 {
		delete(h.byDevice, deviceID)
	}
}

func (h *Hub) removeConnection(c *connection) {
	deviceID := c.identity.DeviceID
	h.mu.Lock()
	delete(h.conns, c)
	if connections, ok := h.byDevice[deviceID]; ok {
		delete(connections, c)
		if len(connections) == 0 {
			delete(h.byDevice, deviceID)
		}
	}
	wasRegistered := c.registered.Load()
	h.mu.Unlock()

	if !c.fatal.Load() {
		c.close(websocket.CloseNormalClosure, "")
	}
	h.logger.Debug("control connection closed", "device_id", deviceID, "remote", c.remote)

	if !wasRegistered {
		return
	}
	if device, offline := h.devices.Disconnect(deviceID); offline {
		h.broadcastPresence(PresenceOffline, device, "")
	}
}

func (h *Hub) broadcastPresence(state string, device registry.Device, excludeDeviceID string) {
	message, err := newOutbound(presenceMessage{
		Type:   TypePresence,
		State:  state,
		Device: newDeviceView(device),
	})
	if err != nil {
		h.logger.Error("encode presence message", "device_id", device.ID, "error", err)
		return
	}
	h.broadcast(message, excludeDeviceID)
}

func (h *Hub) broadcast(message outbound, excludeDeviceID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for deviceID, connections := range h.byDevice {
		if deviceID == excludeDeviceID {
			continue
		}
		for c := range connections {
			c.enqueue(message)
		}
	}
}

func (h *Hub) deviceViewsExcept(deviceID string) []DeviceView {
	devices := h.devices.Snapshot()
	views := make([]DeviceView, 0, len(devices))
	for _, device := range devices {
		if device.ID == deviceID {
			continue
		}
		views = append(views, newDeviceView(device))
	}
	return views
}

func (h *Hub) sweepLoop() {
	defer h.wg.Done()
	ticker := time.NewTicker(h.options.SweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			h.sweep()
		}
	}
}

func (h *Hub) sweep() {
	cutoff := h.now().Add(-h.options.OfflineAfter)
	for _, device := range h.devices.Sweep(cutoff) {
		h.logger.Info("device timed out", "device_id", device.ID)
		h.closeDevice(device.ID, websocket.ClosePolicyViolation, "heartbeat timeout")
		h.broadcastPresence(PresenceOffline, device, device.ID)
	}
}

func (h *Hub) closeDevice(deviceID string, code int, reason string) {
	h.mu.RLock()
	connections := make([]*connection, 0, len(h.byDevice[deviceID]))
	for c := range h.byDevice[deviceID] {
		connections = append(connections, c)
	}
	h.mu.RUnlock()

	for _, c := range connections {
		c.close(code, reason)
	}
}

func (h *Hub) checkOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Non-browser clients do not send an Origin header.
		return true
	}
	normalized, err := config.NormalizeOrigin(origin)
	if err != nil {
		return false
	}
	_, allowed := h.allowedOrigins[normalized]
	return allowed
}

func validateOptions(options Options) error {
	if options.HandshakeTimeout <= 0 || options.HelloTimeout <= 0 ||
		options.PingInterval <= 0 || options.PongTimeout <= 0 ||
		options.WriteTimeout <= 0 || options.HeartbeatInterval <= 0 ||
		options.OfflineAfter <= 0 || options.SweepInterval <= 0 {
		return errors.New("ws: all timeouts must be positive")
	}
	if options.PongTimeout <= options.PingInterval {
		return errors.New("ws: pong timeout must be greater than the ping interval")
	}
	if options.SweepInterval > options.OfflineAfter {
		return errors.New("ws: sweep interval must not exceed the offline timeout")
	}
	if options.ReadLimitBytes <= 0 || options.SendBuffer <= 0 ||
		options.MaxMessagesPerSecond <= 0 || options.MaxEndpointsPerDevice <= 0 {
		return errors.New("ws: limits must be positive")
	}
	return nil
}

func writeHTTPError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	body := struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{}
	body.Error.Code = code
	body.Error.Message = message
	_ = json.NewEncoder(w).Encode(body)
}
