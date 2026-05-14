package kafka

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/IBM/sarama"
	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/mq"
	"google.golang.org/protobuf/proto"
)

// Producer implements mq.Producer via Kafka SyncProducer.
// Produces the same pb.PushMsg wire format as the legacy dao/kafka.go,
// so existing Job consumers can read messages without changes.
type Producer struct {
	pub       sarama.SyncProducer
	pushTopic string
	roomTopic string
	allTopic  string
	ackTopic  string
	fallback  string
}

// NewProducer creates a Kafka-backed mq.Producer.
// pushTopic/roomTopic/allTopic/ackTopic are the split-topic names;
// fallback is the legacy single topic used when a split topic is empty.
func NewProducer(brokers []string, pushTopic, roomTopic, allTopic, ackTopic, fallback string) (*Producer, error) {
	kc := sarama.NewConfig()
	kc.Producer.RequiredAcks = sarama.WaitForAll
	kc.Producer.Retry.Max = 10
	kc.Producer.Return.Successes = true
	pub, err := sarama.NewSyncProducer(brokers, kc)
	if err != nil {
		return nil, err
	}
	return &Producer{
		pub:       pub,
		pushTopic: pushTopic,
		roomTopic: roomTopic,
		allTopic:  allTopic,
		ackTopic:  ackTopic,
		fallback:  fallback,
	}, nil
}

func (p *Producer) topicFor(pushTopic, specTopic string) string {
	if specTopic != "" {
		return specTopic
	}
	if pushTopic != "" {
		return pushTopic
	}
	return p.fallback
}

// EnqueueToUser enqueues a per-user message via the push topic.
// Uses uid as the Kafka partition key to guarantee per-user ordering within a partition.
func (p *Producer) EnqueueToUser(ctx context.Context, uid int64, msg *mq.Message) error {
	topic := p.topicFor(p.pushTopic, p.pushTopic)
	km := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(strconv.FormatInt(uid, 10)),
		Value: sarama.ByteEncoder(msg.Value),
	}
	_, _, err := p.pub.SendMessage(km)
	return err
}

// EnqueueToUsers enqueues a message to multiple users.
// Each user's message is sent with their uid as partition key for ordering.
func (p *Producer) EnqueueToUsers(ctx context.Context, uids []int64, msg *mq.Message) error {
	topic := p.topicFor(p.pushTopic, p.pushTopic)
	for _, uid := range uids {
		km := &sarama.ProducerMessage{
			Topic: topic,
			Key:   sarama.StringEncoder(strconv.FormatInt(uid, 10)),
			Value: sarama.ByteEncoder(msg.Value),
		}
		if _, _, err := p.pub.SendMessage(km); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueToRoom enqueues a room broadcast message.
func (p *Producer) EnqueueToRoom(ctx context.Context, roomID string, msg *mq.Message) error {
	topic := p.topicFor(p.roomTopic, p.roomTopic)
	km := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(roomID),
		Value: sarama.ByteEncoder(msg.Value),
	}
	_, _, err := p.pub.SendMessage(km)
	return err
}

// EnqueueBroadcast enqueues a global broadcast message.
func (p *Producer) EnqueueBroadcast(ctx context.Context, msg *mq.Message, speed int32) error {
	topic := p.topicFor(p.allTopic, p.allTopic)
	km := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(strconv.FormatInt(int64(speed), 10)),
		Value: sarama.ByteEncoder(msg.Value),
	}
	_, _, err := p.pub.SendMessage(km)
	return err
}

// EnqueueACK publishes an ACK event to the ACK topic.
func (p *Producer) EnqueueACK(ctx context.Context, msgID string, uid int64, status string) error {
	if p.ackTopic == "" {
		return nil
	}
	ackMsg := &pb.PushMsg{
		Type:      pb.PushMsg_PUSH,
		Operation: 19,
		Keys:      []string{fmt.Sprintf("uid:%d", uid)},
		Msg:       []byte(fmt.Sprintf(`{"msg_id":"%s","uid":%d,"status":"%s"}`, msgID, uid, status)),
	}
	b, err := proto.Marshal(ackMsg)
	if err != nil {
		return err
	}
	km := &sarama.ProducerMessage{
		Topic: p.ackTopic,
		Key:   sarama.StringEncoder(msgID),
		Value: sarama.ByteEncoder(b),
	}
	_, _, err = p.pub.SendMessage(km)
	return err
}

// EnqueueDelayed enqueues a message with a delivery delay.
// Uses a Kafka header (goim_delayed_until) to mark the target delivery time.
// Consumers should check this header and skip processing until the time arrives.
func (p *Producer) EnqueueDelayed(ctx context.Context, uid int64, msg *mq.Message, delayMs int64) error {
	topic := p.topicFor(p.pushTopic, p.pushTopic)
	deliverAt := time.Now().UnixMilli() + delayMs

	headers := make([]sarama.RecordHeader, 0, len(msg.Headers)+1)
	for k, v := range msg.Headers {
		headers = append(headers, sarama.RecordHeader{Key: []byte(k), Value: []byte(v)})
	}
	headers = append(headers, sarama.RecordHeader{
		Key:   []byte(mq.HeaderDelayedUntil),
		Value: []byte(strconv.FormatInt(deliverAt, 10)),
	})

	km := &sarama.ProducerMessage{
		Topic:   topic,
		Key:     sarama.StringEncoder(strconv.FormatInt(uid, 10)),
		Value:   sarama.ByteEncoder(msg.Value),
		Headers: headers,
	}
	_, _, err := p.pub.SendMessage(km)
	return err
}

// Close closes the underlying Kafka producer.
func (p *Producer) Close() error {
	return p.pub.Close()
}
