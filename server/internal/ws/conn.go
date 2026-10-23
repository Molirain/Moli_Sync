package ws

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"

	"github.com/molirain/molisync/server/internal/auth"
	"github.com/molirain/molisync/server/internal/registry"
	"github.com/molirain/molisync/server/internal/store"
)

// maxCloseReasonBytes is the protocol limit for a close frame reason.
const maxCloseReasonBytes = 123

// connection is one authenticated control-plane socket. The read pump owns all
// inbound state changes; the write pump is the only goroutine that writes to
// the websocket.
type connection struct {
	hub      *Hub
	ws       *websocket.Conn
	identity auth.Identity
	remote   string
	send     chan outbound
	done     chan struct{}
	limiter  *tokenBucket

	closeOnce   sync.Once
	closeCode   int
	closeReason string
	closed      atomic.Bool
	registered  atomic.Bool
	fatal       atomic.Bool
}

func newConnection(hub *Hub, ws *websocket.Conn, identity auth.Identity, remote string) *connection {
	return &connection{
		hub:      hub,
		ws:       ws,
		identity: identity,
		remote:   remote,
		send:     make(chan outbound, hub.options.SendBuffer),
		done:     make(chan struct{}),
		limiter:  newTokenBucket(hub.options.MaxMessagesPerSecond, hub.now),
	}
}

func (c *connection) readPump() {
	defer func() {
		// A fatal protocol error already queued its own error and close frames.
		// Closing here must not preempt that ordered shutdown.
		if !c.fatal.Load() {
			c.close(websocket.CloseNormalClosure, "")
		}
	}()

	c.ws.SetReadLimit(c.hub.options.ReadLimitBytes)
	_ = c.ws.SetReadDeadline(c.hub.now().Add(c.hub.options.HelloTimeout))
	c.ws.SetPongHandler(func(string) error {
		_ = c.ws.SetReadDeadline(c.hub.now().Add(c.hub.options.PongTimeout))
		if c.registered.Load() {
			c.hub.devices.Touch(c.identity.DeviceID)
		}
		return nil
	})

	for {
		messageType, data, err := c.ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived,
			) {
				c.hub.logger.Debug("control connection read failed",
					"device_id", c.identity.DeviceID,
					"remote", c.remote,
					"error", err,
				)
			}
			return
		}
		_ = c.ws.SetReadDeadline(c.hub.now().Add(c.hub.options.PongTimeout))

		if messageType != websocket.TextMessage {
			c.sendError(CodeUnsupportedMessage, "only JSON text frames are supported", false)
			continue
		}
		if !c.limiter.Allow() {
			c.fail(CodeRateLimited, "message rate limit exceeded", websocket.ClosePolicyViolation, "rate limit exceeded")
			return
		}

		message, err := decodeInbound(data)
		if err != nil {
			c.sendError(CodeInvalidMessage, err.Error(), false)
			continue
		}
		if c.registered.Load() {
			c.hub.devices.Touch(c.identity.DeviceID)
		}
		if !c.handle(message) {
			return
		}
	}
}

func (c *connection) writePump() {
	ticker := time.NewTicker(c.hub.options.PingInterval)
	defer ticker.Stop()
	defer c.ws.Close()

	for {
		select {
		case message := <-c.send:
			if err := c.writeFrame(message); err != nil {
				c.close(websocket.CloseAbnormalClosure, "write failed")
				return
			}
			if message.closeCode != 0 {
				code, reason := message.closeCode, message.closeReason
				c.close(code, reason)
				_ = c.writeClose(code, reason)
				return
			}
		case <-ticker.C:
			if err := c.writeControl(websocket.PingMessage, nil); err != nil {
				c.close(websocket.CloseAbnormalClosure, "ping failed")
				return
			}
		case <-c.done:
			code, reason := c.closeStatus()
			_ = c.writeClose(code, reason)
			return
		}
	}
}

func (c *connection) handle(message inbound) bool {
	switch message.Type {
	case TypeHello:
		return c.handleHello(message)
	case TypeHeartbeat:
		return c.handleHeartbeat(message)
	case TypePing:
		payload, err := newOutbound(pongMessage{Type: TypePong, ServerTime: c.hub.now().UnixMilli()})
		if err != nil {
			c.hub.logger.Error("encode pong", "device_id", c.identity.DeviceID, "error", err)
			return false
		}
		c.enqueue(payload)
		return true
	case TypePublishRecord:
		return c.handlePublishRecord(message)
	case TypeOpenSession:
		return c.handleOpenSession(message)
	default:
		c.sendError(CodeUnsupportedMessage,
			fmt.Sprintf("unsupported message type %s", strconv.Quote(truncate(message.Type, 32))), false)
		return true
	}
}

