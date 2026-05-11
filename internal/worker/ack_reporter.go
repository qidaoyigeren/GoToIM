package worker

import (
	"context"

	"github.com/Terry-Mao/goim/internal/mq"
	log "github.com/Terry-Mao/goim/pkg/log"
)

// DeliveryResult records the outcome of a single message delivery attempt.
type DeliveryResult struct {
	MsgID  string
	UID    int64
	Status mq.DeliveryStatus
}

// ACKReporter writes delivery status back to the MQ/Logic layer
// after each gRPC push completes (success or failure).
type ACKReporter struct {
	producer mq.Producer
}

// SetProducer sets the MQ producer for ACK reporting.
func (r *ACKReporter) SetProducer(p mq.Producer) {
	r.producer = p
}

// Report writes a delivery result to the MQ ACK topic.
func (r *ACKReporter) Report(ctx context.Context, result DeliveryResult) {
	if r.producer == nil {
		return
	}
	status := "delivered"
	if result.Status == mq.StatusFailed {
		status = "failed"
	}
	if err := r.producer.EnqueueACK(ctx, result.MsgID, result.UID, status); err != nil {
		log.Warningf("ack report failed: msg_id=%s uid=%d err=%v", result.MsgID, result.UID, err)
	}
}
