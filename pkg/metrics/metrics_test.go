package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestPushTotal_Increments(t *testing.T) {
	PushTotal.WithLabelValues("direct", "success").Inc()
	PushTotal.WithLabelValues("direct", "success").Inc()
	val := testutil.ToFloat64(PushTotal.WithLabelValues("direct", "success"))
	assert.Equal(t, float64(2), val)
}

func TestMsgAckTotal_Increments(t *testing.T) {
	before := testutil.ToFloat64(MsgAckTotal)
	MsgAckTotal.Inc()
	MsgAckTotal.Inc()
	MsgAckTotal.Inc()
	after := testutil.ToFloat64(MsgAckTotal)
	assert.Equal(t, before+3, after)
}

func TestConnectionsActive_Gauge(t *testing.T) {
	ConnectionsActive.Set(100)
	assert.Equal(t, float64(100), testutil.ToFloat64(ConnectionsActive))
	ConnectionsActive.Inc()
	assert.Equal(t, float64(101), testutil.ToFloat64(ConnectionsActive))
	ConnectionsActive.Dec()
	assert.Equal(t, float64(100), testutil.ToFloat64(ConnectionsActive))
}

func TestRateLimitedTotal_Increments(t *testing.T) {
	before := testutil.ToFloat64(RateLimitedTotal)
	RateLimitedTotal.Add(5)
	after := testutil.ToFloat64(RateLimitedTotal)
	assert.Equal(t, before+5, after)
}

func TestRetryTotal_Labels(t *testing.T) {
	RetryTotal.WithLabelValues("success").Inc()
	RetryTotal.WithLabelValues("failed").Inc()
	RetryTotal.WithLabelValues("failed").Inc()
	assert.Equal(t, float64(1), testutil.ToFloat64(RetryTotal.WithLabelValues("success")))
	assert.Equal(t, float64(2), testutil.ToFloat64(RetryTotal.WithLabelValues("failed")))
}

func TestPushLatency_Observe(t *testing.T) {
	PushLatency.WithLabelValues("direct").Observe(0.05)
	PushLatency.WithLabelValues("kafka").Observe(0.1)
	count := testutil.CollectAndCount(PushLatency)
	assert.Greater(t, count, 0)
}
