package service

import (
	"testing"
	"time"
)

func TestRecordAckTracksKnownNotificationOnce(t *testing.T) {
	svc := NewOrderNotifyService(nil)

	svc.stats.mu.Lock()
	svc.stats.TotalPushed = 1
	svc.stats.pushTimes["NTF-1"] = time.Now().Add(-10 * time.Millisecond)
	svc.stats.pendingAcks["NTF-1"] = 1
	svc.stats.mu.Unlock()

	if !svc.RecordAck("NTF-1") {
		t.Fatal("RecordAck returned false for a known notification")
	}

	svc.stats.mu.RLock()
	totalAcked := svc.stats.TotalAcked
	ackRate := svc.stats.AckRate
	latencyP50 := svc.stats.LatencyP50Ms
	svc.stats.mu.RUnlock()

	if totalAcked != 1 {
		t.Fatalf("TotalAcked = %d, want 1", totalAcked)
	}
	if ackRate != 1 {
		t.Fatalf("AckRate = %f, want 1", ackRate)
	}
	if latencyP50 <= 0 {
		t.Fatalf("LatencyP50Ms = %f, want positive latency", latencyP50)
	}

	if svc.RecordAck("NTF-1") {
		t.Fatal("RecordAck returned true for a duplicate notification ACK")
	}

	svc.stats.mu.RLock()
	totalAcked = svc.stats.TotalAcked
	svc.stats.mu.RUnlock()
	if totalAcked != 1 {
		t.Fatalf("TotalAcked after duplicate = %d, want 1", totalAcked)
	}
}

func TestRecordAckIgnoresEmptyAndUnknownNotification(t *testing.T) {
	svc := NewOrderNotifyService(nil)

	svc.stats.mu.Lock()
	svc.stats.TotalPushed = 3
	svc.stats.mu.Unlock()

	if svc.RecordAck("") {
		t.Fatal("RecordAck returned true for an empty notify_id")
	}
	if svc.RecordAck("missing") {
		t.Fatal("RecordAck returned true for an unknown notify_id")
	}

	svc.stats.mu.RLock()
	totalAcked := svc.stats.TotalAcked
	ackRate := svc.stats.AckRate
	svc.stats.mu.RUnlock()
	if totalAcked != 0 {
		t.Fatalf("TotalAcked = %d, want 0", totalAcked)
	}
	if ackRate != 0 {
		t.Fatalf("AckRate = %f, want 0", ackRate)
	}
}

func TestRecordAckAllowsExpectedCountForSharedNotificationID(t *testing.T) {
	svc := NewOrderNotifyService(nil)

	svc.stats.mu.Lock()
	svc.stats.TotalPushed = 2
	svc.stats.pushTimes["SALE-1"] = time.Now().Add(-10 * time.Millisecond)
	svc.stats.pendingAcks["SALE-1"] = 2
	svc.stats.mu.Unlock()

	if !svc.RecordAck("SALE-1") {
		t.Fatal("first RecordAck returned false")
	}
	if !svc.RecordAck("SALE-1") {
		t.Fatal("second RecordAck returned false")
	}
	if svc.RecordAck("SALE-1") {
		t.Fatal("third RecordAck returned true after expected count was exhausted")
	}

	svc.stats.mu.RLock()
	totalAcked := svc.stats.TotalAcked
	ackRate := svc.stats.AckRate
	svc.stats.mu.RUnlock()
	if totalAcked != 2 {
		t.Fatalf("TotalAcked = %d, want 2", totalAcked)
	}
	if ackRate != 1 {
		t.Fatalf("AckRate = %f, want 1", ackRate)
	}
}
