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
	TypeHello         = "hello"
	TypeHeartbeat     = "heartbeat"
	TypePing          = "ping"
	TypeWelcome       = "welcome"
	TypeHeartbeatAck  = "heartbeat_ack"
	TypePong          = "pong"
	TypePresence      = "presence"
	TypeError         = "error"
	TypePublishRecord = "publish_record"
	TypeSyncRecord    = "sync_record"
	TypeRecordAck     = "record_ack"
	TypeOpenSession   = "open_session"
	TypeSessionTicket = "session_ticket"
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
	CodeInvalidRecord      = "invalid_record"
	CodeStorageUnavailable = "storage_unavailable"
	CodePeerUnavailable    = "peer_unavailable"
	CodeSessionFailed      = "session_failed"
)

// inbound is a control-plane frame sent by a client.
type inbound struct {
	Type         string               `json:"type"`
	Device       *deviceMetadata      `json:"device,omitempty"`
	Endpoints    *[]registry.Endpoint `json:"endpoints,omitempty"`
	Record       *recordPayload       `json:"record,omitempty"`
	ToDeviceID   string               `json:"to_device_id,omitempty"`
	TransferName string               `json:"transfer_name,omitempty"`
}

type deviceMetadata struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

// DeviceView is the JSON representation of a registered device.
type DeviceView struct {
	DeviceID    string              `json:"device_id"`
	Fingerprint string              `json:"fingerprint,omitempty"`
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
		Fingerprint: device.Fingerprint,
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
	ServerVersion       string       `json:"server_version,omitempty"`
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

// sessionTicketMessage is delivered to both participants of a transfer. The
// ticket authorizes exactly one (from, to) session and expires quickly.
type sessionTicketMessage struct {
	Type         string       `json:"type"`
	SessionID    string       `json:"session_id"`
	Ticket       string       `json:"ticket"`
	FromDeviceID string       `json:"from_device_id"`
	ToDeviceID   string       `json:"to_device_id"`
	From         DeviceView   `json:"from"`
	To           DeviceView   `json:"to"`
	Devices      []DeviceView `json:"devices"`
	RelayURL     string       `json:"relay_url"`
	ExpiresAt    int64        `json:"expires_at"`
	TransferName string       `json:"transfer_name,omitempty"`
}

// recordPayload is the client-supplied sync record in a publish_record frame.
type recordPayload struct {
	RecordID       string           `json:"record_id,omitempty"`
	SenderDeviceID string           `json:"sender_device_id,omitempty"`
	CreatedAt      int64            `json:"created_at,omitempty"`
	Type           string           `json:"type,omitempty"`
	TextContent    string           `json:"text_content,omitempty"`
	FileMeta       *fileMetaPayload `json:"file_meta,omitempty"`
}

type fileMetaPayload struct {
	BlobID     string `json:"blob_id,omitempty"`
	FileName   string `json:"file_name,omitempty"`
	FileSize   int64  `json:"file_size,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
	Blake3Hash string `json:"blake3_hash,omitempty"`
}

type syncRecordMessage struct {
	Type   string         `json:"type"`
	Record syncRecordView `json:"record"`
}

type syncRecordView struct {
	RecordID       string           `json:"record_id"`
	SenderDeviceID string           `json:"sender_device_id"`
	SenderName     string           `json:"sender_name,omitempty"`
	CreatedAt      int64            `json:"created_at"`
	Type           string           `json:"type"`
	TextContent    string           `json:"text_content,omitempty"`
	FileMeta       *fileMetaPayload `json:"file_meta,omitempty"`
}

type recordAckMessage struct {
	Type      string `json:"type"`
	RecordID  string `json:"record_id"`
	CreatedAt int64  `json:"created_at"`
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
