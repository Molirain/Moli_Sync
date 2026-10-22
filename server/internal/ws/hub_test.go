package ws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/molirain/molisync/server/internal/auth"
	"github.com/molirain/molisync/server/internal/registry"
)

const phoneDeviceID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const pcDeviceID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func testHubOptions() Options {
	return Options{
		HandshakeTimeout:      2 * time.Second,
		HelloTimeout:          5 * time.Second,
		PingInterval:          time.Second,
		PongTimeout:           5 * time.Second,
		WriteTimeout:          time.Second,
		ReadLimitBytes:        64 << 10,
		SendBuffer:            64,
		HeartbeatInterval:     time.Second,
		MaxMessagesPerSecond:  50,
		MaxEndpointsPerDevice: 8,
		OfflineAfter:          30 * time.Second,
		SweepInterval:         10 * time.Second,
	}
}

func newTestHub(t *testing.T, mutate func(*Options)) (*httptest.Server, *Hub) {
	t.Helper()
	options := testHubOptions()
	if mutate != nil {
		mutate(&options)
	}
	devices := registry.New(64, options.MaxEndpointsPerDevice, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub, err := NewHub(options, devices, logger)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device")
		identity := auth.Identity{
			DeviceID:        deviceID,
			Fingerprint:     deviceID,
			ProxyIP:         netip.MustParseAddr("127.0.0.1"),
			AuthenticatedAt: time.Now().UTC(),
		}
		hub.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), identity)))
	})
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hub.Shutdown(shutdownContext)
		server.Close()
	})
	return server, hub
}

