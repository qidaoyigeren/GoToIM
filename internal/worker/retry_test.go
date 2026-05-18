package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/Terry-Mao/goim/internal/mq"
)

// mockRetryCounter implements mq.RetryCounter for testing.
type mockRetryCounter struct {
	counts  map[string]int64
	nextVal int64
}

func newMockRetryCounter() *mockRetryCounter {
	return &mockRetryCounter{counts: make(map[string]int64)}
}

func (m *mockRetryCounter) Incr(_ context.Context, topic string, partition int32, offset int64) (int64, error) {
	key := topic
	_ = partition
	_ = offset
	m.counts[key]++
	return m.counts[key], nil
}

// mockDLQ implements mq.DLQProducer for testing.
type mockDLQ struct {
	messages []dlqMessage
}

type dlqMessage struct {
	Topic  string
	Reason string
}

func (m *mockDLQ) Send(_ context.Context, msg *mq.Message, reason string) error {
	m.messages = append(m.messages, dlqMessage{Topic: msg.Topic, Reason: reason})
	return nil
}

func TestCheckRetry_NilError(t *testing.T) {
	w := &DeliveryWorker{}
	msg := &mq.Message{Topic: "test-topic", Partition: 0, Offset: 0}
	if err := w.checkRetry(context.Background(), msg, nil); err != nil {
		t.Errorf("checkRetry(nil error) = %v, want nil", err)
	}
}

func TestCheckRetry_NoRetryCounter(t *testing.T) {
	w := &DeliveryWorker{}
	msg := &mq.Message{Topic: "test-topic", Partition: 0, Offset: 0}
	pushErr := errors.New("push failed")
	if err := w.checkRetry(context.Background(), msg, pushErr); err != pushErr {
		t.Errorf("checkRetry without counter should return original error")
	}
}

func TestCheckRetry_UnderMaxRetries(t *testing.T) {
	counter := newMockRetryCounter()
	w := &DeliveryWorker{retryCounter: counter}
	msg := &mq.Message{Topic: "test-topic", Partition: 0, Offset: 1}

	pushErr := errors.New("push failed")
	err := w.checkRetry(context.Background(), msg, pushErr)

	// Should return original error (not DeadLetterError) because retry count < 3
	if err != pushErr {
		t.Errorf("expected original error, got %v", err)
	}
	if _, isDL := err.(*mq.DeadLetterError); isDL {
		t.Error("should NOT be DeadLetterError when under max retries")
	}
}

func TestCheckRetry_MaxRetriesReached(t *testing.T) {
	counter := newMockRetryCounter()
	counter.counts["test-topic"] = 2 // next Incr returns 3
	w := &DeliveryWorker{retryCounter: counter}
	msg := &mq.Message{Topic: "test-topic", Partition: 0, Offset: 42}

	pushErr := errors.New("push failed")
	err := w.checkRetry(context.Background(), msg, pushErr)

	// Should return DeadLetterError because retry count >= 3
	dl, ok := err.(*mq.DeadLetterError)
	if !ok {
		t.Fatalf("expected DeadLetterError, got %T: %v", err, err)
	}
	if dl.Retries != 3 {
		t.Errorf("DeadLetterError.Retries = %d, want 3", dl.Retries)
	}
	if dl.Unwrap() != pushErr {
		t.Errorf("DeadLetterError.Unwrap() should return original error")
	}
}

func TestCheckRetry_BeyondMaxRetries(t *testing.T) {
	counter := newMockRetryCounter()
	counter.counts["test-topic"] = 4 // next Incr returns 5
	w := &DeliveryWorker{retryCounter: counter}
	msg := &mq.Message{Topic: "test-topic", Partition: 0, Offset: 99}

	pushErr := errors.New("push failed")
	err := w.checkRetry(context.Background(), msg, pushErr)

	dl, ok := err.(*mq.DeadLetterError)
	if !ok {
		t.Fatalf("expected DeadLetterError, got %T: %v", err, err)
	}
	if dl.Retries != 5 {
		t.Errorf("DeadLetterError.Retries = %d, want 5", dl.Retries)
	}
}
