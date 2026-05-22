package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/IBM/sarama"
	pb "github.com/Terry-Mao/goim/api/logic"
	log "github.com/Terry-Mao/goim/pkg/log"
	"google.golang.org/protobuf/proto"
)

// AckBridgeConsumer subscribes to the Kafka ACK topic and synchronizes
// IM-layer ACKs into the Notify Server's notification ACK state.
//
// This bridges the gap between two ACK data sources:
//   - IM Router ACK (Redis msg:{msg_id})
//   - Notify Server ACK (MySQL notification_acks)
//
// The consumer is best-effort: Kafka unavailability does not block IM ACKs,
// and duplicate events are handled by RecordAckIdempotent's existing dedup.
type AckBridgeConsumer struct {
	orderSvc *OrderNotifyService
	client   sarama.ConsumerGroup
	topic    string
	ready    chan struct{}
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// AckBridgeConfig holds configuration for the ACK bridge consumer.
type AckBridgeConfig struct {
	Brokers []string
	Topic   string
	GroupID string
}

// NewAckBridgeConsumer creates a new ACK bridge consumer.
// Returns nil if config is empty (ACK bridging not configured).
func NewAckBridgeConsumer(svc *OrderNotifyService, cfg AckBridgeConfig) (*AckBridgeConsumer, error) {
	if cfg.Topic == "" || len(cfg.Brokers) == 0 {
		return nil, nil
	}
	saramaCfg := sarama.NewConfig()
	saramaCfg.Consumer.Group.Rebalance.Strategy = sarama.NewBalanceStrategyRoundRobin()
	saramaCfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	client, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.GroupID, saramaCfg)
	if err != nil {
		return nil, err
	}
	return &AckBridgeConsumer{
		orderSvc: svc,
		client:   client,
		topic:    cfg.Topic,
		ready:    make(chan struct{}),
		stopCh:   make(chan struct{}),
	}, nil
}

// Start begins consuming ACK events from Kafka in a background goroutine.
func (c *AckBridgeConsumer) Start() {
	if c == nil {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		handler := &ackBridgeHandler{svc: c.orderSvc, ready: c.ready}
		for {
			if err := c.client.Consume(context.Background(), []string{c.topic}, handler); err != nil {
				log.Errorf("ack bridge consume error: %v", err)
			}
			select {
			case <-c.stopCh:
				return
			default:
			}
			c.ready = make(chan struct{})
		}
	}()
	<-c.ready
	log.Infof("ack bridge consumer started on topic=%s", c.topic)
}

// Stop gracefully shuts down the consumer.
func (c *AckBridgeConsumer) Stop() {
	if c == nil {
		return
	}
	close(c.stopCh)
	c.wg.Wait()
	if err := c.client.Close(); err != nil {
		log.Warningf("ack bridge consumer close: %v", err)
	}
}

// ackBridgeHandler implements sarama.ConsumerGroupHandler.
type ackBridgeHandler struct {
	svc   *OrderNotifyService
	ready chan struct{}
}

func (h *ackBridgeHandler) Setup(sarama.ConsumerGroupSession) error {
	close(h.ready)
	return nil
}

func (h *ackBridgeHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *ackBridgeHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		if msg == nil {
			continue
		}
		h.processMessage(msg)
		sess.MarkMessage(msg, "")
	}
	return nil
}

// processMessage deserializes an AckEvent from Kafka and synchronizes
// the ACK into the Notify Server's notification table.
func (h *ackBridgeHandler) processMessage(msg *sarama.ConsumerMessage) {
	var event struct {
		MsgID      string   `json:"msg_id"`
		MsgIDs     []string `json:"msg_ids"`
		UserID     string   `json:"user_id"`
		DeviceID   string   `json:"device_id"`
		SessionID  string   `json:"session_id"`
		AckTime    int64    `json:"ack_time"`
		Status     string   `json:"status"`
		TargetNode string   `json:"target_node"`
		LatencyMs  float64  `json:"latency_ms"`
		TraceID    string   `json:"trace_id"`
		NotifyID   string   `json:"notify_id"`
	}
	if err := decodeAckBridgeEvent(msg.Value, &event); err != nil {
		log.Warningf("ack bridge: decode event failed offset=%d: %v", msg.Offset, err)
		return
	}
	msgIDs := normalizeAckBridgeMsgIDs(event.MsgID, event.MsgIDs)
	if len(msgIDs) == 0 {
		return
	}
	for _, msgID := range msgIDs {
		h.processAckEvent(msgID, event.NotifyID, event.DeviceID, event.SessionID, event.Status, event.TraceID)
	}
}

func (h *ackBridgeHandler) processAckEvent(msgID, eventNotifyID, deviceID, sessionID, status, traceID string) {
	// TODO: When notification_id is available in the AckEvent, use it directly.
	// For now, we map via msg_id lookup in notification_acks table.
	// If the notification cannot be found, this is likely an IM chat message
	// (not a business notification), which is safe to skip.
	notifyID := eventNotifyID
	if notifyID == "" {
		var err error
		notifyID, err = h.svc.FindNotifyIDByMsgID(msgID)
		if err != nil {
			log.Warningf("ack bridge: msg_id lookup failed msg_id=%s: %v", msgID, err)
			return
		}
	}
	if notifyID == "" {
		// IM-only message, no notification to update — this is expected
		return
	}

	switch status {
	case "server_received":
		_ = h.svc.store.UpdateNotificationStatus(notifyID, "delivering", time.Now())
		return
	case "pushed":
		_ = h.svc.store.UpdateNotificationStatus(notifyID, "delivered", time.Now())
		return
	case "", "acked":
	default:
		log.V(1).Infof("ack bridge: ignored status=%s msg_id=%s", status, msgID)
		return
	}

	// RecordAckIdempotent handles deduplication — repeated ACK events are safe
	recorded, err := h.svc.RecordAckIdempotent(AckInput{
		NotifyID:  notifyID,
		MsgID:     msgID,
		DeviceID:  deviceID,
		SessionID: sessionID,
		TraceID:   traceID,
	}, "kafka-ack-"+msgID)
	if err != nil {
		log.Warningf("ack bridge: record ack failed notify_id=%s msg_id=%s: %v", notifyID, msgID, err)
		return
	}
	if recorded {
		log.V(1).Infof("ack bridge: synced notify_id=%s msg_id=%s device=%s", notifyID, msgID, deviceID)
	}
}

func normalizeAckBridgeMsgIDs(msgID string, msgIDs []string) []string {
	seen := make(map[string]struct{}, len(msgIDs)+1)
	out := make([]string, 0, len(msgIDs)+1)
	for _, id := range append(msgIDs, msgID) {
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

func decodeAckBridgeEvent(data []byte, dst any) error {
	if err := json.Unmarshal(data, dst); err == nil {
		return nil
	}
	pushMsg := new(pb.PushMsg)
	if err := proto.Unmarshal(data, pushMsg); err != nil {
		return err
	}
	return json.Unmarshal(pushMsg.Msg, dst)
}
