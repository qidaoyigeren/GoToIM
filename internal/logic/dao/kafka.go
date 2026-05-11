package dao

import (
	"context"
	"fmt"
	"strconv"

	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/mq"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/golang/protobuf/proto"
	sarama "gopkg.in/Shopify/sarama.v1"
)

// topicFor returns the appropriate topic for the given message type.
// Falls back to the legacy single topic if split topics are not configured.
func (d *Dao) topicFor(msgType pb.PushMsg_Type) string {
	switch msgType {
	case pb.PushMsg_PUSH:
		if d.c.Kafka.PushTopic != "" {
			return d.c.Kafka.PushTopic
		}
	case pb.PushMsg_ROOM:
		if d.c.Kafka.RoomTopic != "" {
			return d.c.Kafka.RoomTopic
		}
	case pb.PushMsg_BROADCAST:
		if d.c.Kafka.AllTopic != "" {
			return d.c.Kafka.AllTopic
		}
	}
	return d.c.Kafka.Topic
}

// PushMsg push a message to databus.
// Deprecated: use PushViaMQ which routes through the MQ abstraction.
func (d *Dao) PushMsg(c context.Context, op int32, server string, keys []string, msg []byte) (err error) {
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_PUSH,
		Operation: op,
		Server:    server,
		Keys:      keys,
		Msg:       msg,
	}
	b, err := proto.Marshal(pushMsg)
	if err != nil {
		return
	}
	m := &sarama.ProducerMessage{
		Key:   sarama.StringEncoder(keys[0]),
		Topic: d.topicFor(pb.PushMsg_PUSH),
		Value: sarama.ByteEncoder(b),
	}
	if _, _, err = d.kafkaPub.SendMessage(m); err != nil {
		log.Errorf("PushMsg.send(push pushMsg:%v) error(%v)", pushMsg, err)
	}
	return
}

// BroadcastRoomMsg push a message to databus.
// Deprecated: use BroadcastRoomViaMQ which routes through the MQ abstraction.
func (d *Dao) BroadcastRoomMsg(c context.Context, op int32, room string, msg []byte) (err error) {
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_ROOM,
		Operation: op,
		Room:      room,
		Msg:       msg,
	}
	b, err := proto.Marshal(pushMsg)
	if err != nil {
		return
	}
	m := &sarama.ProducerMessage{
		Key:   sarama.StringEncoder(room),
		Topic: d.topicFor(pb.PushMsg_ROOM),
		Value: sarama.ByteEncoder(b),
	}
	if _, _, err = d.kafkaPub.SendMessage(m); err != nil {
		log.Errorf("PushMsg.send(broadcast_room pushMsg:%v) error(%v)", pushMsg, err)
	}
	return
}

// BroadcastMsg push a message to databus.
func (d *Dao) BroadcastMsg(c context.Context, op, speed int32, msg []byte) (err error) {
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_BROADCAST,
		Operation: op,
		Speed:     speed,
		Msg:       msg,
	}
	b, err := proto.Marshal(pushMsg)
	if err != nil {
		return
	}
	m := &sarama.ProducerMessage{
		Key:   sarama.StringEncoder(strconv.FormatInt(int64(op), 10)),
		Topic: d.topicFor(pb.PushMsg_BROADCAST),
		Value: sarama.ByteEncoder(b),
	}
	if _, _, err = d.kafkaPub.SendMessage(m); err != nil {
		log.Errorf("PushMsg.send(broadcast pushMsg:%v) error(%v)", pushMsg, err)
	}
	return
}

// PublishACK publishes an ACK event to the ACK topic for async consumers (e.g., analytics, sender notification).
func (d *Dao) PublishACK(c context.Context, msgID string, uid int64, status string) error {
	topic := d.c.Kafka.ACKTopic
	if topic == "" {
		return nil // ACK topic not configured, skip
	}
	ackMsg := &pb.PushMsg{
		Type:      pb.PushMsg_PUSH,
		Operation: 19, // OpPushMsgAck
		Keys:      []string{fmt.Sprintf("uid:%d", uid)},
		Msg:       []byte(fmt.Sprintf(`{"msg_id":"%s","uid":%d,"status":"%s"}`, msgID, uid, status)),
	}
	b, err := proto.Marshal(ackMsg)
	if err != nil {
		return err
	}
	m := &sarama.ProducerMessage{
		Key:   sarama.StringEncoder(msgID),
		Topic: topic,
		Value: sarama.ByteEncoder(b),
	}
	if _, _, err = d.kafkaPub.SendMessage(m); err != nil {
		log.Errorf("PublishACK.send(msg_id:%s) error(%v)", msgID, err)
	}
	return err
}

// PushViaMQ routes a user push through the MQ abstraction when available,
// falling back to the legacy Kafka path when no MQ is configured.
func (d *Dao) PushViaMQ(ctx context.Context, op int32, server string, keys []string, msg []byte) error {
	if d.mqProducer == nil {
		return d.PushMsg(ctx, op, server, keys, msg)
	}
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_PUSH,
		Operation: op,
		Server:    server,
		Keys:      keys,
		Msg:       msg,
	}
	b, err := proto.Marshal(pushMsg)
	if err != nil {
		return err
	}
	return d.mqProducer.EnqueueToUser(ctx, 0, &mq.Message{Key: keys[0], Value: b})
}

// BroadcastRoomViaMQ routes a room broadcast through the MQ abstraction.
func (d *Dao) BroadcastRoomViaMQ(ctx context.Context, op int32, room string, msg []byte) error {
	if d.mqProducer == nil {
		return d.BroadcastRoomMsg(ctx, op, room, msg)
	}
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_ROOM,
		Operation: op,
		Room:      room,
		Msg:       msg,
	}
	b, err := proto.Marshal(pushMsg)
	if err != nil {
		return err
	}
	return d.mqProducer.EnqueueToRoom(ctx, room, &mq.Message{Key: room, Value: b})
}

// BroadcastViaMQ routes a global broadcast through the MQ abstraction.
func (d *Dao) BroadcastViaMQ(ctx context.Context, op, speed int32, msg []byte) error {
	if d.mqProducer == nil {
		return d.BroadcastMsg(ctx, op, speed, msg)
	}
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_BROADCAST,
		Operation: op,
		Speed:     speed,
		Msg:       msg,
	}
	b, err := proto.Marshal(pushMsg)
	if err != nil {
		return err
	}
	return d.mqProducer.EnqueueBroadcast(ctx, &mq.Message{Value: b}, speed)
}

// PublishACKViaMQ publishes an ACK event through the MQ abstraction.
func (d *Dao) PublishACKViaMQ(ctx context.Context, msgID string, uid int64, status string) error {
	if d.mqProducer == nil {
		return d.PublishACK(ctx, msgID, uid, status)
	}
	return d.mqProducer.EnqueueACK(ctx, msgID, uid, status)
}