func dialTestClient(t *testing.T, server *httptest.Server, deviceID string, headers http.Header) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/?device=" + deviceID
	connection, response, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("dial: %v (status %d)", err, response.StatusCode)
		}
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func sendJSON(t *testing.T, connection *websocket.Conn, payload any) {
	t.Helper()
	if err := connection.WriteJSON(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readEnvelope(t *testing.T, connection *websocket.Conn) map[string]any {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	_, data, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return payload
}

func sendHello(t *testing.T, connection *websocket.Conn, name string, endpoints []map[string]any) {
	t.Helper()
	payload := map[string]any{
		"type":   "hello",
		"device": map[string]any{"name": name, "platform": "android"},
	}
	if endpoints != nil {
		payload["endpoints"] = endpoints
	}
	sendJSON(t, connection, payload)
}

func TestRegistrationPresenceAndHeartbeat(t *testing.T) {
	server, _ := newTestHub(t, nil)

	phone := dialTestClient(t, server, phoneDeviceID, nil)
	sendHello(t, phone, "Phone A", []map[string]any{
		{"network": "lan", "ip": "192.168.1.20", "port": 9527},
	})

	welcome := readEnvelope(t, phone)
	if welcome["type"] != TypeWelcome {
		t.Fatalf("expected welcome, got %v", welcome)
	}
	if welcome["device_id"] != phoneDeviceID {
		t.Fatalf("welcome device id = %v", welcome["device_id"])
	}
	if welcome["protocol_version"].(float64) != ProtocolVersion {
		t.Fatalf("protocol version = %v", welcome["protocol_version"])
	}
	if devices, _ := welcome["devices"].([]any); len(devices) != 0 {
		t.Fatalf("first device should see no peers: %v", devices)
	}

	pc := dialTestClient(t, server, pcDeviceID, nil)
	sendHello(t, pc, "PC C", []map[string]any{
		{"network": "vpn", "ip": "100.64.0.8", "port": 9527},
	})
	pcWelcome := readEnvelope(t, pc)
	devices, _ := pcWelcome["devices"].([]any)
	if len(devices) != 1 {
		t.Fatalf("expected one peer in welcome, got %v", pcWelcome["devices"])
	}
	peer := devices[0].(map[string]any)
	if peer["device_id"] != phoneDeviceID || peer["name"] != "Phone A" {
		t.Fatalf("unexpected peer: %+v", peer)
	}

	online := readEnvelope(t, phone)
	if online["type"] != TypePresence || online["state"] != PresenceOnline {
		t.Fatalf("expected online presence, got %v", online)
	}
	if online["device"].(map[string]any)["device_id"] != pcDeviceID {
		t.Fatalf("unexpected presence device: %v", online["device"])
	}

	sendJSON(t, pc, map[string]any{
		"type": "heartbeat",
		"endpoints": []map[string]any{
			{"network": "vpn", "ip": "100.64.0.9", "port": 9527},
		},
	})
	ack := readEnvelope(t, pc)
	if ack["type"] != TypeHeartbeatAck || ack["endpoints_changed"] != true {
		t.Fatalf("unexpected heartbeat ack: %v", ack)
	}

	updated := readEnvelope(t, phone)
	if updated["type"] != TypePresence || updated["state"] != PresenceUpdated {
		t.Fatalf("expected updated presence, got %v", updated)
	}
	updatedEndpoints := updated["device"].(map[string]any)["endpoints"].([]any)
	if len(updatedEndpoints) != 1 || updatedEndpoints[0].(map[string]any)["ip"] != "100.64.0.9" {
		t.Fatalf("endpoint update not broadcast: %v", updatedEndpoints)
	}

	if err := pc.Close(); err != nil {
		t.Fatalf("close pc: %v", err)
	}
	offline := readEnvelope(t, phone)
	if offline["type"] != TypePresence || offline["state"] != PresenceOffline {
		t.Fatalf("expected offline presence, got %v", offline)
	}
	if offline["device"].(map[string]any)["device_id"] != pcDeviceID {
		t.Fatalf("unexpected offline device: %v", offline["device"])
	}
}

func TestHelloTimeoutClosesConnection(t *testing.T) {
	server, _ := newTestHub(t, func(options *Options) {
		options.HelloTimeout = 250 * time.Millisecond
	})
	connection := dialTestClient(t, server, phoneDeviceID, nil)

	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	for {
		_, _, err := connection.ReadMessage()
		if err == nil {
			continue
		}
		var netError net.Error
		if errors.As(err, &netError) && netError.Timeout() {
			t.Fatal("connection was not closed after the hello timeout")
		}
		return
	}
}

func TestInvalidEndpointsCanBeRetried(t *testing.T) {
	server, _ := newTestHub(t, nil)
	connection := dialTestClient(t, server, phoneDeviceID, nil)

	sendHello(t, connection, "Phone A", []map[string]any{
		{"network": "lan", "ip": "127.0.0.1", "port": 9527},
	})
	rejection := readEnvelope(t, connection)
	if rejection["type"] != TypeError || rejection["code"] != CodeInvalidEndpoints {
		t.Fatalf("expected invalid_endpoints error, got %v", rejection)
	}
	if rejection["fatal"] != false {
		t.Fatalf("endpoint rejection must not be fatal: %v", rejection)
	}

	sendHello(t, connection, "Phone A", []map[string]any{
		{"network": "lan", "ip": "192.168.1.20", "port": 9527},
	})
	welcome := readEnvelope(t, connection)
	if welcome["type"] != TypeWelcome {
		t.Fatalf("expected welcome after retry, got %v", welcome)
	}
}

func TestUnsupportedMessageAndInvalidJSON(t *testing.T) {
	server, _ := newTestHub(t, nil)
	connection := dialTestClient(t, server, phoneDeviceID, nil)
	sendHello(t, connection, "Phone A", nil)
	if welcome := readEnvelope(t, connection); welcome["type"] != TypeWelcome {
		t.Fatalf("expected welcome, got %v", welcome)
	}

	sendJSON(t, connection, map[string]any{"type": "publish_record"})
	unsupported := readEnvelope(t, connection)
	if unsupported["type"] != TypeError || unsupported["code"] != CodeUnsupportedMessage {
		t.Fatalf("unexpected response: %v", unsupported)
	}

	if err := connection.WriteMessage(websocket.TextMessage, []byte("{not json")); err != nil {
		t.Fatalf("write: %v", err)
	}
	invalid := readEnvelope(t, connection)
	if invalid["type"] != TypeError || invalid["code"] != CodeInvalidMessage {
		t.Fatalf("unexpected response: %v", invalid)
	}
}

func TestOriginPolicy(t *testing.T) {
	server, _ := newTestHub(t, func(options *Options) {
		options.AllowedOrigins = []string{"https://app.example.com"}
	})
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/?device=" + phoneDeviceID

	_, response, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"https://evil.example"}})
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a disallowed origin, got error=%v response=%v", err, response)
	}

	allowed, response, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"https://app.example.com"}})
	if err != nil {
		t.Fatalf("allowed origin rejected: %v (response %v)", err, response)
	}
	t.Cleanup(func() { _ = allowed.Close() })
	sendHello(t, allowed, "Phone A", nil)
	if welcome := readEnvelope(t, allowed); welcome["type"] != TypeWelcome {
		t.Fatalf("expected welcome, got %v", welcome)
	}
}

