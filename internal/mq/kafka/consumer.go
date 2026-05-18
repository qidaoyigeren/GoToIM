package kafka

import (
	"context"
	"strconv"
	"time"

	"github.com/IBM/sarama"
	"github.com/Terry-Mao/goim/internal/mq"

	log "github.com/Terry-Mao/goim/pkg/log"
)

// Consumer implements mq.Consumer via sarama.ConsumerGroup.
type Consumer struct {
	cg     sarama.ConsumerGroup
	topics []string
	dlq    mq.DLQProducer // optional: routes undeliverable messages to dead-letter
}

// NewConsumer creates a Kafka consumer group.
func NewConsumer(brokers []string, group string, topics []string) (*Consumer, error) {
	config := sarama.NewConfig()
	config.Consumer.Return.Errors = true
	config.Consumer.Offsets.AutoCommit.Enable = false // manual offset commit for at-least-once
	cg, err := sarama.NewConsumerGroup(brokers, group, config)
	if err != nil {
		return nil, err
	}
	return &Consumer{cg: cg, topics: topics}, nil
}

// SetDLQ configures an optional dead-letter queue. When set, messages whose
// handler returns a non-nil error are sent to the DLQ instead of being
// redelivered indefinitely.
func (c *Consumer) SetDLQ(dlq mq.DLQProducer) {
	c.dlq = dlq
}

// Consume starts the consume loop. Blocks until ctx is cancelled.
func (c *Consumer) Consume(ctx context.Context, handler mq.MessageHandler) error {
	h := &consumerGroupHandler{handler: handler, dlq: c.dlq}

	// Background goroutine to log consumer errors
	go func() {
		for err := range c.cg.Errors() {
			log.Errorf("consumer group error: %v", err)
		}
	}()

	for {
		if err := c.cg.Consume(ctx, c.topics, h); err != nil {
			log.Errorf("consumer group consume error: %v", err)
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

// Close shuts down the consumer group.
func (c *Consumer) Close() error {
	return c.cg.Close()
}

// consumerGroupHandler implements sarama.ConsumerGroupHandler.
type consumerGroupHandler struct {
	handler mq.MessageHandler
	dlq     mq.DLQProducer // optional
}

func (h *consumerGroupHandler) Setup(sarama.ConsumerGroupSession) error {
	log.Infof("consumer group session setup")
	return nil
}

func (h *consumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error {
	log.Infof("consumer group session cleanup")
	return nil
}

func (h *consumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for raw := range claim.Messages() {
		msg := &mq.Message{
			Topic:     raw.Topic,
			Key:       string(raw.Key),
			Value:     raw.Value,
			Partition: raw.Partition,
			Offset:    raw.Offset,
			Timestamp: raw.Timestamp.UnixMilli(),
		}
		// Extract headers
		if len(raw.Headers) > 0 {
			msg.Headers = make(map[string]string, len(raw.Headers))
			for _, h := range raw.Headers {
				msg.Headers[string(h.Key)] = string(h.Value)
			}
		}
		// Check delayed delivery
		if delayedUntil, ok := msg.Headers[mq.HeaderDelayedUntil]; ok {
			if deliverAt, err := strconv.ParseInt(delayedUntil, 10, 64); err == nil {
				if time.Now().UnixMilli() < deliverAt {
					time.Sleep(100 * time.Millisecond)
					continue
				}
			}
		}

		err := h.handler(session.Context(), msg)
		if err == nil {
			session.MarkMessage(raw, "")
			continue
		}

		log.Errorf("handler error for %s/%d: %v", raw.Topic, raw.Offset, err)

		// If the error wraps mq.ErrDeadLetter, route to DLQ and commit offset
		// so the message is not redelivered.
		if h.dlq != nil && isDeadLetter(err) {
			reason := err.Error()
			if dlqErr := h.dlq.Send(session.Context(), msg, reason); dlqErr != nil {
				log.Errorf("dlq send error for %s/%d: %v", raw.Topic, raw.Offset, dlqErr)
				// DLQ send failed — don't commit, let Kafka redeliver
				continue
			}
			log.Warningf("message sent to DLQ: topic=%s offset=%d reason=%s", raw.Topic, raw.Offset, reason)
			session.MarkMessage(raw, "")
			continue
		}

		// Non-DLQ error: skip offset commit → Kafka redelivers
	}
	return nil
}

// isDeadLetter checks whether the error signals that the message should be
// routed to the dead-letter queue.
func isDeadLetter(err error) bool {
	type deadLetter interface {
		DeadLetter() bool
	}
	if d, ok := err.(deadLetter); ok {
		return d.DeadLetter()
	}
	return false
}
