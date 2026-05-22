package dao

import (
	"context"
	"strconv"
	"time"

	"github.com/IBM/sarama"
	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/mq"
	mqkafka "github.com/Terry-Mao/goim/internal/mq/kafka"
	"github.com/Terry-Mao/goim/internal/tracectx"
	log "github.com/Terry-Mao/goim/pkg/log"
	"google.golang.org/protobuf/proto"
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

// PublishACK publishes an ACK event to the ACK topic for async consumers (e.g., Notify Server ACK sync).
// The event includes device-level info for bridging IM and business ACK data sources.
func (d *Dao) PublishACK(c context.Context, msgID string, uid int64, status, targetNode, deviceID, sessionID string) error {
	topic := d.c.Kafka.ACKTopic
	if topic == "" {
		return nil // ACK topic not configured, skip
	}
	traceID := tracectx.TraceID(c)
	event := mq.AckEvent{
		MsgID:      msgID,
		UserID:     strconv.FormatInt(uid, 10),
		UID:        uid,
		DeviceID:   deviceID,
		SessionID:  sessionID,
		AckTime:    time.Now().UnixMilli(),
		Status:     status,
		TargetNode: targetNode,
		TraceID:    traceID,
	}
	var err error
	if d.ackBatcher != nil {
		err = d.ackBatcher.Enqueue(c, event)
	} else {
		err = mqkafka.SendACKEvent(c, d.kafkaPub, topic, event)
	}
	if err != nil {
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
func (d *Dao) PublishACKViaMQ(ctx context.Context, msgID string, uid int64, status, targetNode, deviceID, sessionID string) error {
	if d.mqProducer == nil {
		return d.PublishACK(ctx, msgID, uid, status, targetNode, deviceID, sessionID)
	}
	return d.mqProducer.EnqueueACK(ctx, msgID, uid, status, targetNode)
}
