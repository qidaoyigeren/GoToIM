package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/policy"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// FlashSaleService handles flash sale notification bursts.
type FlashSaleService struct {
	mu                   sync.RWMutex
	sales                map[string]*model.FlashSale
	pushClient           *PushClient
	stats                *StatsCollector
	orderSvc             *OrderNotifyService
	defaultRateLimit     int
	asyncFanoutThreshold int
	audienceBatchSize    int
}

// NewFlashSaleService creates a new FlashSaleService.
func NewFlashSaleService(pushClient *PushClient, stats *StatsCollector) *FlashSaleService {
	return &FlashSaleService{
		sales:      make(map[string]*model.FlashSale),
		pushClient: pushClient,
		stats:      stats,
	}
}

// SetOrderService enables durable campaign, notification, and outbox writes.
func (s *FlashSaleService) SetOrderService(orderSvc *OrderNotifyService) {
	s.orderSvc = orderSvc
}

// SetDefaultRateLimit configures campaign target creation pacing.
func (s *FlashSaleService) SetDefaultRateLimit(limit int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultRateLimit = limit
}

// SetBatchGeneration configures when raw campaign targets are converted into background batches.
func (s *FlashSaleService) SetBatchGeneration(asyncThreshold, audienceBatchSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asyncFanoutThreshold = asyncThreshold
	s.audienceBatchSize = audienceBatchSize
}

// CreateFlashSale creates a new flash sale and sends notifications to target users.
// If targetUIDs is empty, broadcasts to all online users via room broadcast.
func (s *FlashSaleService) CreateFlashSale(title, description string, targetUIDs []string) (*model.FlashSale, error) {
	return s.CreateFlashSaleWithAudience(title, description, targetUIDs, "")
}

// CreateFlashSaleWithAudience creates a flash sale from raw targets or an imported audience snapshot.
func (s *FlashSaleService) CreateFlashSaleWithAudience(title, description string, targetUIDs []string, audienceID string) (*model.FlashSale, error) {
	return s.CreateFlashSaleWithAudienceAndRateLimit(title, description, targetUIDs, audienceID, 0)
}

// CreateFlashSaleWithAudienceAndRateLimit creates a campaign with an optional per-campaign rate override.
func (s *FlashSaleService) CreateFlashSaleWithAudienceAndRateLimit(title, description string, targetUIDs []string, audienceID string, rateLimit int) (*model.FlashSale, error) {
	if rateLimit <= 0 {
		rateLimit = s.currentDefaultRateLimit()
	}
	sale := &model.FlashSale{
		SaleID:      fmt.Sprintf("FLS-%06d", time.Now().UnixNano()%1000000),
		Title:       title,
		Description: description,
		TargetUIDs:  targetUIDs,
		StartAt:     time.Now(),
		CreatedAt:   time.Now(),
	}
	if audienceID != "" && s.orderSvc != nil {
		batches, err := s.orderSvc.ListCampaignAudienceBatches(audienceID)
		if err != nil {
			return nil, err
		}
		targetCount := 0
		if len(batches) > 0 && batches[0].CampaignID != "" {
			sale.SaleID = batches[0].CampaignID
		}
		for _, batch := range batches {
			targetCount += int(batch.TargetCount)
		}
		sale.TargetUIDs = nil
		s.mu.Lock()
		s.sales[sale.SaleID] = sale
		s.mu.Unlock()
		s.persistCampaignSummary(sale, title, description, targetCount, rateLimit)
		return sale, nil
	}

	s.mu.Lock()
	s.sales[sale.SaleID] = sale
	s.mu.Unlock()
	s.persistCampaignSummary(sale, title, description, len(targetUIDs), rateLimit)

	titleTxt, content := FlashSaleNotification(title, description)
	if s.orderSvc != nil && len(targetUIDs) > 0 {
		if s.shouldGenerateAsync(len(targetUIDs)) {
			_, _, err := s.orderSvc.ImportCampaignAudience(sale.SaleID, "flash_sale_targets", map[string]string{"source": "flash_sale"}, targetUIDs, s.currentAudienceBatchSize())
			if err != nil {
				return sale, err
			}
			sale.TargetUIDs = nil
			return sale, nil
		}
		return s.persistTargetedFlashSale(sale, titleTxt, content, targetUIDs, "", nil, rateLimit)
	}

	if s.orderSvc != nil && len(targetUIDs) == 0 {
		_, err := s.orderSvc.CreateFlashSaleBroadcastNotification(sale.SaleID, titleTxt, content)
		if err != nil {
			return sale, err
		}
		return sale, nil
	}

	if s.pushClient == nil {
		return sale, nil
	}

	return sale, nil
}

