package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

type fakeCampaignBatchStore struct {
	mu           sync.Mutex
	campaign     *model.Campaign
	batches      []*model.CampaignAudienceBatch
	targets      map[string][]*model.CampaignAudienceTarget
	resultStatus string
	success      int64
	failed       int64
	expired      int
}

func (s *fakeCampaignBatchStore) ClaimCampaignAudienceBatches(workerID string, batchSize int, lockTTL time.Duration) ([]*model.CampaignAudienceBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.CampaignAudienceBatch
	for _, batch := range s.batches {
		if batch.Status != "pending" && batch.Status != "retrying" {
			continue
		}
		batch.Status = "processing"
		out = append(out, batch)
		if len(out) == batchSize {
			break
		}
	}
	return out, nil
}

func (s *fakeCampaignBatchStore) GetCampaign(string) (*model.Campaign, error) {
	if s.campaign == nil {
		return nil, errors.New("missing campaign")
	}
	return s.campaign, nil
}

func (s *fakeCampaignBatchStore) ListCampaignAudienceTargetsByBatch(batchID string, statuses []string, limit int) ([]*model.CampaignAudienceTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	statusSet := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		statusSet[status] = struct{}{}
	}
	var out []*model.CampaignAudienceTarget
	for _, target := range s.targets[batchID] {
		if len(statusSet) > 0 {
			if _, ok := statusSet[target.Status]; !ok {
				continue
			}
		}
		out = append(out, target)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *fakeCampaignBatchStore) MarkCampaignAudienceTargetFailed(audienceID, userID, lastError string) error {
	return nil
}

func (s *fakeCampaignBatchStore) MarkCampaignAudienceTargetExpired(audienceID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expired++
	return nil
}

func (s *fakeCampaignBatchStore) MarkCampaignAudienceBatchResult(batchID, workerID, status, lastError string, successCount, failedCount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resultStatus = status
	s.success += successCount
	s.failed += failedCount
	return nil
}

type fakeCampaignTargetCreator struct {
	mu    sync.Mutex
	calls int
}

func (c *fakeCampaignTargetCreator) CreateCampaignAudienceTargetNotification(target *model.CampaignAudienceTarget, title, content string) (*model.Notification, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return &model.Notification{NotifyID: "ntf-" + target.UserID, UserID: target.UserID}, nil
}

func TestCampaignBatchGeneratorProcessesPendingTargets(t *testing.T) {
	store := &fakeCampaignBatchStore{
		campaign: &model.Campaign{
			CampaignID: "camp-1",
			Title:      "promo",
			Status:     model.CampaignActive,
			CreatedAt:  time.Now(),
		},
		batches: []*model.CampaignAudienceBatch{{BatchID: "batch-1", CampaignID: "camp-1", Status: "pending"}},
		targets: map[string][]*model.CampaignAudienceTarget{
			"batch-1": {
				{AudienceID: "aud-1", CampaignID: "camp-1", BatchID: "batch-1", UserID: "1001", Status: "pending"},
				{AudienceID: "aud-1", CampaignID: "camp-1", BatchID: "batch-1", UserID: "1002", Status: "pending"},
			},
		},
	}
	creator := &fakeCampaignTargetCreator{}
	generator := NewCampaignBatchGenerator(store, creator, CampaignBatchGeneratorConfig{
		Enabled:         true,
		BatchSize:       1,
		TargetBatchSize: 10,
	})

	if err := generator.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned error: %v", err)
	}
	if creator.calls != 2 {
		t.Fatalf("creator calls = %d, want 2", creator.calls)
	}
	if store.resultStatus != "completed" || store.success != 2 || store.failed != 0 {
		t.Fatalf("batch result status=%s success=%d failed=%d, want completed/2/0", store.resultStatus, store.success, store.failed)
	}
}

func TestCampaignBatchGeneratorExpiresOldCampaignTargets(t *testing.T) {
	store := &fakeCampaignBatchStore{
		campaign: &model.Campaign{
			CampaignID: "camp-old",
			Title:      "promo",
			Status:     model.CampaignActive,
			CreatedAt:  time.Now().Add(-24 * time.Hour),
		},
		batches: []*model.CampaignAudienceBatch{{BatchID: "batch-old", CampaignID: "camp-old", Status: "pending"}},
		targets: map[string][]*model.CampaignAudienceTarget{
			"batch-old": {
				{AudienceID: "aud-old", CampaignID: "camp-old", BatchID: "batch-old", UserID: "1001", Status: "pending"},
			},
		},
	}
	creator := &fakeCampaignTargetCreator{}
	generator := NewCampaignBatchGenerator(store, creator, CampaignBatchGeneratorConfig{
		Enabled:         true,
		BatchSize:       1,
		TargetBatchSize: 10,
	})

	if err := generator.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned error: %v", err)
	}
	if creator.calls != 0 {
		t.Fatalf("creator calls = %d, want 0", creator.calls)
	}
	if store.expired != 1 || store.resultStatus != "completed" || store.failed != 1 {
		t.Fatalf("expired=%d status=%s failed=%d, want 1/completed/1", store.expired, store.resultStatus, store.failed)
	}
}
