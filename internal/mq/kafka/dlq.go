package kafka

import (
	"context"
	"fmt"

	"github.com/IBM/sarama"
	"github.com/Terry-Mao/goim/internal/mq"
)

// DLQ is a dead-letter queue backed by a Kafka topic.
// Messages that exceed max retries are routed here for manual inspection.
type DLQ struct {
	pub   sarama.SyncProducer
	topic string
}

// NewDLQ creates a dead-letter queue producer.
func NewDLQ(brokers []string, topic string) (*DLQ, error) {
	kc := sarama.NewConfig()
	kc.Producer.RequiredAcks = sarama.WaitForAll
	kc.Producer.Retry.Max = 3
	kc.Producer.Return.Successes = true
	pub, err := sarama.NewSyncProducer(brokers, kc)
	if err != nil {
		return nil, err
	}
	return &DLQ{pub: pub, topic: topic}, nil
}

// Send moves a message to the dead-letter queue.
func (d *DLQ) Send(ctx context.Context, msg *mq.Message, reason string) error {
	km := &sarama.ProducerMessage{
		Topic: d.topic,
		Value: sarama.ByteEncoder(msg.Value),
	}
	if msg.Key != "" {
		km.Key = sarama.StringEncoder(msg.Key)
	}
	if reason != "" {
		km.Headers = []sarama.RecordHeader{
			{Key: []byte("dlq-reason"), Value: []byte(reason)},
		}
	}
	_, _, err := d.pub.SendMessage(km)
	return err
}

// Close closes the DLQ producer.
func (d *DLQ) Close() error {
	return d.pub.Close()
}

// ensure imports used
var _ = fmt.Sprintf
