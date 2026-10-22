package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/molirain/molisync/server/internal/auth"
	"github.com/molirain/molisync/server/internal/registry"
	"github.com/molirain/molisync/server/internal/ws"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	middleware, err := auth.NewMiddleware(auth.Options{
		VerifyHeader:              "X-Client-Verify",
		FingerprintHeader:         "X-Client-Fingerprint",
		DNHeader:                  "X-Client-DN",
		RequiredVerifyValue:       "SUCCESS",
		DeviceIDSource:            "fingerprint",
		MaxHeaderValueBytes:       1024,
		MaxCommonNameLength:       128,
		MaxDistinguishedNameBytes: 1024,
	}, []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, logger)
	if err != nil {
		t.Fatalf("auth.NewMiddleware: %v", err)
	}

	devices := registry.New(8, 4, nil)
	hub, err := ws.NewHub(ws.Options{
		HandshakeTimeout:      5 * time.Second,
		HelloTimeout:          5 * time.Second,
		PingInterval:          10 * time.Second,
		PongTimeout:           30 * time.Second,
		WriteTimeout:          5 * time.Second,
		ReadLimitBytes:        64 << 10,
		SendBuffer:            16,
		HeartbeatInterval:     10 * time.Second,
		MaxMessagesPerSecond:  20,
		MaxEndpointsPerDevice: 4,
		OfflineAfter:          time.Minute,
		SweepInterval:         30 * time.Second,
	}, devices, logger)
	if err != nil {
		t.Fatalf("ws.NewHub: %v", err)
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = hub.Shutdown(shutdownContext)
	})

	return NewRouter(Deps{Auth: middleware, Hub: hub, Logger: logger})
}

func performRequest(t *testing.T, router http.Handler, method, path, remote string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	request.RemoteAddr = remote
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestHealthEndpoint(t *testing.T) {
	router := newTestRouter(t)
	recorder := performRequest(t, router, http.MethodGet, "/healthz", "127.0.0.1:9000", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" || body.Time == 0 {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestMeEndpoint(t *testing.T) {
	router := newTestRouter(t)
	fingerprint := strings.Repeat("ab", 32)
	recorder := performRequest(t, router, http.MethodGet, "/api/v1/me", "127.0.0.1:9000", map[string]string{
		"X-Client-Verify":      "SUCCESS",
		"X-Client-Fingerprint": fingerprint,
		"X-Client-DN":          "CN=phone-a,O=Molirain",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body meResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.DeviceID != fingerprint || body.CommonName != "phone-a" || body.ProxyIP != "127.0.0.1" {
		t.Fatalf("unexpected identity: %+v", body)
	}
}

func TestMeEndpointRequiresVerification(t *testing.T) {
	router := newTestRouter(t)
	recorder := performRequest(t, router, http.MethodGet, "/api/v1/me", "127.0.0.1:9000", nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = performRequest(t, router, http.MethodGet, "/api/v1/me", "10.1.2.3:9000", map[string]string{
		"X-Client-Verify":      "SUCCESS",
		"X-Client-Fingerprint": strings.Repeat("ab", 32),
	})
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("untrusted peer status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestDevicesEndpoint(t *testing.T) {
	router := newTestRouter(t)
	recorder := performRequest(t, router, http.MethodGet, "/api/v1/devices", "127.0.0.1:9000", map[string]string{
		"X-Client-Verify":      "SUCCESS",
		"X-Client-Fingerprint": strings.Repeat("ab", 32),
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body devicesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Devices == nil || len(body.Devices) != 0 {
		t.Fatalf("expected an empty device array, got %+v", body.Devices)
	}
}

func TestUnknownRouteIsJSON(t *testing.T) {
	router := newTestRouter(t)
	recorder := performRequest(t, router, http.MethodGet, "/api/v1/nope", "127.0.0.1:9000", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "not_found" {
		t.Fatalf("error code = %q", body.Error.Code)
	}
}

func TestWebSocketThroughFullRouter(t *testing.T) {
	router := newTestRouter(t)
	server := httptest.NewServer(router)

	deviceID := strings.Repeat("cd", 32)
	headers := http.Header{
		"X-Client-Verify":      {"SUCCESS"},
		"X-Client-Fingerprint": {deviceID},
	}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws"
	connection, response, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("dial: %v (status %d)", err, response.StatusCode)
		}
		t.Fatalf("dial: %v", err)
	}
	defer func() {
		_ = connection.Close()
		server.Close()
	}()

	if err := connection.WriteJSON(map[string]any{
		"type":      "hello",
		"device":    map[string]any{"name": "Phone A", "platform": "android"},
		"endpoints": []map[string]any{{"network": "lan", "ip": "192.168.1.20", "port": 9527}},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	var welcome struct {
		Type     string `json:"type"`
		DeviceID string `json:"device_id"`
	}
	if err := connection.ReadJSON(&welcome); err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	if welcome.Type != "welcome" || welcome.DeviceID != deviceID {
		t.Fatalf("unexpected welcome: %+v", welcome)
	}

	if err := connection.WriteJSON(map[string]any{"type": "heartbeat"}); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	var ack struct {
		Type string `json:"type"`
	}
	if err := connection.ReadJSON(&ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack.Type != "heartbeat_ack" {
		t.Fatalf("unexpected ack: %+v", ack)
	}
}
