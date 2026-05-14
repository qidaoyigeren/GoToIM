package mq

import (
	"context"
	"testing"
)

func TestMessageStruct(t *testing.T) {
	msg := &Message{
		Topic:     "test-topic",
		Key:       "user-1001",
		Value:     []byte(`{"hello":"world"}`),
		Headers:   map[string]string{"key1": "val1"},
		Partition: 3,
		Offset:    42,
		Timestamp: 1700000000000,
	}

	if msg.Topic != "test-topic" {
		t.Errorf("Topic = %q, want %q", msg.Topic, "test-topic")
	}
	if msg.Key != "user-1001" {
		t.Errorf("Key = %q, want %q", msg.Key, "user-1001")
	}
	if string(msg.Value) != `{"hello":"world"}` {
		t.Errorf("Value = %q, want %q", string(msg.Value), `{"hello":"world"}`)
	}
	if msg.Headers["key1"] != "val1" {
		t.Errorf("Headers[key1] = %q, want %q", msg.Headers["key1"], "val1")
	}
	if msg.Partition != 3 {
		t.Errorf("Partition = %d, want 3", msg.Partition)
	}
	if msg.Offset != 42 {
		t.Errorf("Offset = %d, want 42", msg.Offset)
	}
	if msg.Timestamp != 1700000000000 {
		t.Errorf("Timestamp = %d, want 1700000000000", msg.Timestamp)
	}
}

func TestMessageZeroValue(t *testing.T) {
	var msg Message
	if msg.Topic != "" {
		t.Errorf("zero Topic = %q, want empty", msg.Topic)
	}
	if msg.Key != "" {
		t.Errorf("zero Key = %q, want empty", msg.Key)
	}
	if msg.Value != nil {
		t.Errorf("zero Value = %v, want nil", msg.Value)
	}
	if msg.Headers != nil {
		t.Errorf("zero Headers = %v, want nil", msg.Headers)
	}
	if msg.Partition != 0 {
		t.Errorf("zero Partition = %d, want 0", msg.Partition)
	}
	if msg.Offset != 0 {
		t.Errorf("zero Offset = %d, want 0", msg.Offset)
	}
}

func TestDeliveryStatusConstants(t *testing.T) {
	tests := []struct {
		name string
		got  DeliveryStatus
		want DeliveryStatus
	}{
		{"StatusPending", StatusPending, 0},
		{"StatusDispatched", StatusDispatched, 1},
		{"StatusDelivered", StatusDelivered, 2},
		{"StatusFailed", StatusFailed, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %d, want %d", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestHeaderDelayedUntilConstant(t *testing.T) {
	if HeaderDelayedUntil != "goim_delayed_until" {
		t.Errorf("HeaderDelayedUntil = %q, want %q", HeaderDelayedUntil, "goim_delayed_until")
	}
}

func TestMessageHandlerType(t *testing.T) {
	called := false
	var handler MessageHandler = func(ctx context.Context, msg *Message) error {
		called = true
		return nil
	}

	msg := &Message{Topic: "t", Value: []byte("test")}
	err := handler(context.Background(), msg)
	if err != nil {
		t.Errorf("handler returned error: %v", err)
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestMessageHandlerError(t *testing.T) {
	expectedErr := context.DeadlineExceeded
	var handler MessageHandler = func(ctx context.Context, msg *Message) error {
		return expectedErr
	}

	err := handler(context.Background(), &Message{})
	if err != expectedErr {
		t.Errorf("handler returned %v, want %v", err, expectedErr)
	}
}

func TestMessageWithNilHeaders(t *testing.T) {
	msg := &Message{
		Topic: "t",
		Key:   "k",
		Value: []byte("v"),
	}
	// Should not panic accessing nil map length
	if len(msg.Headers) != 0 {
		t.Errorf("len(nil Headers) = %d, want 0", len(msg.Headers))
	}
}

func TestMessageEmptyValue(t *testing.T) {
	msg := &Message{
		Topic: "t",
		Value: []byte{},
	}
	if len(msg.Value) != 0 {
		t.Errorf("len(empty Value) = %d, want 0", len(msg.Value))
	}
}
