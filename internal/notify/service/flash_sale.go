package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
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
	if s.orderSvc != nil && len(targetUIDs) > 0 && len(targetUIDs) <= 100 {
		for _, uid := range targetUIDs {
			notif, err := s.orderSvc.CreateFlashSaleNotification(uid, sale.SaleID, titleTxt, content)
			if err != nil {
				return sale, err
			}
			_ = s.orderSvc.store.InsertCampaignTarget(sale.SaleID, uid, notif.NotifyID, "queued", notif.CreatedAt)
		}
		return sale, nil
	}

	if s.pushClient == nil {
		return sale, nil
	}
	payload := BuildNotificationJSON("flash_sale", titleTxt, content, sale.SaleID, "")
	pushedAt := time.Now()

	if len(targetUIDs) == 0 {
		// Broadcast to all online users via room broadcast
		body, err := json.Marshal(payload)
		if err != nil {
			return sale, fmt.Errorf("marshal flash sale payload: %w", err)
		}
		_, err = s.pushClient.PushToRoom(protocol.OpRaw, "live", "flash_sale_all", body)
		if err != nil {
			return sale, fmt.Errorf("broadcast flash sale failed: %w", err)
		}
		s.stats.mu.Lock()
		s.stats.TotalPushed++
		s.stats.recordPendingAck(sale.SaleID, 1, pushedAt)
		s.stats.mu.Unlock()
	} else if len(targetUIDs) <= 100 {
		// Small batch: still use user-level routing so Logic can choose direct
		// delivery or reliable fallback per user and expose real path metrics.
		intMids := make([]int64, 0, len(targetUIDs))
		for _, uid := range targetUIDs {
			if id, err := strconv.ParseInt(uid, 10, 64); err == nil {
				intMids = append(intMids, id)
			}
		}
		if len(intMids) == 0 {
			return sale, fmt.Errorf("flash sale has no valid numeric target users")
		}
		if _, err := s.pushClient.PushJSONToUsers(protocol.OpRaw, intMids, payload); err != nil {
			return sale, fmt.Errorf("small batch flash sale failed: %w", err)
		}
		s.stats.mu.Lock()
		s.stats.TotalPushed += int64(len(intMids))
		s.stats.recordPendingAck(sale.SaleID, int64(len(intMids)), pushedAt)
		s.stats.mu.Unlock()
	} else {
		// Large batch: use PushMids for throughput
		intMids := make([]int64, 0, len(targetUIDs))
		for _, uid := range targetUIDs {
			if id, err := strconv.ParseInt(uid, 10, 64); err == nil {
				intMids = append(intMids, id)
			}
		}
		if len(intMids) == 0 {
			return sale, fmt.Errorf("flash sale has no valid numeric target users")
		}
		_, err := s.pushClient.PushJSONToUsers(protocol.OpRaw, intMids, payload)
		if err != nil {
			return sale, fmt.Errorf("batch push flash sale failed: %w", err)
		}
		s.stats.mu.Lock()
		s.stats.TotalPushed += int64(len(intMids))
		s.stats.recordPendingAck(sale.SaleID, int64(len(intMids)), pushedAt)
		s.stats.mu.Unlock()
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