func (s *FlashSaleService) persistCampaignSummary(sale *model.FlashSale, title, description string, targetCount, rateLimit int) {
	if s.orderSvc == nil || sale == nil {
		return
	}
	_ = s.orderSvc.store.InsertCampaignWithRateLimit(sale.SaleID, title, description, "flash_sale", targetCount, "", rateLimit, time.Now())
}

func (s *FlashSaleService) currentDefaultRateLimit() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaultRateLimit
}

func (s *FlashSaleService) shouldGenerateAsync(targetCount int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.asyncFanoutThreshold > 0 && targetCount > s.asyncFanoutThreshold
}

func (s *FlashSaleService) currentAudienceBatchSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.audienceBatchSize <= 0 {
		return 500
	}
	return s.audienceBatchSize
}

func (s *FlashSaleService) persistTargetedFlashSale(sale *model.FlashSale, title, content string, targetUIDs []string, audienceID string, audienceTargets []*model.CampaignAudienceTarget, rateLimit int) (*model.FlashSale, error) {
	const batchSize = 500
	targetByUID := make(map[string]*model.CampaignAudienceTarget, len(audienceTargets))
	for _, target := range audienceTargets {
		targetByUID[target.UserID] = target
	}
	ttl := policy.Resolve("flash_sale", "notify").TTL
	for start := 0; start < len(targetUIDs); start += batchSize {
		end := start + batchSize
		if end > len(targetUIDs) {
			end = len(targetUIDs)
		}
		for _, uid := range targetUIDs[start:end] {
			if ttl > 0 && time.Since(sale.CreatedAt) > ttl {
				_ = s.orderSvc.store.InsertCampaignTarget(sale.SaleID, uid, "", "expired", time.Now())
				continue
			}
			s.waitCampaignToken(sale.SaleID, rateLimit)
			notif, err := s.orderSvc.CreateFlashSaleNotification(uid, sale.SaleID, title, content)
			if err != nil {
				return sale, err
			}
			_ = s.orderSvc.store.InsertCampaignTarget(sale.SaleID, uid, notif.NotifyID, "pending", notif.CreatedAt)
			if audienceID != "" {
				_ = s.orderSvc.MarkCampaignAudienceTargetCreated(audienceID, uid, notif.NotifyID)
				if targetByUID[uid] != nil {
					targetByUID[uid].NotifyID = notif.NotifyID
					targetByUID[uid].Status = "created"
				}
			}
		}
		if end < len(targetUIDs) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	return sale, nil
}

func (s *FlashSaleService) waitCampaignToken(campaignID string, rateLimit int) {
	if rateLimit <= 0 || s.orderSvc == nil {
		return
	}
	sleep := time.Second / time.Duration(rateLimit)
	if sleep < time.Millisecond {
		sleep = time.Millisecond
	}
	for !s.orderSvc.tryAcquireCampaignToken(campaignID, rateLimit) {
		metrics.NotifyCampaignRateLimitedTotal.Inc()
		time.Sleep(sleep)
	}
}

// GetFlashSale returns a flash sale by ID.
func (s *FlashSaleService) GetFlashSale(saleID string) (*model.FlashSale, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sale, ok := s.sales[saleID]
	return sale, ok
}
