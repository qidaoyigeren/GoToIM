package kafka

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/IBM/sarama"
	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/Terry-Mao/goim/internal/tracectx"
	log "github.com/Terry-Mao/goim/pkg/log"
	"google.golang.org/protobuf/proto"
)

const (
	defaultACKBatchWindow = 50 * time.Millisecond
	defaultACKBatchMax    = 100
	defaultACKQueueSize   = 4096
)

// ACKBatcher merges short bursts of ACK events for the same user and ACK
// metadata into one Kafka event carrying a msg_ids list.
type ACKBatcher struct {
	topic string
	pub   sarama.SyncProducer

	in       chan mq.AckEvent
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

type ackBatchKey struct {
	userID     string
	uid        int64
	status     string
	targetNode string
	deviceID   string
	sessionID  string
	traceID    string
	notifyID   string
}

type ackBatch struct {
	key          ackBatchKey
	msgIDs       []string
	firstAckTime int64
	lastAckTime  int64
}

// NewACKBatcher starts an ACK batcher for a Kafka topic. Nil is returned when
// batching cannot be enabled.
func NewACKBatcher(topic string, pub sarama.SyncProducer) *ACKBatcher {
	if topic == "" || pub == nil {
		return nil
	}
	b := &ACKBatcher{
		topic: topic,
		pub:   pub,
		in:    make(chan mq.AckEvent, defaultACKQueueSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go b.run()
	return b
}

// Enqueue adds an ACK event to the short-window batch. If the in-memory queue is
// full, it falls back to immediate single-event delivery instead of dropping.
func (b *ACKBatcher) Enqueue(ctx context.Context, event mq.AckEvent) error {
	if b == nil {
		return nil
	}
	normalizeACKEvent(&event)
	if event.MsgID == "" && len(event.MsgIDs) == 0 {
		return nil
	}

	select {
	case <-b.stop:
		return SendACKEvent(ctx, b.pub, b.topic, event)
	default:
	}

	select {
	case b.in <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.stop:
		return SendACKEvent(ctx, b.pub, b.topic, event)
	default:
		return SendACKEvent(ctx, b.pub, b.topic, event)
	}
}

// Close flushes all queued ACK events before returning.
func (b *ACKBatcher) Close() error {
	if b == nil {
		return nil
	}
	b.stopOnce.Do(func() {
		close(b.stop)
	})
	<-b.done
	return nil
}

func (b *ACKBatcher) run() {
	defer close(b.done)
	ticker := time.NewTicker(defaultACKBatchWindow)
	defer ticker.Stop()

	pending := make(map[ackBatchKey]*ackBatch)
	for {
		select {
		case event := <-b.in:
			b.add(pending, event)
		case <-ticker.C:
			b.flushAll(pending)
		case <-b.stop:
			for {
				select {
				case event := <-b.in:
					b.add(pending, event)
				default:
					b.flushAll(pending)
					return
				}
			}
		}
	}
}

func (b *ACKBatcher) add(pending map[ackBatchKey]*ackBatch, event mq.AckEvent) {
	normalizeACKEvent(&event)
	key := ackKey(event)
	batch := pending[key]
	if batch == nil {
		batch = &ackBatch{key: key, firstAckTime: event.AckTime}
		pending[key] = batch
	}
	batch.msgIDs = append(batch.msgIDs, ackMsgIDs(event)...)
	if batch.firstAckTime == 0 || event.AckTime < batch.firstAckTime {
		batch.firstAckTime = event.AckTime
	}
	if event.AckTime > batch.lastAckTime {
		batch.lastAckTime = event.AckTime
	}
	if len(batch.msgIDs) >= defaultACKBatchMax {
		b.flushOne(key, batch)
		delete(pending, key)
	}
}

func (b *ACKBatcher) flushAll(pending map[ackBatchKey]*ackBatch) {
	for key, batch := range pending {
		b.flushOne(key, batch)
		delete(pending, key)
	}
}

func (b *ACKBatcher) flushOne(_ ackBatchKey, batch *ackBatch) {
	if batch == nil || len(batch.msgIDs) == 0 {
		return
	}
	if err := sendACKBatch(context.Background(), b.pub, b.topic, batch); err != nil {
		// ACK events are observability/bridge events; callers have already moved
		// the authoritative message state.
		log.Warningf("ack batch send failed: user_id=%s count=%d err=%v", batch.key.userID, len(batch.msgIDs), err)
	}
}

// SendACKEvent writes a single ACK event immediately. It is used as a fallback
// when batching is unavailable or the in-memory queue is full.
func SendACKEvent(ctx context.Context, pub sarama.SyncProducer, topic string, event mq.AckEvent) error {
	normalizeACKEvent(&event)
	batch := &ackBatch{
		key:          ackKey(event),
		msgIDs:       ackMsgIDs(event),
		firstAckTime: event.AckTime,
		lastAckTime:  event.AckTime,
	}
	return sendACKBatch(ctx, pub, topic, batch)
}

func sendACKBatch(ctx context.Context, pub sarama.SyncProducer, topic string, batch *ackBatch) error {
	if topic == "" || pub == nil || batch == nil || len(batch.msgIDs) == 0 {
		return nil
	}
	value, err := buildACKBatchValue(batch)
	if err != nil {
		return err
	}
	key := batch.key.userID
	if key == "" {
		key = batch.msgIDs[0]
	}
	km := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(value),
	}
	traceID := batch.key.traceID
	if traceID == "" {
		traceID = tracectx.TraceID(ctx)
	}
	if traceID != "" {
		km.Headers = []sarama.RecordHeader{{Key: []byte(mq.HeaderTraceID), Value: []byte(traceID)}}
	}
	_, _, err = pub.SendMessage(km)
	return err
}

func buildACKBatchValue(batch *ackBatch) ([]byte, error) {
	if batch == nil || len(batch.msgIDs) == 0 {
		return nil, nil
	}
	userID := batch.key.userID
	if userID == "" && batch.key.uid != 0 {
		userID = strconv.FormatInt(batch.key.uid, 10)
	}
	event := mq.AckEvent{
		MsgID:        batch.msgIDs[0],
		MsgIDs:       uniqueMsgIDs(batch.msgIDs),
		UserID:       userID,
		UID:          batch.key.uid,
		DeviceID:     batch.key.deviceID,
		SessionID:    batch.key.sessionID,
		AckTime:      batch.lastAckTime,
		FirstAckTime: batch.firstAckTime,
		Status:       batch.key.status,
		TargetNode:   batch.key.targetNode,
		TraceID:      batch.key.traceID,
		NotifyID:     batch.key.notifyID,
		Count:        len(uniqueMsgIDs(batch.msgIDs)),
		Batched:      len(batch.msgIDs) > 1,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_PUSH,
		Operation: 19,
		Keys:      []string{"uid:" + userID},
		Msg:       payload,
	}
	return proto.Marshal(pushMsg)
}

func normalizeACKEvent(event *mq.AckEvent) {
	if event == nil {
		return
	}
	if event.UID == 0 && event.UserID != "" {
		event.UID, _ = strconv.ParseInt(event.UserID, 10, 64)
	}
	if event.UserID == "" && event.UID != 0 {
		event.UserID = strconv.FormatInt(event.UID, 10)
	}
	if event.AckTime == 0 {
		event.AckTime = time.Now().UnixMilli()
	}
	if event.MsgID == "" && len(event.MsgIDs) > 0 {
		event.MsgID = event.MsgIDs[0]
	}
	if len(event.MsgIDs) == 0 && event.MsgID != "" {
		event.MsgIDs = []string{event.MsgID}
	}
}

func ackKey(event mq.AckEvent) ackBatchKey {
	return ackBatchKey{
		userID:     event.UserID,
		uid:        event.UID,
		status:     event.Status,
		targetNode: event.TargetNode,
		deviceID:   event.DeviceID,
		sessionID:  event.SessionID,
		traceID:    event.TraceID,
		notifyID:   event.NotifyID,
	}
}

func ackMsgIDs(event mq.AckEvent) []string {
	ids := append([]string(nil), event.MsgIDs...)
	if event.MsgID != "" {
		ids = append(ids, event.MsgID)
	}
	return uniqueMsgIDs(ids)
}

func uniqueMsgIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
