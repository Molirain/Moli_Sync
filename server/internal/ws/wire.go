package ws

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/molirain/molisync/server/internal/registry"
)

// ProtocolVersion is advertised in the welcome message. Clients must reject
// versions they do not understand.
const ProtocolVersion = 1

// Control-plane message types. The JSON wire format mirrors proto/sync.proto
// field for field; every frame is a single JSON object with a "type" tag.
const (
	TypeHello        = "hello"
	TypeHeartbeat    = "heartbeat"
	TypePing         = "ping"
	TypeWelcome      = "welcome"
	TypeHeartbeatAck = "heartbeat_ack"
	TypePong         = "pong"
	TypePresence     = "presence"
	TypeError        = "error"
)

// Presence states broadcast to other devices.
const (
	PresenceOnline  = "online"
	PresenceUpdated = "updated"
	PresenceOffline = "offline"
)

// Machine readable error codes returned in error messages.
const (
	CodeInvalidMessage     = "invalid_message"
	CodeUnsupportedMessage = "unsupported_message"
	CodeNotRegistered      = "not_registered"
	CodeAlreadyRegistered  = "already_registered"
	CodeInvalidDevice      = "invalid_device"
	CodeInvalidEndpoints   = "invalid_endpoints"
	CodeTooManyDevices     = "too_many_devices"
	CodeRateLimited        = "rate_limited"
	CodeServerShuttingDown = "server_shutting_down"
	CodeInternalError      = "internal_error"
)

// inbound is a control-plane frame sent by a client.
type inbound struct {
	Type      string               `json:"type"`
	Device    *deviceMetadata      `json:"device,omitempty"`
	Endpoints *[]registry.Endpoint `json:"endpoints,omitempty"`
}

type deviceMetadata struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

// DeviceView is the JSON representation of a registered device.
type DeviceView struct {
	DeviceID    string              `json:"device_id"`
	CommonName  string              `json:"common_name,omitempty"`
	Name        string              `json:"name"`
	Platform    string              `json:"platform"`
	Endpoints   []registry.Endpoint `json:"endpoints"`
	ConnectedAt int64               `json:"connected_at"`
	LastSeen    int64               `json:"last_seen"`
	Connections int                 `json:"connections"`
}

func newDeviceView(device registry.Device) DeviceView {
	endpoints := device.Endpoints
	if endpoints == nil {
		endpoints = []registry.Endpoint{}
	}
	return DeviceView{
		DeviceID:    device.ID,
		CommonName:  device.CommonName,
		Name:        device.Name,
		Platform:    device.Platform,
		Endpoints:   endpoints,
		ConnectedAt: device.ConnectedAt.UnixMilli(),
		LastSeen:    device.LastSeen.UnixMilli(),
		Connections: device.Connections,
	}
}

type welcomeMessage struct {
	Type                string       `json:"type"`
	ProtocolVersion     int          `json:"protocol_version"`
	DeviceID            string       `json:"device_id"`
	ServerTime          int64        `json:"server_time"`
	HeartbeatIntervalMS int64        `json:"heartbeat_interval_ms"`
	PongTimeoutMS       int64        `json:"pong_timeout_ms"`
	Devices             []DeviceView `json:"devices"`
}

type heartbeatAckMessage struct {
	Type             string `json:"type"`
	ServerTime       int64  `json:"server_time"`
	EndpointsChanged bool   `json:"endpoints_changed"`
}

type pongMessage struct {
	Type       string `json:"type"`
	ServerTime int64  `json:"server_time"`
}

type presenceMessage struct {
	Type   string     `json:"type"`
	State  string     `json:"state"`
	Device DeviceView `json:"device"`
}

type errorMessage struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}

// outbound is a pre-encoded frame. A non-zero closeCode asks the write pump to
// send a close frame after the payload, which guarantees that fatal errors are
// flushed before the socket is torn down.
type outbound struct {
	messageType int
	data        []byte
	closeCode   int
	closeReason string
}

func newOutbound(payload any) (outbound, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return outbound{}, err
	}
	return outbound{messageType: websocket.TextMessage, data: data}, nil
}

func decodeInbound(data []byte) (inbound, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var message inbound
	if err := decoder.Decode(&message); err != nil {
		return inbound{}, errors.New("payload is not a valid JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return inbound{}, errors.New("payload must contain exactly one JSON object")
	}
	message.Type = strings.ToLower(strings.TrimSpace(message.Type))
	if message.Type == "" {
		return inbound{}, errors.New("message type is required")
	}
	return message, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
