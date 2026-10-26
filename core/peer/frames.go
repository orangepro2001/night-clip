package peer

import "encoding/json"

// Wire frames of the event channel. Every frame is one WebSocket text message
// holding a JSON object with a "type" (docs/api-spec.md §4).
const (
	frameHello    = "hello"
	frameHelloAck = "hello_ack"
	frameEvent    = "event"
	frameAck      = "ack"
	frameSkip     = "skip"
	frameResync   = "resync"
	framePing     = "ping"
	framePong     = "pong"
	frameError    = "error"
	frameGoaway   = "goaway"
)

type frameHead struct {
	Type string `json:"type"`
}

type helloFrame struct {
	Type         string   `json:"type"`
	DeviceID     string   `json:"device_id"`
	Name         string   `json:"name,omitempty"`
	TailnetAddrs []string `json:"tailnet_addrs,omitempty"`
	RecvCursor   int64    `json:"recv_cursor"`
}

type helloAckFrame struct {
	Type          string `json:"type"`
	DeviceID      string `json:"device_id"`
	ServerTime    string `json:"server_time"`
	RecvCursor    int64  `json:"recv_cursor"`
	MaxFrameBytes int    `json:"max_frame_bytes"`
}

type eventFrame struct {
	Type     string          `json:"type"`
	Envelope json.RawMessage `json:"envelope"`
}

type ackFrame struct {
	Type    string  `json:"type"`
	EventID string  `json:"event_id"`
	Result  string  `json:"result"`
	Reason  *string `json:"reason"`
	AckedAt string  `json:"acked_at"`
}

// skipFrame tells the receiver that no sequence <= Through will ever be sent
// again (the events expired or were purged), so its cursor may jump there.
type skipFrame struct {
	Type    string `json:"type"`
	Through int64  `json:"through"`
}

// resyncFrame asks the sender to resend everything after After.
type resyncFrame struct {
	Type  string `json:"type"`
	After int64  `json:"after"`
}

type pingFrame struct {
	Type string `json:"type"` // ping or pong
	TS   string `json:"ts"`
}

type errorFrame struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type goawayFrame struct {
	Type            string `json:"type"`
	Reason          string `json:"reason"`
	ReconnectAfterM int    `json:"reconnect_after_ms"`
}

// ack results (docs/api-spec.md §4.2).
const (
	ResultReceived = "received"
	ResultApplied  = "applied"
	ResultIgnored  = "ignored"
	ResultExpired  = "expired"
	ResultError    = "error"
)
