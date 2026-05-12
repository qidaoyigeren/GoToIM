package router

import (
	"encoding/json"
	"fmt"

	"github.com/Terry-Mao/goim/api/protocol"
)

// externalMessage is the JSON format used by HTTP API clients.
type externalMessage struct {
	From int64  `json:"from"`
	To   int64  `json:"to"`
	Text string `json:"text"`
	Ts   int64  `json:"ts"`
}

// ProtocolConverter translates between external business messages
// (JSON from HTTP API) and the internal wire format (protocol.MsgBody protobuf).
type ProtocolConverter struct{}

// NewProtocolConverter creates a new ProtocolConverter.
func NewProtocolConverter() *ProtocolConverter {
	return &ProtocolConverter{}
}

// ExternalToInternal converts an external JSON business message to internal MsgBody protobuf.
// Input: JSON bytes {"from":1001,"to":1002,"text":"hello","ts":1699000000}
// Output: protobuf-encoded MsgBody bytes
func (c *ProtocolConverter) ExternalToInternal(externalMsg []byte) ([]byte, error) {
	var ext externalMessage
	if err := json.Unmarshal(externalMsg, &ext); err != nil {
		return nil, fmt.Errorf("unmarshal external message: %w", err)
	}
	if ext.From == 0 || ext.To == 0 {
		return nil, fmt.Errorf("external message missing from/to fields")
	}
	msgBody := &protocol.MsgBody{
		FromUID:   ext.From,
		ToUID:     ext.To,
		Timestamp: ext.Ts,
		Content:   []byte(ext.Text),
	}
	return protocol.MarshalMsgBody(msgBody)
}

// InternalToExternal converts internal MsgBody protobuf to external JSON format.
// Input: protobuf-encoded MsgBody bytes
// Output: JSON bytes {"from":1001,"to":1002,"text":"hello","ts":1699000000}
func (c *ProtocolConverter) InternalToExternal(internal []byte) ([]byte, error) {
	msgBody, err := protocol.UnmarshalMsgBody(internal)
	if err != nil {
		return nil, fmt.Errorf("unmarshal internal message: %w", err)
	}
	ext := externalMessage{
		From: msgBody.FromUID,
		To:   msgBody.ToUID,
		Text: string(msgBody.Content),
		Ts:   msgBody.Timestamp,
	}
	return json.Marshal(ext)
}
