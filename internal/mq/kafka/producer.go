package kafka

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/IBM/sarama"
	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/Terry-Mao/goim/internal/tracectx"
	"google.golang.org/protobuf/proto"
)

// Producer 通过 Kafka SyncProducer 实现 mq.Producer 接口。
// Phase 2 升级：支持按 priority 分 topic、Kafka header 携带业务元信息。
type Producer struct {
	pub       sarama.SyncProducer
	pushTopic string
	roomTopic string
	allTopic  string
	ackTopic  string
	dlqTopic  string // Phase 2: dead-letter-queue topic
	fallback  string
	// Phase 2: priority-split topics (optional)
	enablePriorityTopics bool
	pushTopicHigh        string
	pushTopicNormal      string
	pushTopicLow         string
}

// NewProducer 创建一个基于 Kafka 的 mq.Producer。
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

// SetPriorityTopics enables priority-based topic routing (Phase 2).
func (p *Producer) SetPriorityTopics(high, normal, low string) {
	p.enablePriorityTopics = true
	p.pushTopicHigh = high
	p.pushTopicNormal = normal
	p.pushTopicLow = low
}

// SetDLQTopic sets the dead-letter-queue topic (Phase 2).
func (p *Producer) SetDLQTopic(topic string) {
	p.dlqTopic = topic
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

// pushTopicForPriority returns the appropriate push topic for a given priority.
func (p *Producer) pushTopicForPriority(priority string) string {
	if !p.enablePriorityTopics {
		return p.topicFor(p.pushTopic, p.pushTopic)
	}
	switch priority {
	case "critical", "high":
		if p.pushTopicHigh != "" {
			return p.pushTopicHigh
		}
	case "low":
		if p.pushTopicLow != "" {
			return p.pushTopicLow
		}
	}
	// normal, empty, or fallback
	if p.pushTopicNormal != "" {
		return p.pushTopicNormal
	}
	return p.topicFor(p.pushTopic, p.pushTopic)
}

// buildHeadersFromMsg converts mq.Message.Headers to sarama.RecordHeader slice.
func buildHeadersFromMsg(msg *mq.Message) []sarama.RecordHeader {
	if len(msg.Headers) == 0 {
		return nil
	}
	headers := make([]sarama.RecordHeader, 0, len(msg.Headers))
	for k, v := range msg.Headers {
		headers = append(headers, sarama.RecordHeader{Key: []byte(k), Value: []byte(v)})
	}
	return headers
}

// EnqueueToUser 将单用户消息发送到推送 Topic（Phase 2：按 priority 分 topic）。
func (p *Producer) EnqueueToUser(ctx context.Context, uid int64, msg *mq.Message) error {
	priority := msg.Headers[mq.HeaderPriority]
	topic := p.pushTopicForPriority(priority)
	key := msg.Key
	if key == "" {
		key = strconv.FormatInt(uid, 10)
	}
	km := &sarama.ProducerMessage{
		Topic:   topic,
		Key:     sarama.StringEncoder(key),
		Value:   sarama.ByteEncoder(msg.Value),
		Headers: buildHeadersFromMsg(msg),
	}
	_, _, err := p.pub.SendMessage(km)
	return err
}

// EnqueueToUsers 将同一消息发送给多个用户。
func (p *Producer) EnqueueToUsers(ctx context.Context, uids []int64, msg *mq.Message) error {
	priority := msg.Headers[mq.HeaderPriority]
	topic := p.pushTopicForPriority(priority)
	hdr := buildHeadersFromMsg(msg)
	for _, uid := range uids {
		km := &sarama.ProducerMessage{
			Topic:   topic,
			Key:     sarama.StringEncoder(strconv.FormatInt(uid, 10)),
			Value:   sarama.ByteEncoder(msg.Value),
			Headers: hdr,
		}
		if _, _, err := p.pub.SendMessage(km); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueToRoom 将房间广播消息发送到房间 Topic。
func (p *Producer) EnqueueToRoom(ctx context.Context, roomID string, msg *mq.Message) error {
	topic := p.topicFor(p.roomTopic, p.roomTopic)
	km := &sarama.ProducerMessage{
		Topic:   topic,
		Key:     sarama.StringEncoder(roomID),
		Value:   sarama.ByteEncoder(msg.Value),
		Headers: buildHeadersFromMsg(msg),
	}
	_, _, err := p.pub.SendMessage(km)
	return err
}

// EnqueueBroadcast 将全服广播消息发送到全局 Topic。
func (p *Producer) EnqueueBroadcast(ctx context.Context, msg *mq.Message, speed int32) error {
	topic := p.topicFor(p.allTopic, p.allTopic)
	km := &sarama.ProducerMessage{
		Topic:   topic,
		Key:     sarama.StringEncoder(strconv.FormatInt(int64(speed), 10)),
		Value:   sarama.ByteEncoder(msg.Value),
		Headers: buildHeadersFromMsg(msg),
	}
	_, _, err := p.pub.SendMessage(km)
	return err
}

// EnqueueACK 将消息送达确认事件发布到 ACK Topic。
func (p *Producer) EnqueueACK(ctx context.Context, msgID string, uid int64, status string) error {
	if p.ackTopic == "" {
		return nil
	}
	traceID := tracectx.TraceID(ctx)
	ackMsg := &pb.PushMsg{
		Type:      pb.PushMsg_PUSH,
		Operation: 19,
		Keys:      []string{fmt.Sprintf("uid:%d", uid)},
		Msg:       []byte(fmt.Sprintf(`{"msg_id":"%s","uid":%d,"status":"%s","trace_id":"%s"}`, msgID, uid, status, traceID)),
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
	if traceID != "" {
		km.Headers = []sarama.RecordHeader{{Key: []byte(mq.HeaderTraceID), Value: []byte(traceID)}}
	}
	_, _, err = p.pub.SendMessage(km)
	return err
}

// EnqueueDelayed 发送一条延迟投递的消息，同时携带 Phase 2 业务 header。
func (p *Producer) EnqueueDelayed(ctx context.Context, uid int64, msg *mq.Message, delayMs int64) error {
	priority := msg.Headers[mq.HeaderPriority]
	topic := p.pushTopicForPriority(priority)
	deliverAt := time.Now().UnixMilli() + delayMs

	headers := buildHeadersFromMsg(msg)
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

// EnqueueToDLQ sends a message to the dead-letter-queue topic (Phase 2).
func (p *Producer) EnqueueToDLQ(ctx context.Context, msg *mq.Message) error {
	topic := p.dlqTopic
	if topic == "" {
		topic = p.fallback
	}
	km := &sarama.ProducerMessage{
		Topic:   topic,
		Key:     sarama.StringEncoder(msg.Key),
		Value:   sarama.ByteEncoder(msg.Value),
		Headers: buildHeadersFromMsg(msg),
	}
	_, _, err := p.pub.SendMessage(km)
	return err
}

// Close 关闭底层的 Kafka 生产者。
func (p *Producer) Close() error {
	return p.pub.Close()
}
