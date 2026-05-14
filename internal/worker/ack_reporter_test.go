package worker

import (
	"context"
	"sync"
	"testing"

	"github.com/Terry-Mao/goim/internal/mq"
)

// mockProducer implements mq.Producer for testing.
type mockProducer struct {
	mu     sync.Mutex
	acks   []ackRecord
	closed bool
}

type ackRecord struct {
	msgID  string
	uid    int64
	status string
}

func (m *mockProducer) EnqueueToUser(ctx context.Context, uid int64, msg *mq.Message) error {
	return nil
}
func (m *mockProducer) EnqueueToUsers(ctx context.Context, uids []int64, msg *mq.Message) error {
	return nil
}
func (m *mockProducer) EnqueueToRoom(ctx context.Context, roomID string, msg *mq.Message) error {
	return nil
}
func (m *mockProducer) EnqueueBroadcast(ctx context.Context, msg *mq.Message, speed int32) error {
	return nil
}
func (m *mockProducer) EnqueueACK(ctx context.Context, msgID string, uid int64, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acks = append(m.acks, ackRecord{msgID, uid, status})
	return nil
}
func (m *mockProducer) EnqueueDelayed(ctx context.Context, uid int64, msg *mq.Message, delayMs int64) error {
	return nil
}
func (m *mockProducer) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockProducer) getACKs() []ackRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]ackRecord, len(m.acks))
	copy(result, m.acks)
	return result
}

func TestACKReporterNilProducer(t *testing.T) {
	r := &ACKReporter{}
	// Should not panic
	r.Report(context.Background(), DeliveryResult{
		MsgID:  "msg-1",
		UID:    1001,
		Status: mq.StatusDelivered,
	})
}

func TestACKReporterDelivered(t *testing.T) {
	p := &mockProducer{}
	r := &ACKReporter{}
	r.SetProducer(p)

	r.Report(context.Background(), DeliveryResult{
		MsgID:  "msg-1",
		UID:    1001,
		Status: mq.StatusDelivered,
	})

	acks := p.getACKs()
	if len(acks) != 1 {
		t.Fatalf("got %d acks, want 1", len(acks))
	}
	if acks[0].msgID != "msg-1" {
		t.Errorf("msgID = %q, want %q", acks[0].msgID, "msg-1")
	}
	if acks[0].uid != 1001 {
		t.Errorf("uid = %d, want 1001", acks[0].uid)
	}
	if acks[0].status != "delivered" {
		t.Errorf("status = %q, want %q", acks[0].status, "delivered")
	}
}

func TestACKReporterFailed(t *testing.T) {
	p := &mockProducer{}
	r := &ACKReporter{}
	r.SetProducer(p)

	r.Report(context.Background(), DeliveryResult{
		MsgID:  "msg-2",
		UID:    1002,
		Status: mq.StatusFailed,
	})

	acks := p.getACKs()
	if len(acks) != 1 {
		t.Fatalf("got %d acks, want 1", len(acks))
	}
	if acks[0].status != "failed" {
		t.Errorf("status = %q, want %q", acks[0].status, "failed")
	}
}

func TestACKReporterMultipleReports(t *testing.T) {
	p := &mockProducer{}
	r := &ACKReporter{}
	r.SetProducer(p)

	for i := int64(0); i < 5; i++ {
		r.Report(context.Background(), DeliveryResult{
			MsgID:  "msg-" + string(rune('0'+i)),
			UID:    1000 + i,
			Status: mq.StatusDelivered,
		})
	}

	acks := p.getACKs()
	if len(acks) != 5 {
		t.Errorf("got %d acks, want 5", len(acks))
	}
}

func TestDeliveryResultStruct(t *testing.T) {
	r := DeliveryResult{
		MsgID:  "test-msg",
		UID:    42,
		Status: mq.StatusDispatched,
	}
	if r.MsgID != "test-msg" {
		t.Errorf("MsgID = %q, want %q", r.MsgID, "test-msg")
	}
	if r.UID != 42 {
		t.Errorf("UID = %d, want 42", r.UID)
	}
	if r.Status != mq.StatusDispatched {
		t.Errorf("Status = %d, want %d", r.Status, mq.StatusDispatched)
	}
}
