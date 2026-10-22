package ws

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/molirain/molisync/server/internal/p2p"
)

// readUntilType drains control frames until the wanted type arrives. Presence
// events may interleave with the frame under test.
func readUntilType(t *testing.T, connection *websocket.Conn, want string) map[string]any {
	t.Helper()
	for attempt := 0; attempt < 8; attempt++ {
		payload := readEnvelope(t, connection)
		if payload["type"] == want {
			return payload
		}
	}
	t.Fatalf("did not receive a %q frame", want)
	return nil
}

func registerDevice(t *testing.T, connection *websocket.Conn, name string) {
	t.Helper()
	sendHello(t, connection, name, nil)
	readUntilType(t, connection, "welcome")
}

func TestOpenSessionDeliversTicketToBothDevices(t *testing.T) {
	server, hub := newTestHub(t, nil)
	sessions, err := p2p.NewStore(2 * time.Minute)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	hub.SetSessions(sessions)
	hub.SetRelayPath("/api/v1/relay")

	phone := dialTestClient(t, server, phoneDeviceID, nil)
	registerDevice(t, phone, "Phone A")
	pc := dialTestClient(t, server, pcDeviceID, nil)
	registerDevice(t, pc, "Home PC")

	sendJSON(t, phone, map[string]any{
		"type":          "open_session",
		"to_device_id":  pcDeviceID,
		"transfer_name": "photo.png",
	})

	for _, connection := range []*websocket.Conn{phone, pc} {
		frame := readUntilType(t, connection, "session_ticket")
		if frame["from_device_id"] != phoneDeviceID || frame["to_device_id"] != pcDeviceID {
			t.Fatalf("unexpected participants: %v", frame)
		}

		encoded, _ := frame["ticket"].(string)
		ticket, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("ticket is not base64url: %v", err)
		}
		if len(ticket) != p2p.TicketSize {
			t.Fatalf("ticket length = %d, want %d", len(ticket), p2p.TicketSize)
		}

		sessionID, _ := frame["session_id"].(string)
		if sessionID == "" {
			t.Fatal("session_id is missing")
		}
		relayURL, _ := frame["relay_url"].(string)
		if relayURL != "/api/v1/relay/"+sessionID {
			t.Fatalf("relay_url = %q", relayURL)
		}
		devices, _ := frame["devices"].([]any)
		if len(devices) != 2 {
			t.Fatalf("devices = %d, want 2", len(devices))
		}
		expires, _ := frame["expires_at"].(float64)
		if expires <= float64(time.Now().UnixMilli()) {
			t.Fatalf("expires_at %v is not in the future", expires)
		}
	}

	if sessions.Len() != 1 {
		t.Fatalf("sessions = %d, want 1", sessions.Len())
	}
}

func TestOpenSessionRejectsUnknownTarget(t *testing.T) {
	server, hub := newTestHub(t, nil)
	sessions, err := p2p.NewStore(2 * time.Minute)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	hub.SetSessions(sessions)

	phone := dialTestClient(t, server, phoneDeviceID, nil)
	registerDevice(t, phone, "Phone A")

	sendJSON(t, phone, map[string]any{
		"type":         "open_session",
		"to_device_id": pcDeviceID,
	})

	frame := readUntilType(t, phone, "error")
	if frame["code"] != CodePeerUnavailable {
		t.Fatalf("code = %v, want %s", frame["code"], CodePeerUnavailable)
	}
	if sessions.Len() != 0 {
		t.Fatalf("sessions = %d, want 0", sessions.Len())
	}
}

func TestOpenSessionRejectsSelfTransfer(t *testing.T) {
	server, hub := newTestHub(t, nil)
	sessions, err := p2p.NewStore(2 * time.Minute)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	hub.SetSessions(sessions)

	phone := dialTestClient(t, server, phoneDeviceID, nil)
	registerDevice(t, phone, "Phone A")

	sendJSON(t, phone, map[string]any{
		"type":         "open_session",
		"to_device_id": phoneDeviceID,
	})

	frame := readUntilType(t, phone, "error")
	if frame["type"] != "error" {
		t.Fatalf("unexpected frame: %v", frame)
	}
}
