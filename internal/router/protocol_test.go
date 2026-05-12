package router

import (
	"encoding/json"
	"testing"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/stretchr/testify/assert"
)

func TestProtocolConverterExternalToInternal(t *testing.T) {
	pc := NewProtocolConverter()

	// Valid external message
	ext := externalMessage{
		From: 1001,
		To:   1002,
		Text: "hello world",
		Ts:   1699000000,
	}
	extBytes, err := json.Marshal(ext)
	assert.Nil(t, err)

	internalBytes, err := pc.ExternalToInternal(extBytes)
	assert.Nil(t, err)
	assert.NotEmpty(t, internalBytes)

	// Verify the result is a valid MsgBody
	msgBody, err := protocol.UnmarshalMsgBody(internalBytes)
	assert.Nil(t, err)
	assert.Equal(t, int64(1001), msgBody.FromUID)
	assert.Equal(t, int64(1002), msgBody.ToUID)
	assert.Equal(t, int64(1699000000), msgBody.Timestamp)
	assert.Equal(t, "hello world", string(msgBody.Content))
}

func TestProtocolConverterInternalToExternal(t *testing.T) {
	pc := NewProtocolConverter()

	// Create internal MsgBody
	msgBody := &protocol.MsgBody{
		FromUID:   2001,
		ToUID:     2002,
		Timestamp: 1700000000,
		Content:   []byte("test message"),
	}
	internalBytes, err := protocol.MarshalMsgBody(msgBody)
	assert.Nil(t, err)

	// Convert to external
	extBytes, err := pc.InternalToExternal(internalBytes)
	assert.Nil(t, err)
	assert.NotEmpty(t, extBytes)

	// Verify the result is valid JSON
	var ext externalMessage
	err = json.Unmarshal(extBytes, &ext)
	assert.Nil(t, err)
	assert.Equal(t, int64(2001), ext.From)
	assert.Equal(t, int64(2002), ext.To)
	assert.Equal(t, "test message", ext.Text)
	assert.Equal(t, int64(1700000000), ext.Ts)
}

func TestProtocolConverterRoundTrip(t *testing.T) {
	pc := NewProtocolConverter()

	// External -> Internal -> External should preserve data
	original := externalMessage{
		From: 3001,
		To:   3002,
		Text: "round trip test",
		Ts:   1701000000,
	}
	extBytes, err := json.Marshal(original)
	assert.Nil(t, err)

	internalBytes, err := pc.ExternalToInternal(extBytes)
	assert.Nil(t, err)

	resultBytes, err := pc.InternalToExternal(internalBytes)
	assert.Nil(t, err)

	var result externalMessage
	err = json.Unmarshal(resultBytes, &result)
	assert.Nil(t, err)
	assert.Equal(t, original.From, result.From)
	assert.Equal(t, original.To, result.To)
	assert.Equal(t, original.Text, result.Text)
	assert.Equal(t, original.Ts, result.Ts)
}

func TestProtocolConverterInvalidInput(t *testing.T) {
	pc := NewProtocolConverter()

	// Invalid JSON
	_, err := pc.ExternalToInternal([]byte("not json"))
	assert.NotNil(t, err)

	// Missing fields
	_, err = pc.ExternalToInternal([]byte(`{"from":0,"to":0}`))
	assert.NotNil(t, err)

	// Invalid internal bytes
	_, err = pc.InternalToExternal([]byte{0x01})
	assert.NotNil(t, err)
}
