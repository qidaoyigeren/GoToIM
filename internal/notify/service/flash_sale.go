package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

// FlashSaleService handles flash sale notification bursts.
type FlashSaleService struct {
	mu         sync.RWMutex
	sales      map[string]*model.FlashSale
	pushClient *PushClient
	stats      *StatsCollector
	orderSvc   *OrderNotifyService
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

// CreateFlashSale creates a new flash sale and sends notifications to target users.
// If targetUIDs is empty, broadcasts to all online users via room broadcast.
func (s *FlashSaleService) CreateFlashSale(title, description string, targetUIDs []string) (*model.FlashSale, error) {
	return s.CreateFlashSaleWithAudience(title, description, targetUIDs, "")
}

// CreateFlashSaleWithAudience creates a flash sale from raw targets or an imported audience snapshot.
func (s *FlashSaleService) CreateFlashSaleWithAudience(title, description string, targetUIDs []string, audienceID string) (*model.FlashSale, error) {
	var audienceTargets []*model.CampaignAudienceTarget
	if audienceID != "" && s.orderSvc != nil {
		targets, err := s.orderSvc.ListCampaignAudienceTargets(audienceID, []string{"pending", "failed"}, 0)
		if err != nil {
			return nil, err
		}
		audienceTargets = targets
		targetUIDs = make([]string, 0, len(targets))
		for _, target := range targets {
			targetUIDs = append(targetUIDs, target.UserID)
		}
	}
	sale := &model.FlashSale{
		SaleID:      fmt.Sprintf("FLS-%06d", time.Now().UnixNano()%1000000),
		Title:       title,
		Description: description,
		TargetUIDs:  targetUIDs,
		StartAt:     time.Now(),
		CreatedAt:   time.Now(),
	}

	s.mu.Lock()
	s.sales[sale.SaleID] = sale
	s.mu.Unlock()
	if s.orderSvc != nil {
		now := time.Now()
		_ = s.orderSvc.store.InsertCampaign(sale.SaleID, title, description, "flash_sale", len(targetUIDs), "", now)
	}

	titleTxt, content := FlashSaleNotification(title, description)
	if s.orderSvc != nil && len(targetUIDs) > 0 {
		return s.persistTargetedFlashSale(sale, titleTxt, content, targetUIDs, audienceID, audienceTargets)
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

func (s *FlashSaleService) persistTargetedFlashSale(sale *model.FlashSale, title, content string, targetUIDs []string, audienceID string, audienceTargets []*model.CampaignAudienceTarget) (*model.FlashSale, error) {
	const batchSize = 500
	targetByUID := make(map[string]*model.CampaignAudienceTarget, len(audienceTargets))
	for _, target := range audienceTargets {
		targetByUID[target.UserID] = target
	}
	for start := 0; start < len(targetUIDs); start += batchSize {
		end := start + batchSize
		if end > len(targetUIDs) {
			end = len(targetUIDs)
		}
		for _, uid := range targetUIDs[start:end] {
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

// GetFlashSale returns a flash sale by ID.
func (s *FlashSaleService) GetFlashSale(saleID string) (*model.FlashSale, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sale, ok := s.sales[saleID]
	return sale, ok
}
