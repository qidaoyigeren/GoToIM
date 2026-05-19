package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/tracectx"
)

func TestPushClientPropagatesTraceHeaders(t *testing.T) {
	var gotTrace string
	logic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTrace = r.Header.Get(tracectx.TraceIDHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"msg_ids":["msg-1"]}}`))
	}))
	defer logic.Close()

	client := NewPushClientWithConfig(strings.TrimPrefix(logic.URL, "http://"), PushClientConfig{
		Timeout:    time.Second,
		MaxRetries: 0,
	})
	results, err := client.PushJSONToUsersDetailedContext(context.Background(), 1, []int64{1001}, map[string]string{"trace_id": "trace-123"})
	if err != nil {
		t.Fatalf("push returned error: %v", err)
	}
	if gotTrace != "trace-123" {
		t.Fatalf("trace header = %q, want trace-123", gotTrace)
	}
	if len(results) != 1 || results[0].TraceID != "trace-123" {
		t.Fatalf("delivery results = %+v, want trace_id propagated", results)
	}
}

func TestCircuitBreakerSnapshotCountsTransitions(t *testing.T) {
	breaker := NewCircuitBreaker(1, time.Minute)
	breaker.RecordFailure()
	if breaker.Allow() {
		t.Fatal("open breaker allowed request")
	}
	snap := breaker.Snapshot()
	if snap.State != "open" || snap.OpenCount != 1 || snap.FailureCount != 1 || snap.BlockedRequests != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
}