func (c *connection) handleHello(message inbound) bool {
	if c.registered.Load() {
		c.sendError(CodeAlreadyRegistered, "this connection is already registered", false)
		return true
	}
	if message.Device == nil {
		c.sendError(CodeInvalidDevice, "hello requires a device object", false)
		return true
	}

	name, platform, err := registry.NormalizeDeviceMetadata(message.Device.Name, message.Device.Platform)
	if err != nil {
		c.sendError(CodeInvalidDevice, err.Error(), false)
		return true
	}
	endpoints := []registry.Endpoint{}
	if message.Endpoints != nil {
		endpoints = *message.Endpoints
	}
	if _, err := registry.NormalizeEndpoints(endpoints, c.hub.options.MaxEndpointsPerDevice); err != nil {
		c.sendError(CodeInvalidEndpoints, err.Error(), false)
		return true
	}

	device, created, err := c.hub.devices.Connect(
		c.identity.DeviceID,
		c.identity.CommonName,
		name,
		platform,
		endpoints,
	)
	if err != nil {
		if errors.Is(err, registry.ErrTooManyDevices) {
			c.fail(CodeTooManyDevices, "server device limit reached", websocket.CloseTryAgainLater, "device limit reached")
			return false
		}
		c.sendError(CodeInvalidDevice, err.Error(), false)
		return true
	}

	c.registered.Store(true)
	if !c.hub.attachDevice(c) {
		c.fail(CodeServerShuttingDown, "server is shutting down", websocket.CloseGoingAway, "server shutting down")
		return false
	}
	c.hub.recordDeviceSeen(c.identity.DeviceID, platform)
	_ = c.ws.SetReadDeadline(c.hub.now().Add(c.hub.options.PongTimeout))

	welcome, err := newOutbound(welcomeMessage{
		Type:                TypeWelcome,
		ProtocolVersion:     ProtocolVersion,
		DeviceID:            c.identity.DeviceID,
		ServerTime:          c.hub.now().UnixMilli(),
		HeartbeatIntervalMS: c.hub.options.HeartbeatInterval.Milliseconds(),
		PongTimeoutMS:       c.hub.options.PongTimeout.Milliseconds(),
		ServerVersion:       c.hub.serverVersion,
		Devices:             c.hub.deviceViewsExcept(c.identity.DeviceID),
	})
	if err != nil {
		c.hub.logger.Error("encode welcome", "device_id", c.identity.DeviceID, "error", err)
		c.fail(CodeInternalError, "cannot encode welcome message", websocket.CloseInternalServerErr, "internal error")
		return false
	}
	c.enqueue(welcome)
	state := PresenceUpdated
	if created {
		state = PresenceOnline
	}
	c.hub.broadcastPresence(state, device, c.identity.DeviceID)

	c.hub.logger.Info("device registered",
		"device_id", device.ID,
		"name", device.Name,
		"platform", device.Platform,
		"endpoints", len(device.Endpoints),
	)
	return true
}

func (c *connection) handleOpenSession(message inbound) bool {
	if !c.registered.Load() {
		c.sendError(CodeNotRegistered, "send hello before open_session", false)
		return true
	}

	to := strings.TrimSpace(message.ToDeviceID)
	if to == "" || len(to) > 128 {
		c.sendError(CodeInvalidMessage, "open_session requires to_device_id", false)
		return true
	}

	name := strings.TrimSpace(message.TransferName)
	if len(name) > 200 {
		name = name[:200]
	}

	if err := c.hub.OpenSession(c.identity.DeviceID, to, name); err != nil {
		code := CodeSessionFailed
		if errors.Is(err, ErrPeerUnavailable) {
			code = CodePeerUnavailable
		}
		c.sendError(code, err.Error(), false)
		return true
	}
	return true
}

func (c *connection) handleHeartbeat(message inbound) bool {
	if !c.registered.Load() {
		c.sendError(CodeNotRegistered, "send hello before heartbeat", false)
		return true
	}

	device, known, changed, err := c.hub.devices.Heartbeat(c.identity.DeviceID, message.Endpoints)
	if err != nil {
		c.sendError(CodeInvalidEndpoints, err.Error(), false)
		return true
	}
	if !known {
		c.registered.Store(false)
		c.hub.detachDevice(c)
		_ = c.ws.SetReadDeadline(c.hub.now().Add(c.hub.options.HelloTimeout))
		c.sendError(CodeNotRegistered, "device is no longer registered, send hello again", false)
		return true
	}

	ack, err := newOutbound(heartbeatAckMessage{
		Type:             TypeHeartbeatAck,
		ServerTime:       c.hub.now().UnixMilli(),
		EndpointsChanged: changed,
	})
	if err != nil {
		c.hub.logger.Error("encode heartbeat ack", "device_id", c.identity.DeviceID, "error", err)
		return false
	}
	c.enqueue(ack)

	if changed {
		c.hub.broadcastPresence(PresenceUpdated, device, c.identity.DeviceID)
	}
	return true
}

