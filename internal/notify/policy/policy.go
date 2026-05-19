package policy

import "time"

const (
	AckNone          = "none"
	AckBestEffort    = "best_effort"
	AckAnyDevice     = "any_device"
	AckPrimaryDevice = "primary_device"
	AckAllDevices    = "all_devices"
)

// Policy describes business-level delivery and ACK behavior for a notification.
type Policy struct {
	BusinessType     string
	EventType        string
	Priority         string
	TTL              time.Duration
	AckPolicy        string
	ExpectedAckCount int64
	MaxRetries       int64
	FallbackEnabled  bool
	DLQEnabled       bool
}

// Resolve returns the default policy for a business event.
func Resolve(businessType, eventType string) Policy {
	p := Policy{
		BusinessType:     businessType,
		EventType:        eventType,
		Priority:         "normal",
		TTL:              10 * time.Minute,
		AckPolicy:        AckAnyDevice,
		ExpectedAckCount: 1,
		MaxRetries:       3,
		FallbackEnabled:  true,
		DLQEnabled:       true,
	}

	switch businessType + "." + eventType {
	case "order.created":
		p.Priority = "normal"
		p.TTL = 10 * time.Minute
		p.MaxRetries = 3
	case "order.paid":
		p.Priority = "high"
		p.TTL = 10 * time.Minute
		p.MaxRetries = 5
	case "order.shipped":
		p.Priority = "high"
		p.TTL = 24 * time.Hour
		p.MaxRetries = 5
	case "order.delivered":
		p.Priority = "normal"
		p.TTL = 24 * time.Hour
		p.MaxRetries = 3
	case "order.delivery_failed":
		p.Priority = "critical"
		p.TTL = 24 * time.Hour
		p.MaxRetries = 5
		p.DLQEnabled = true
	case "logistics.update":
		p.Priority = "normal"
		p.TTL = 24 * time.Hour
		p.MaxRetries = 3
	case "flash_sale.notify":
		p.Priority = "low"
		p.TTL = 30 * time.Second
		p.AckPolicy = AckBestEffort
		p.ExpectedAckCount = 0
		p.MaxRetries = 1
		p.DLQEnabled = false
	}
	return p
}
