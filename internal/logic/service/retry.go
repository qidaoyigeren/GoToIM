package service

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	log "github.com/Terry-Mao/goim/pkg/log"
)

const (
	maxRetries    = 3
	retryInterval = 5 * time.Second
	retryTimeout  = 30 * time.Second // messages older than this are eligible for retry
)

// RetryWorker periodically scans for timed-out messages and retries delivery.
type RetryWorker struct {
	ackService *AckService
	pushSvc    *PushService
	retryDAO   dao.RetryDAO
	msgDAO     dao.MessageDAO
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewRetryWorker creates a new RetryWorker.
func NewRetryWorker(ackSvc *AckService, pushSvc *PushService, retryDAO dao.RetryDAO, msgDAO dao.MessageDAO) *RetryWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &RetryWorker{
		ackService: ackSvc,
		pushSvc:    pushSvc,
		retryDAO:   retryDAO,
		msgDAO:     msgDAO,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start begins the retry worker loop.
func (w *RetryWorker) Start() {
	w.wg.Add(1)
	go w.run()
	log.Info("retry worker started")
}

// Stop gracefully stops the retry worker.
func (w *RetryWorker) Stop() {
	w.cancel()
	w.wg.Wait()
	log.Info("retry worker stopped")
}

func (w *RetryWorker) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.scanAndRetry()
		}
	}
}

// scanAndRetry scans for pending messages that have timed out and retries them.
func (w *RetryWorker) scanAndRetry() {
	ctx, cancel := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancel()

	now := float64(time.Now().UnixMilli())
	members, err := w.retryDAO.DequeueRetry(ctx, 0, now, 100)
	if err != nil {
		log.Errorf("dequeue retry failed: %v", err)
		return
	}

	if len(members) == 0 {
		return
	}

	log.V(1).Infof("retry worker found %d candidates", len(members))

	for _, member := range members {
		parts := strings.SplitN(member, ":", 2)
		if len(parts) != 2 {
			log.Warningf("invalid retry member format: %s", member)
			continue
		}
		msgID := parts[0]
		uid, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			log.Warningf("invalid retry uid: %s", parts[1])
			continue
		}

		w.retryMessage(ctx, msgID, uid)
	}
}

func (w *RetryWorker) retryMessage(ctx context.Context, msgID string, uid int64) {
	// Check message current status
	msgData, err := w.msgDAO.GetMessageStatus(ctx, msgID)
	if err != nil || len(msgData) == 0 {
		w.retryDAO.RemoveRetry(ctx, msgID, uid)
		return
	}

	status := msgData["status"]
	if status == MsgStatusAcked || status == MsgStatusFailed {
		// Already resolved, remove from retry queue
		w.retryDAO.RemoveRetry(ctx, msgID, uid)
		return
	}

	// Check retry count
	cnt, err := w.retryDAO.IncrRetryCount(ctx, msgID)
	if err != nil {
		log.Errorf("incr retry count failed: msg_id=%s err=%v", msgID, err)
		return
	}
	if cnt > maxRetries {
		w.ackService.MarkFailed(ctx, msgID)
		w.retryDAO.RemoveRetry(ctx, msgID, uid)
		log.Warningf("msg %s exceeded max retries (%d), marked failed", msgID, maxRetries)
		return
	}

	// Decode message body
	var body []byte
	if v, ok := msgData["body"]; ok && v != "" {
		body, _ = base64.StdEncoding.DecodeString(v)
	}
	op, _ := strconv.ParseInt(msgData["op"], 10, 32)

	// Re-push the message
	log.Infof("retrying msg_id=%s uid=%d attempt=%d", msgID, uid, cnt)
	if err := w.pushSvc.PushToUser(ctx, msgID, uid, int32(op), body, 0); err != nil {
		log.Warningf("retry push failed: msg_id=%s uid=%d cnt=%d err=%v", msgID, uid, cnt, err)
		// Re-enqueue for next retry
		nextRetry := float64(time.Now().Add(retryInterval).UnixMilli())
		w.retryDAO.EnqueueRetry(ctx, msgID, uid, nextRetry)
	} else {
		// Push succeeded (at least to Kafka), remove from retry queue
		// The ACK flow will handle final resolution
		w.retryDAO.RemoveRetry(ctx, msgID, uid)
	}
}

// EnqueueForRetry adds a message to the retry queue with a scheduled retry time.
func (w *RetryWorker) EnqueueForRetry(ctx context.Context, msgID string, uid int64) {
	score := float64(time.Now().Add(retryTimeout).UnixMilli())
	if err := w.retryDAO.EnqueueRetry(ctx, msgID, uid, score); err != nil {
		log.Errorf("enqueue retry failed: msg_id=%s uid=%d err=%v", msgID, uid, err)
	}
}