func (c *connection) handlePublishRecord(message inbound) bool {
	if !c.registered.Load() {
		c.sendError(CodeNotRegistered, "send hello before publish_record", false)
		return true
	}
	if message.Record == nil {
		c.sendError(CodeInvalidRecord, "publish_record requires a record object", false)
		return true
	}
	if c.hub.records == nil {
		c.sendError(CodeStorageUnavailable, "record storage is not configured", false)
		return true
	}

	kind := strings.ToLower(strings.TrimSpace(message.Record.Type))
	if kind != store.KindText {
		c.sendError(CodeInvalidRecord,
			"publish_record accepts text records; upload images and files over HTTP", false)
		return true
	}
	if strings.TrimSpace(message.Record.TextContent) == "" {
		c.sendError(CodeInvalidRecord, "text_content must not be empty", false)
		return true
	}

	senderName := c.hub.DeviceName(c.identity.DeviceID)
	if senderName == "" {
		senderName = c.identity.CommonName
	}
	record, err := c.hub.records.SaveText(c.hub.ctx, c.identity.DeviceID, senderName, message.Record.TextContent)
	if err != nil {
		if errors.Is(err, store.ErrTooLarge) {
			c.sendError(CodeInvalidRecord, "text record exceeds the size limit", false)
		} else {
			c.hub.logger.Error("persist text record", "device_id", c.identity.DeviceID, "error", err)
			c.sendError(CodeInternalError, "cannot persist the record", false)
		}
		return true
	}

	c.hub.BroadcastRecord(record)
	ack, err := newOutbound(recordAckMessage{
		Type:      TypeRecordAck,
		RecordID:  record.RecordID,
		CreatedAt: record.CreatedAt,
	})
	if err != nil {
		c.hub.logger.Error("encode record ack", "record_id", record.RecordID, "error", err)
		return false
	}
	return c.enqueue(ack)
}

func (c *connection) enqueue(message outbound) bool {
	if c.closed.Load() {
		return false
	}
	select {
	case c.send <- message:
		return true
	default:
		c.close(websocket.CloseTryAgainLater, "client is not reading fast enough")
		return false
	}
}

func (c *connection) sendError(code, message string, fatal bool) bool {
	payload, err := newOutbound(errorMessage{
		Type:    TypeError,
		Code:    code,
		Message: message,
		Fatal:   fatal,
	})
	if err != nil {
		c.hub.logger.Error("encode error frame", "device_id", c.identity.DeviceID, "error", err)
		return false
	}
	return c.enqueue(payload)
}

func (c *connection) fail(code, message string, closeCode int, reason string) {
	c.fatal.Store(true)
	payload, err := newOutbound(errorMessage{
		Type:    TypeError,
		Code:    code,
		Message: message,
		Fatal:   true,
	})
	if err != nil {
		c.close(closeCode, reason)
		return
	}
	payload.closeCode = closeCode
	payload.closeReason = reason
	if !c.enqueue(payload) {
		c.close(closeCode, reason)
	}
}

func (c *connection) close(code int, reason string) {
	if code == 0 {
		code = websocket.CloseNormalClosure
	}
	reason = truncateCloseReason(reason)
	c.closeOnce.Do(func() {
		c.closeCode = code
		c.closeReason = reason
		c.closed.Store(true)
		close(c.done)
	})
}

func (c *connection) closeStatus() (int, string) {
	return c.closeCode, c.closeReason
}

func (c *connection) writeFrame(message outbound) error {
	_ = c.ws.SetWriteDeadline(c.hub.now().Add(c.hub.options.WriteTimeout))
	return c.ws.WriteMessage(message.messageType, message.data)
}

func (c *connection) writeControl(messageType int, data []byte) error {
	return c.ws.WriteControl(messageType, data, c.hub.now().Add(c.hub.options.WriteTimeout))
}

func (c *connection) writeClose(code int, reason string) error {
	if code == 0 {
		code = websocket.CloseNormalClosure
	}
	return c.writeControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, truncateCloseReason(reason)))
}

func truncateCloseReason(reason string) string {
	if len(reason) <= maxCloseReasonBytes {
		return reason
	}
	cut := maxCloseReasonBytes
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return reason[:cut]
}
