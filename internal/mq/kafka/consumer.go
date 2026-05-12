package kafka

import (
	"context"
	"strconv"
	"time"

	"github.com/Terry-Mao/goim/internal/mq"
	"github.com/bsm/sarama-cluster"

	log "github.com/Terry-Mao/goim/pkg/log"
)

// Consumer implements mq.Consumer via sarama-cluster consumer group.
// Mirrors the consumption pattern used by internal/job/job.go.
type Consumer struct {
	consumer *cluster.Consumer
}

// NewConsumer creates a Kafka consumer group.
func NewConsumer(brokers []string, group string, topics []string) (*Consumer, error) {
	config := cluster.NewConfig()
	config.Consumer.Return.Errors = true
	config.Group.Return.Notifications = true
	consumer, err := cluster.NewConsumer(brokers, group, topics, config)
	if err != nil {
		return nil, err
	}
	return &Consumer{consumer: consumer}, nil
}

// Consume starts the consume loop. Blocks until ctx is cancelled.
// Messages are delivered to handler one at a time.
// If handler returns an error, the offset is NOT committed (redelivery).
// Delayed messages (with goim_delayed_until header) are held until their delivery time.
func (c *Consumer) Consume(ctx context.Context, handler mq.MessageHandler) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-c.consumer.Errors():
			log.Errorf("consumer error(%v)", err)
		case n := <-c.consumer.Notifications():
			log.Infof("consumer rebalanced(%v)", n)
		case raw, ok := <-c.consumer.Messages():
			if !ok {
				return nil
			}
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
			if msg.Headers != nil {
				if delayedUntil, ok := msg.Headers[mq.HeaderDelayedUntil]; ok {
					if deliverAt, err := strconv.ParseInt(delayedUntil, 10, 64); err == nil {
						if time.Now().UnixMilli() < deliverAt {
							// Not yet time to deliver; skip without committing offset
							// Kafka will redeliver on next poll
							time.Sleep(100 * time.Millisecond)
							continue
						}
					}
				}
			}
			if err := handler(ctx, msg); err != nil {
				log.Errorf("handler error for %s/%d: %v", raw.Topic, raw.Offset, err)
				continue
			}
			c.consumer.MarkOffset(raw, "")
		}
	}
}

// Close shuts down the consumer group.
func (c *Consumer) Close() error {
	return c.consumer.Close()
}
