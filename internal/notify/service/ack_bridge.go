package service

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/IBM/sarama"
	log "github.com/Terry-Mao/goim/pkg/log"
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
		MsgID     string `json:"msg_id"`
		UserID    string `json:"user_id"`
		DeviceID  string `json:"device_id"`
		SessionID string `json:"session_id"`
		AckTime   int64  `json:"ack_time"`
		Status    string `json:"status"`
		TraceID   string `json:"trace_id"`
	}
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		log.Warningf("ack bridge: decode event failed offset=%d: %v", msg.Offset, err)
		return
	}
	if event.MsgID == "" {
		return
	}

	// TODO: When notification_id is available in the AckEvent, use it directly.
	// For now, we map via msg_id lookup in notification_acks table.
	// If the notification cannot be found, this is likely an IM chat message
	// (not a business notification), which is safe to skip.
	notifyID, err := h.svc.FindNotifyIDByMsgID(event.MsgID)
	if err != nil {
		log.Warningf("ack bridge: msg_id lookup failed msg_id=%s: %v", event.MsgID, err)
		return
	}
	if notifyID == "" {
		// IM-only message, no notification to update — this is expected
		return
	}

	// RecordAckIdempotent handles deduplication — repeated ACK events are safe
	recorded, err := h.svc.RecordAckIdempotent(AckInput{
		NotifyID:  notifyID,
		MsgID:     event.MsgID,
		DeviceID:  event.DeviceID,
		SessionID: event.SessionID,
		TraceID:   event.TraceID,
	}, "kafka-ack-"+event.MsgID)
	if err != nil {
		log.Warningf("ack bridge: record ack failed notify_id=%s msg_id=%s: %v", notifyID, event.MsgID, err)
		return
	}
	if recorded {
		log.V(1).Infof("ack bridge: synced notify_id=%s msg_id=%s device=%s", notifyID, event.MsgID, event.DeviceID)
	}
}
