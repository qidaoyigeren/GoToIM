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

// Producer 通过 Kafka SyncProducer 实现 mq.Producer 接口。
// 生产与旧版 dao/kafka.go 相同 pb.PushMsg 线格式的消息，
// 确保现有的 Job 消费者无需修改即可读取消息。
type Producer struct {
	pub       sarama.SyncProducer
	pushTopic string
	roomTopic string
	allTopic  string
	ackTopic  string
	fallback  string
}

// NewProducer 创建一个基于 Kafka 的 mq.Producer。
// pushTopic/roomTopic/allTopic/ackTopic 是拆分后的各 Topic 名称；
// fallback 是旧版单一 Topic，当某个拆分 Topic 为空时使用。
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

// EnqueueToUser 将单用户消息发送到推送 Topic。
// 使用 uid 作为 Kafka 分区键，保证同一用户的消息在分区内有序。
func (p *Producer) EnqueueToUser(ctx context.Context, uid int64, msg *mq.Message) error {
	topic := p.topicFor(p.pushTopic, p.pushTopic)
	key := msg.Key
	if key == "" {
		key = strconv.FormatInt(uid, 10)
	}
	km := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(msg.Value),
	}
	_, _, err := p.pub.SendMessage(km)
	return err
}

// EnqueueToUsers 将同一消息发送给多个用户。
// 每条消息以对应用户的 uid 作为分区键发送，保证分区内有序。
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

// EnqueueToRoom 将房间广播消息发送到房间 Topic。
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

// EnqueueBroadcast 将全服广播消息发送到全局 Topic。
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

// EnqueueACK 将消息送达确认事件发布到 ACK Topic。
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

// EnqueueDelayed 发送一条延迟投递的消息。
// 通过 Kafka Header（goim_delayed_until）标记目标投递时间，
// 消费者应检查该 Header，在到达时间之前跳过处理。
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

// Close 关闭底层的 Kafka 生产者。
func (p *Producer) Close() error {
	return p.pub.Close()
}
