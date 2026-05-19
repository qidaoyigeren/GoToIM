package notify

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	NotifyTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "notify",
		Name:      "notifications_total",
		Help:      "Total notifications created",
	}, []string{"business_type", "event_type", "priority"})

	OutboxPending = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "goim",
		Subsystem: "notify",
		Name:      "outbox_pending",
		Help:      "Number of pending outbox items",
	})

	DLQTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "goim",
		Subsystem: "notify",
		Name:      "dlq_total",
		Help:      "Number of items in DLQ",
	})

	ACKTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "goim",
		Subsystem: "notify",
		Name:      "acks_total",
		Help:      "Total ACKs received",
	}, []string{"policy", "satisfied"})
)
