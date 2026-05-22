package kafka

import (
	"encoding/json"
	"testing"

	"github.com/IBM/sarama"
	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
)

func TestTopicFor(t *testing.T) {
	p := &Producer{
		pushTopic: "push-topic",
		roomTopic: "room-topic",
		allTopic:  "all-topic",
		ackTopic:  "ack-topic",
		fallback:  "fallback-topic",
	}

	// When specTopic is provided, use it
	assert.Equal(t, "custom-topic", p.topicFor("push-topic", "custom-topic"))

	// When specTopic is empty, use pushTopic
	assert.Equal(t, "push-topic", p.topicFor("push-topic", ""))

	// When both empty, use fallback
	assert.Equal(t, "fallback-topic", p.topicFor("", ""))
}

func TestHeaderDelayedUntilConstant(t *testing.T) {
	// Verify the constant matches what consumer expects
	assert.Equal(t, "goim_delayed_until", mq.HeaderDelayedUntil)
}

func TestSaramaRecordHeaderForDelayedMessage(t *testing.T) {
	// Verify that sarama.RecordHeader can hold our delayed message headers
	headers := []sarama.RecordHeader{
		{Key: []byte(mq.HeaderDelayedUntil), Value: []byte("1700000000000")},
	}
	assert.Len(t, headers, 1)
	assert.Equal(t, mq.HeaderDelayedUntil, string(headers[0].Key))
	assert.Equal(t, "1700000000000", string(headers[0].Value))
}

func TestMessageStructWithHeaders(t *testing.T) {
	// Verify Message struct can carry headers for delayed delivery
	msg := &mq.Message{
		Key:   "5001",
		Value: []byte("test payload"),
		Headers: map[string]string{
			mq.HeaderDelayedUntil: "1700000000000",
		},
	}
	assert.Equal(t, "5001", msg.Key)
	assert.Equal(t, "1700000000000", msg.Headers[mq.HeaderDelayedUntil])
}

func TestBuildACKBatchValue(t *testing.T) {
	batch := &ackBatch{
		key: ackBatchKey{
			userID:     "1001",
			uid:        1001,
			status:     "acked",
			deviceID:   "web",
			sessionID:  "sid-1",
			targetNode: "comet-a",
			traceID:    "trace-1",
		},
		msgIDs:       []string{"m1", "m2", "m1"},
		firstAckTime: 100,
		lastAckTime:  150,
	}

	data, err := buildACKBatchValue(batch)
	assert.NoError(t, err)

	pushMsg := new(pb.PushMsg)
	assert.NoError(t, proto.Unmarshal(data, pushMsg))
	assert.Equal(t, int32(19), pushMsg.Operation)
	assert.Equal(t, []string{"uid:1001"}, pushMsg.Keys)

	var event mq.AckEvent
	assert.NoError(t, json.Unmarshal(pushMsg.Msg, &event))
	assert.Equal(t, "m1", event.MsgID)
	assert.Equal(t, []string{"m1", "m2"}, event.MsgIDs)
	assert.Equal(t, int64(1001), event.UID)
	assert.Equal(t, "1001", event.UserID)
	assert.Equal(t, "acked", event.Status)
	assert.True(t, event.Batched)
	assert.Equal(t, 2, event.Count)
	assert.Equal(t, int64(100), event.FirstAckTime)
	assert.Equal(t, int64(150), event.AckTime)
}
