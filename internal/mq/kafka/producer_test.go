package kafka

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/stretchr/testify/assert"
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
