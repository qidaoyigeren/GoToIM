package service

import (
	"encoding/json"
	"fmt"
	"strconv"
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
}

// NewFlashSaleService creates a new FlashSaleService.
func NewFlashSaleService(pushClient *PushClient, stats *StatsCollector) *FlashSaleService {
	return &FlashSaleService{
		sales:      make(map[string]*model.FlashSale),
		pushClient: pushClient,
		stats:      stats,
	}
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

	titleTxt, content := FlashSaleNotification(title, description)
	payload := BuildNotificationJSON("flash_sale", titleTxt, content, sale.SaleID, "")

	if len(targetUIDs) == 0 {
		// Broadcast to all online users via room broadcast
		body, err := json.Marshal(payload)
		if err != nil {
			return sale, fmt.Errorf("marshal flash sale payload: %w", err)
		}
		_, err = s.pushClient.PushToRoom(10, "live", "flash_sale_all", body)
		if err != nil {
			return sale, fmt.Errorf("broadcast flash sale failed: %w", err)
		}
		s.stats.mu.Lock()
		s.stats.TotalPushed++
		s.stats.mu.Unlock()
	} else if len(targetUIDs) <= 100 {
		// Small batch: push to each user individually for reliability
		var wg sync.WaitGroup
		for _, uid := range targetUIDs {
			wg.Add(1)
			go func(uid string) {
				defer wg.Done()
				keys := extractKeysFromMids([]string{uid})
				_, _ = s.pushClient.PushJSONToUser(10, keys, payload)
			}(uid)
		}
		wg.Wait()
		s.stats.mu.Lock()
		s.stats.TotalPushed += int64(len(targetUIDs))
		s.stats.mu.Unlock()
	} else {
		// Large batch: use PushMids for throughput
		intMids := make([]int64, 0, len(targetUIDs))
		for _, uid := range targetUIDs {
			if id, err := strconv.ParseInt(uid, 10, 64); err == nil {
				intMids = append(intMids, id)
			}
		}
		_, err := s.pushClient.PushJSONToUsers(10, intMids, payload)
		if err != nil {
			return sale, fmt.Errorf("batch push flash sale failed: %w", err)
		}
		s.stats.mu.Lock()
		s.stats.TotalPushed += int64(len(targetUIDs))
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