func TestUnauthenticatedUpgradeRejected(t *testing.T) {
	devices := registry.New(4, 4, nil)
	hub, err := NewHub(testHubOptions(), devices, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	server := httptest.NewServer(hub)
	t.Cleanup(func() {
		context, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = hub.Shutdown(context)
		server.Close()
	})

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestFatalErrorIsFlushedBeforeClose(t *testing.T) {
	options := testHubOptions()
	devices := registry.New(0, options.MaxEndpointsPerDevice, nil)
	hub, err := NewHub(options, devices, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := auth.Identity{
			DeviceID:        phoneDeviceID,
			Fingerprint:     phoneDeviceID,
			ProxyIP:         netip.MustParseAddr("127.0.0.1"),
			AuthenticatedAt: time.Now().UTC(),
		}
		hub.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), identity)))
	})
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		context, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hub.Shutdown(context)
		server.Close()
	})

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	connection, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer connection.Close()

	sendHello(t, connection, "Phone A", nil)
	failure := readEnvelope(t, connection)
	if failure["type"] != TypeError || failure["code"] != CodeTooManyDevices || failure["fatal"] != true {
		t.Fatalf("expected fatal too_many_devices, got %v", failure)
	}

	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	_, _, err = connection.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseTryAgainLater) {
		t.Fatalf("expected close code %d, got %v", websocket.CloseTryAgainLater, err)
	}
}

func TestShutdownClosesConnections(t *testing.T) {
	server, hub := newTestHub(t, nil)
	connection := dialTestClient(t, server, phoneDeviceID, nil)
	sendHello(t, connection, "Phone A", nil)
	if welcome := readEnvelope(t, connection); welcome["type"] != TypeWelcome {
		t.Fatalf("expected welcome, got %v", welcome)
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := hub.Shutdown(shutdownContext); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	var readError error
	for {
		if _, _, readError = connection.ReadMessage(); readError != nil {
			break
		}
	}
	var netError net.Error
	if errors.As(readError, &netError) && netError.Timeout() {
		t.Fatal("shutdown did not close the websocket")
	}
}

func TestTokenBucket(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	bucket := newTokenBucket(10, now)

	if bucket.burst != 20 {
		t.Fatalf("burst = %v, want 20", bucket.burst)
	}
	for i := 0; i < 20; i++ {
		if !bucket.Allow() {
			t.Fatalf("token %d rejected inside the burst", i)
		}
	}
	if bucket.Allow() {
		t.Fatal("bucket did not enforce the burst limit")
	}

	clock = clock.Add(100 * time.Millisecond)
	if !bucket.Allow() {
		t.Fatal("bucket did not refill")
	}
	if bucket.Allow() {
		t.Fatal("bucket refilled more than one token")
	}
}
