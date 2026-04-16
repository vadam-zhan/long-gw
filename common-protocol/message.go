// Package proto defines the canonical wire protocol for the long-connection gateway.
//
// Design Philosophy
// ─────────────────
// A single Message type serves all business domains (IM, Push, Live, etc.).
// Extensibility is achieved via a typed Header map and a polymorphic Body,
// rather than creating domain-specific message structs that would couple the
// gateway to individual business needs.
//
// Wire format (per frame):
//
//	┌──────────┬────────────┬──────────────────────────────────┐
//	│ 4 bytes  │  1 byte    │  N bytes                         │
//	│ Total Len│  Codec     │  Encoded Message (JSON/Protobuf) │
//	└──────────┴────────────┴──────────────────────────────────┘
//
// Total Len includes the codec byte and the encoded payload.
package proto

import (
	"encoding/json"
	"fmt"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Enumeration types
// ─────────────────────────────────────────────────────────────────────────────

// MsgType identifies the high-level purpose of a message.
// Values 0–99 are reserved for gateway control plane.
// Values 100–999 are reserved for standard business domains.
// Values 1000+ are available for custom/private use.
type MsgType uint16

const (
	// ── Control plane (gateway-internal) ─────────────────────────────────────

	MsgTypeHandshake    MsgType = 1 // Client → Gateway: initial auth handshake
	MsgTypeHandshakeAck MsgType = 2 // Gateway → Client: handshake result
	MsgTypePing         MsgType = 3 // Client → Gateway: heartbeat probe
	MsgTypePong         MsgType = 4 // Gateway → Client: heartbeat reply
	MsgTypeKick         MsgType = 5 // Gateway → Client: forced disconnect notice
	MsgTypeError        MsgType = 6 // bidirectional: error notification

	// ── Data plane ───────────────────────────────────────────────────────────

	// Universal upstream/downstream data frame. The Body.Type field provides
	// finer-grained routing within a business domain.
	MsgTypeData MsgType = 100

	// ── IM domain (101–199) ──────────────────────────────────────────────────

	MsgTypeIMChat    MsgType = 101 // single/group chat message
	MsgTypeIMReceipt MsgType = 102 // delivery / read receipt
	MsgTypeIMRevoke  MsgType = 103 // message revocation
	MsgTypeIMTyping  MsgType = 104 // typing indicator

	// ── Push domain (201–299) ────────────────────────────────────────────────

	MsgTypePushNotify MsgType = 201 // server-initiated push notification
	MsgTypePushAck    MsgType = 202 // client acknowledges push received

	// ── Live / streaming domain (301–399) ────────────────────────────────────

	MsgTypeLiveJoin    MsgType = 301 // join a live room
	MsgTypeLiveLeave   MsgType = 302 // leave a live room
	MsgTypeLiveComment MsgType = 303 // barrage / comment
	MsgTypeLiveGift    MsgType = 304 // gift event
	MsgTypeLiveSignal  MsgType = 305 // signal (mic-on, camera-on, etc.)
)

// QoS defines the delivery guarantee level, modeled after MQTT semantics.
type QoS uint8

const (
	QoSAtMostOnce  QoS = 0 // fire-and-forget, no ACK required
	QoSAtLeastOnce QoS = 1 // retried until ACK; may duplicate
	QoSExactlyOnce QoS = 2 // dedup on receiver side; highest cost
)

// Codec identifies the serialization format used for the message body.
type Codec uint8

const (
	CodecJSON     Codec = 1
	CodecProtobuf Codec = 2
	CodecMsgpack  Codec = 3
)

// CompressAlgo identifies the optional compression algorithm.
type CompressAlgo uint8

const (
	CompressNone   CompressAlgo = 0
	CompressGzip   CompressAlgo = 1
	CompressZstd   CompressAlgo = 2
	CompressSnappy CompressAlgo = 3
)

// ─────────────────────────────────────────────────────────────────────────────
// Core Message
// ─────────────────────────────────────────────────────────────────────────────

// Message is the canonical unit of communication between SDK and gateway.
// It is intentionally flat at the top level to allow O(1) routing decisions
// without deserializing the Body.
type Message struct {
	// ── Framing & identity ───────────────────────────────────────────────────

	// Version allows the gateway to handle protocol upgrades gracefully.
	// Current version: 1.
	Version uint8 `json:"v"`

	// Type drives routing and processing pipelines on both gateway and SDK.
	Type MsgType `json:"t"`

	// MsgID is a client-generated UUID (or snowflake) that uniquely identifies
	// this message within a session. Used for dedup and ACK correlation.
	MsgID string `json:"mid"`

	// SeqID is a per-sender monotonically increasing sequence number.
	// Receivers use it to detect gaps and request retransmission.
	SeqID uint64 `json:"seq"`

	// Timestamp is Unix milliseconds at message creation on the sender side.
	Timestamp int64 `json:"ts"`

	// ── Addressing ───────────────────────────────────────────────────────────

	// From is the sender identity. Format: "{domain}:{uid}", e.g. "im:user_123".
	// Empty for server-originating control messages.
	From string `json:"from,omitempty"`

	// To is the target identity. Supports four address types:
	//   uid:  "u:{uid}"            → single user
	//   room: "r:{roomID}"         → all connections in a room
	//   group:"g:{groupID}"        → group members (resolved server-side)
	//   topic:"t:{topicName}"      → pub/sub topic subscribers
	//   conn: "c:{connID}"         → raw connection (internal use only)
	To string `json:"to,omitempty"`

	// ── Delivery control ─────────────────────────────────────────────────────

	// QoS controls the delivery guarantee. Default: QoSAtMostOnce.
	QoS QoS `json:"qos,omitempty"`

	// AckID is populated by the gateway when it confirms receipt of a
	// QoSAtLeastOnce / QoSExactlyOnce message. SDK uses it to stop retrying.
	AckID string `json:"ack_id,omitempty"`

	// ExpireAt is Unix milliseconds. The message is dropped (not delivered)
	// after this time. Zero means no expiry.
	ExpireAt int64 `json:"expire_at,omitempty"`

	// Offline controls whether the message should be stored for offline users.
	// When true and the target is offline, the gateway persists it and delivers
	// on next connection.
	Offline bool `json:"offline,omitempty"`

	// ── Routing hints ────────────────────────────────────────────────────────

	// BizCode identifies the business domain. Gateway plugins use this to apply
	// domain-specific middleware (e.g. content moderation for IM, throttle for Live).
	// Convention: "im", "push", "live", "custom.*"
	BizCode string `json:"biz,omitempty"`

	// TraceID carries the distributed trace context for observability.
	TraceID string `json:"trace_id,omitempty"`

	// ── Encoding hints (applied to Body only) ────────────────────────────────

	// BodyCodec specifies how Body.Payload is encoded.
	// Receivers MUST check this before unmarshaling Payload.
	BodyCodec Codec `json:"body_codec,omitempty"`

	// Compress specifies the compression applied to Body.Payload after encoding.
	Compress CompressAlgo `json:"compress,omitempty"`

	// ── Extension ────────────────────────────────────────────────────────────

	// Headers carries optional key-value metadata. Gateway middleware may read
	// and write headers (e.g. "x-retry-count", "x-content-type", "x-priority").
	// Keys must be lowercase, values must be strings.
	Headers map[string]string `json:"headers,omitempty"`

	// Body carries the domain-specific payload. See Body below.
	Body *Body `json:"body,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Body — polymorphic payload container
// ─────────────────────────────────────────────────────────────────────────────

// Body wraps the domain payload. Keeping Payload as raw JSON allows the gateway
// to forward messages between services without re-encoding: the gateway only
// inspects the outer Message fields for routing.
type Body struct {
	// Type is a domain-specific sub-type string, e.g.:
	//   IM:   "text", "image", "video", "file", "location", "custom"
	//   Push: "alert", "silent", "badge"
	//   Live: "comment", "gift", "pklive", "anchor_info"
	Type string `json:"type"`

	// Payload is the business payload encoded according to Message.BodyCodec.
	// Using json.RawMessage avoids double-encoding and lets the gateway
	// forward transparently without touching business logic.
	Payload json.RawMessage `json:"payload"`

	// Ext is a free-form map for lightweight metadata that does not warrant
	// a dedicated field. Business teams own this namespace.
	// Example: {"mention_uids": ["u1","u2"], "reply_to_mid": "msg_abc"}
	Ext map[string]interface{} `json:"ext,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Well-known payload schemas
// ─────────────────────────────────────────────────────────────────────────────
// These structs define the canonical shape of Body.Payload for each domain.
// They are used by the SDK helpers; the gateway itself never deserializes them.

// HandshakePayload is sent by the client immediately after TCP/WebSocket connect.
type HandshakePayload struct {
	// AppID identifies the application (multi-tenant gateway support).
	AppID string `json:"app_id"`

	// Token is a short-lived JWT issued by the business auth service.
	Token string `json:"token"`

	// DeviceID is a stable device fingerprint (persists across sessions).
	DeviceID string `json:"device_id"`

	// DeviceType: "ios", "android", "web", "pc", "iot"
	DeviceType string `json:"device_type"`

	// SDKVersion allows the gateway to negotiate capabilities.
	SDKVersion string `json:"sdk_version"`

	// LastSeqID is the highest SeqID the client received in its previous session.
	// Gateway uses this to replay missed messages on reconnect.
	LastSeqID uint64 `json:"last_seq_id,omitempty"`
}

// HandshakeAckPayload is the gateway's response to HandshakePayload.
type HandshakeAckPayload struct {
	// ConnID is the gateway-assigned unique connection identifier.
	ConnID string `json:"conn_id"`

	// UserID is the resolved user identity after token validation.
	UserID string `json:"user_id"`

	// ServerTime is Unix milliseconds; clients use it to calibrate clock skew.
	ServerTime int64 `json:"server_time"`

	// HeartbeatInterval is the recommended Ping interval in seconds.
	HeartbeatInterval int `json:"heartbeat_interval"`

	// MaxBodySize is the maximum allowed Body.Payload size in bytes.
	MaxBodySize int64 `json:"max_body_size"`

	// ReplayFrom is the SeqID from which the gateway will start replaying.
	// Equal to LastSeqID+1 from the handshake if replay is supported.
	ReplayFrom uint64 `json:"replay_from,omitempty"`

	// Extensions lists gateway capabilities the client can use.
	// e.g. ["compress:zstd", "qos:2", "multi-device"]
	Extensions []string `json:"extensions,omitempty"`
}

// KickPayload explains why the gateway is terminating the connection.
type KickPayload struct {
	// Code is a machine-readable reason code.
	Code int `json:"code"`

	// Reason is human-readable text for debugging.
	Reason string `json:"reason"`

	// ReconnectAfter, if positive, tells the client to wait this many seconds
	// before attempting to reconnect (e.g. during rolling restarts).
	ReconnectAfter int `json:"reconnect_after,omitempty"`
}

// ErrorPayload is used by MsgTypeError frames.
type ErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
	// RefMsgID is the MsgID of the message that caused this error, if applicable.
	RefMsgID string `json:"ref_mid,omitempty"`
}

// ── IM payloads ──────────────────────────────────────────────────────────────

// IMChatPayload is the payload for MsgTypeIMChat with Body.Type = "text"|"image"|etc.
type IMChatPayload struct {
	// ConvID is the conversation identifier (single chat: sorted uid pair, group: groupID).
	ConvID string `json:"conv_id"`

	// ContentType: "text", "image", "video", "audio", "file", "location", "custom"
	ContentType string `json:"content_type"`

	// Content is the raw content; structure depends on ContentType.
	Content json.RawMessage `json:"content"`

	// Mentions lists UIDs explicitly mentioned. Gateway may use to trigger extra delivery.
	Mentions []string `json:"mentions,omitempty"`

	// ReplyTo is the MsgID this message replies to, for thread support.
	ReplyTo string `json:"reply_to,omitempty"`
}

// IMReceiptPayload is the payload for delivery/read receipts.
type IMReceiptPayload struct {
	// ConvID scopes the receipt.
	ConvID string `json:"conv_id"`

	// ReceiptType: "delivered" | "read"
	ReceiptType string `json:"receipt_type"`

	// MsgIDs are the messages being acknowledged.
	MsgIDs []string `json:"msg_ids"`

	// ReadSeq is the highest SeqID the user has read; used for "read watermark" style UIs.
	ReadSeq uint64 `json:"read_seq,omitempty"`
}

// ── Push payloads ─────────────────────────────────────────────────────────────

// PushNotifyPayload is the payload for MsgTypePushNotify.
type PushNotifyPayload struct {
	// Title and Body are the visible notification content.
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`

	// Sound: "default" | "" (silent) | custom sound name
	Sound string `json:"sound,omitempty"`

	// Badge is the app icon badge count. -1 means no change.
	Badge int `json:"badge,omitempty"`

	// Data is arbitrary key-value for the app to handle silently.
	Data map[string]string `json:"data,omitempty"`

	// Priority: "high" | "normal". High wakes the device immediately.
	Priority string `json:"priority,omitempty"`

	// CollapseKey allows the gateway to replace earlier undelivered pushes
	// with the same key (like FCM collapse_key).
	CollapseKey string `json:"collapse_key,omitempty"`
}

// ── Live payloads ─────────────────────────────────────────────────────────────

// LiveCommentPayload is the payload for MsgTypeLiveComment.
type LiveCommentPayload struct {
	RoomID  string `json:"room_id"`
	Content string `json:"content"`
	// Color is an optional rich text color (hex), used for paid "colored" comments.
	Color string `json:"color,omitempty"`
}

// LiveGiftPayload is the payload for MsgTypeLiveGift.
type LiveGiftPayload struct {
	RoomID   string `json:"room_id"`
	GiftID   string `json:"gift_id"`
	GiftName string `json:"gift_name"`
	Count    int    `json:"count"`
	// TotalCost is the gateway-verified cost in virtual currency units.
	TotalCost int64 `json:"total_cost"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Builder helpers — for constructing common messages without boilerplate
// ─────────────────────────────────────────────────────────────────────────────

// NewMessage returns a Message with Version, MsgID, and Timestamp pre-filled.
// Callers set Type, From, To, and Body.
func NewMessage(msgType MsgType) *Message {
	return &Message{
		Version:   1,
		Type:      msgType,
		MsgID:     newMsgID(),
		Timestamp: time.Now().UnixMilli(),
	}
}

// WithQoS sets the delivery guarantee and is chainable.
func (m *Message) WithQoS(q QoS) *Message {
	m.QoS = q
	return m
}

// WithOffline enables offline message storage and is chainable.
func (m *Message) WithOffline() *Message {
	m.Offline = true
	return m
}

// WithTTL sets an absolute expiry and is chainable.
func (m *Message) WithTTL(d time.Duration) *Message {
	m.ExpireAt = time.Now().Add(d).UnixMilli()
	return m
}

// WithHeader adds a single header and is chainable.
func (m *Message) WithHeader(key, value string) *Message {
	if m.Headers == nil {
		m.Headers = make(map[string]string)
	}
	m.Headers[key] = value
	return m
}

// WithTrace sets the distributed trace ID and is chainable.
func (m *Message) WithTrace(traceID string) *Message {
	m.TraceID = traceID
	return m
}

// SetJSONBody serializes payload as JSON and sets it as the Body.
func (m *Message) SetJSONBody(bodyType string, payload interface{}, ext map[string]interface{}) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("proto: marshal body payload: %w", err)
	}
	m.BodyCodec = CodecJSON
	m.Body = &Body{
		Type:    bodyType,
		Payload: raw,
		Ext:     ext,
	}
	return nil
}

// DecodePayload unmarshals Body.Payload into dst.
// Currently supports JSON codec only; extend for Protobuf/Msgpack as needed.
func (m *Message) DecodePayload(dst interface{}) error {
	if m.Body == nil {
		return fmt.Errorf("proto: message has no body")
	}
	switch m.BodyCodec {
	case CodecJSON, 0:
		return json.Unmarshal(m.Body.Payload, dst)
	default:
		return fmt.Errorf("proto: unsupported codec %d", m.BodyCodec)
	}
}

// IsExpired reports whether the message has passed its expiry time.
func (m *Message) IsExpired() bool {
	return m.ExpireAt > 0 && time.Now().UnixMilli() > m.ExpireAt
}

// newMsgID returns a time-prefixed pseudo-unique ID.
// Production code should use UUID v4 or a Snowflake generator.
func newMsgID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
