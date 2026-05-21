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

	// RoomPushDroppedTotal counts room messages dropped because a connection queue is full.
	RoomPushDroppedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "comet",
		Name:      "room_push_dropped_total",
		Help:      "Total room push messages dropped by full connection queues",
	})

	// RetryTotal counts retry attempts.
	RetryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "logic",
		Name:      "retry_total",
		Help:      "Total retry attempts",
	}, []string{"result"})

	// NotifyPushCircuitBreakerState exposes the PushClient circuit breaker state.
	// The active state label is set to 1 and the inactive states are set to 0.
	NotifyPushCircuitBreakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "goim",
		Subsystem: "notify",
		Name:      "push_circuit_breaker_state",
		Help:      "PushClient circuit breaker state, labelled closed/open/half_open",
	}, []string{"state"})

	NotifyPushCircuitBreakerOpenTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "notify",
		Name:      "push_circuit_breaker_open_total",
		Help:      "Total number of times the PushClient circuit breaker opened",
	})

	NotifyPushCircuitBreakerFailures = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "goim",
		Subsystem: "notify",
		Name:      "push_circuit_breaker_failures",
		Help:      "Current consecutive PushClient circuit breaker failure count",
	})

	NotifyPushCircuitBreakerBlockedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "notify",
		Name:      "push_circuit_breaker_blocked_total",
		Help:      "Total PushClient requests blocked while the circuit breaker is open",
	})

	// SpoolWriteTotal counts messages written to the local durable spool.
	SpoolWriteTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "router",
		Name:      "spool_write_total",
		Help:      "Total messages written to local durable spool (Redis+Kafka both failed)",
	})

	// SpoolReplayTotal counts spool replay attempts by result.
	SpoolReplayTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "router",
		Name:      "spool_replay_total",
		Help:      "Total spool replay attempts (success/failed/expired)",
	}, []string{"result"})

	// SpoolFileCount tracks the current number of files in the spool directory.
	SpoolFileCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "goim",
		Subsystem: "router",
		Name:      "spool_file_count",
		Help:      "Current number of spool files awaiting replay",
	})
)
