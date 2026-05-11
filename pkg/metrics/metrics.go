package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// PushTotal counts push operations by type and status.
	PushTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "logic",
		Name:      "push_total",
		Help:      "Total push operations",
	}, []string{"type", "status"})

	// MsgAckTotal counts ACK messages received.
	MsgAckTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "logic",
		Name:      "msg_ack_total",
		Help:      "Total ACK messages processed",
	})

	// PushLatency measures push latency by channel type.
	PushLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "goim",
		Subsystem: "logic",
		Name:      "push_latency_seconds",
		Help:      "Push latency in seconds",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
	}, []string{"type"})

	// ConnectionsActive tracks active connections.
	ConnectionsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "goim",
		Subsystem: "comet",
		Name:      "connections_active",
		Help:      "Number of active connections",
	})

	// RateLimitedTotal counts rate-limited messages.
	RateLimitedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "comet",
		Name:      "rate_limited_total",
		Help:      "Total rate-limited messages",
	})

	// RetryTotal counts retry attempts.
	RetryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "logic",
		Name:      "retry_total",
		Help:      "Total retry attempts",
	}, []string{"result"})
)
