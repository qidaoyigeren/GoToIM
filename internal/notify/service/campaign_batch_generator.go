package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/policy"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// CampaignBatchGeneratorConfig controls background campaign target materialization.
type CampaignBatchGeneratorConfig struct {
	Enabled          bool
	BatchSize        int
	WorkerCount      int
	TargetBatchSize  int
	PollInterval     time.Duration
	LockTTL          time.Duration
	DefaultRateLimit int
}

// CampaignBatchStore is the storage surface used by CampaignBatchGenerator.
type CampaignBatchStore interface {
	ClaimCampaignAudienceBatches(workerID string, batchSize int, lockTTL time.Duration) ([]*model.CampaignAudienceBatch, error)
	GetCampaign(campaignID string) (*model.Campaign, error)
	ListCampaignAudienceTargetsByBatch(batchID string, statuses []string, limit int) ([]*model.CampaignAudienceTarget, error)
	MarkCampaignAudienceTargetFailed(audienceID, userID, lastError string) error
	MarkCampaignAudienceTargetExpired(audienceID, userID string) error
	MarkCampaignAudienceBatchResult(batchID, workerID, status, lastError string, successCount, failedCount int64) error
}

// CampaignTargetNotificationCreator creates durable notification/outbox rows for one target.
type CampaignTargetNotificationCreator interface {
	CreateCampaignAudienceTargetNotification(target *model.CampaignAudienceTarget, title, content string) (*model.Notification, error)
}

// CampaignBatchGenerator turns imported campaign audience batches into notification/outbox rows.
type CampaignBatchGenerator struct {
	store    CampaignBatchStore
	creator  CampaignTargetNotificationCreator
	cfg      CampaignBatchGeneratorConfig
	workerID string
	stopCh   chan struct{}
	wg       sync.WaitGroup
	limiters sync.Map // campaignID -> *rateLimiter
}

// NewCampaignBatchGenerator creates a campaign audience batch generator.
func NewCampaignBatchGenerator(store CampaignBatchStore, creator CampaignTargetNotificationCreator, cfg CampaignBatchGeneratorConfig) *CampaignBatchGenerator {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 1
	}
	if cfg.TargetBatchSize <= 0 {
		cfg.TargetBatchSize = 500
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = 30 * time.Second
	}
	return &CampaignBatchGenerator{
		store:    store,
		creator:  creator,
		cfg:      cfg,
		workerID: fmt.Sprintf("notify-campaign-generator-%d", time.Now().UnixNano()),
		stopCh:   make(chan struct{}),
	}
}

// Start begins background campaign batch generation.
func (g *CampaignBatchGenerator) Start() {
	if g == nil || !g.cfg.Enabled {
		return
	}
	for i := 0; i < g.cfg.WorkerCount; i++ {
		g.wg.Add(1)
		go func(worker int) {
			defer g.wg.Done()
			ticker := time.NewTicker(g.cfg.PollInterval)
			defer ticker.Stop()
			for {
				if err := g.ProcessOnce(context.Background()); err != nil {
					log.Printf("[campaign_batch_generator] process_once failed worker=%d worker_id=%s err=%v", worker, g.workerID, err)
				}
				select {
				case <-ticker.C:
				case <-g.stopCh:
					return
				}
			}
		}(i)
	}
}

// Stop gracefully stops the generator.
func (g *CampaignBatchGenerator) Stop() {
	if g == nil {
		return
	}
	close(g.stopCh)
	g.wg.Wait()
}

// ProcessOnce claims and processes one set of campaign audience batches.
func (g *CampaignBatchGenerator) ProcessOnce(ctx context.Context) error {
	if g == nil || g.store == nil || g.creator == nil {
		return nil
	}
	batches, err := g.store.ClaimCampaignAudienceBatches(g.workerID, g.cfg.BatchSize, g.cfg.LockTTL)
	if err != nil {
		return err
	}
	var firstErr error
	for _, batch := range batches {
		if err := g.processBatch(ctx, batch); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (g *CampaignBatchGenerator) processBatch(ctx context.Context, batch *model.CampaignAudienceBatch) error {
	if batch == nil {
		return nil
	}
	campaign, err := g.store.GetCampaign(batch.CampaignID)
	if err != nil {
		_ = g.store.MarkCampaignAudienceBatchResult(batch.BatchID, g.workerID, "failed", err.Error(), 0, 0)
		return err
	}
	if campaign.Status == model.CampaignPaused {
		return g.store.MarkCampaignAudienceBatchResult(batch.BatchID, g.workerID, "retrying", "campaign_paused", 0, 0)
	}
	if campaign.Status == model.CampaignCancelled {
		return g.store.MarkCampaignAudienceBatchResult(batch.BatchID, g.workerID, "cancelled", "campaign_cancelled", 0, 0)
	}
	title, content := FlashSaleNotification(campaign.Title, campaign.Description)
	targets, err := g.store.ListCampaignAudienceTargetsByBatch(batch.BatchID, []string{"pending", "retrying"}, g.cfg.TargetBatchSize+1)
	if err != nil {
		_ = g.store.MarkCampaignAudienceBatchResult(batch.BatchID, g.workerID, "failed", err.Error(), 0, 0)
		return err
	}
	hasMore := len(targets) > g.cfg.TargetBatchSize
	if hasMore {
		targets = targets[:g.cfg.TargetBatchSize]
	}
	var success, retryableFailed, expired int64
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if g.isCampaignExpired(campaign) {
			if err := g.store.MarkCampaignAudienceTargetExpired(target.AudienceID, target.UserID); err != nil {
				retryableFailed++
				continue
			}
			expired++
			continue
		}
		g.waitCampaignToken(campaign.CampaignID, campaign.RateLimit)
		if _, err := g.creator.CreateCampaignAudienceTargetNotification(target, title, content); err != nil {
			retryableFailed++
			_ = g.store.MarkCampaignAudienceTargetFailed(target.AudienceID, target.UserID, err.Error())
			continue
		}
		success++
	}
	status := "completed"
	lastError := ""
	if retryableFailed > 0 {
		status = "failed"
		lastError = "target_generation_failed"
	}
	if hasMore && retryableFailed == 0 {
		status = "retrying"
	}
	if err := g.store.MarkCampaignAudienceBatchResult(batch.BatchID, g.workerID, status, lastError, success, retryableFailed+expired); err != nil {
		return err
	}
	metrics.NotifyCampaignBatchProcessedTotal.WithLabelValues(status).Inc()
	metrics.NotifyCampaignTargetGeneratedTotal.Add(float64(success))
	metrics.NotifyCampaignTargetExpiredTotal.Add(float64(expired))
	log.Printf("[campaign_batch_generator] processed batch_id=%s campaign_id=%s status=%s success=%d failed=%d expired=%d",
		batch.BatchID, batch.CampaignID, status, success, retryableFailed, expired)
	return nil
}

func (g *CampaignBatchGenerator) isCampaignExpired(campaign *model.Campaign) bool {
	if campaign == nil {
		return false
	}
	ttl := policy.Resolve("flash_sale", "notify").TTL
	return ttl > 0 && time.Since(campaign.CreatedAt) > ttl
}

func (g *CampaignBatchGenerator) waitCampaignToken(campaignID string, rateLimit int) {
	if rateLimit <= 0 {
		rateLimit = g.cfg.DefaultRateLimit
	}
	if rateLimit <= 0 {
		return
	}
	rl := &rateLimiter{
		tokens:     float64(rateLimit),
		lastRefill: time.Now(),
		ratePerSec: float64(rateLimit),
	}
	actual, _ := g.limiters.LoadOrStore(campaignID, rl)
	limiter := actual.(*rateLimiter)
	sleep := time.Second / time.Duration(rateLimit)
	if sleep < time.Millisecond {
		sleep = time.Millisecond
	}
	for !limiter.allow() {
		metrics.NotifyCampaignRateLimitedTotal.Inc()
		time.Sleep(sleep)
	}
}
