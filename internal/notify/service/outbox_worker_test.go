package service

import (
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/policy"
)

func TestCompensationStrategyConstants(t *testing.T) {
	strategies := []string{
		string(policy.CompensationRetryThenDLQ),
		string(policy.CompensationRetryThenDrop),
		string(policy.CompensationRetryThenAlert),
		string(policy.CompensationImmediateDLQ),
	}
	expected := []string{"retry_then_dlq", "retry_then_drop", "retry_then_alert", "immediate_dlq"}
	for i, s := range strategies {
		if s != expected[i] {
			t.Errorf("strategy[%d]: expected %q, got %q", i, expected[i], s)
		}
	}
}

func TestCampaignStatusConstants(t *testing.T) {
	statuses := []string{
		model.CampaignDraft,
		model.CampaignActive,
		model.CampaignPaused,
		model.CampaignCompleted,
		model.CampaignCancelled,
	}
	for _, s := range statuses {
		if s == "" {
			t.Error("empty status constant")
		}
	}
}

func TestRateLimiterAllow(t *testing.T) {
	rl := &rateLimiter{
		tokens:     10,
		lastRefill: timeNow(),
		ratePerSec: 10,
	}

	// First 10 should pass
	for i := 0; i < 10; i++ {
		if !rl.allow() {
			t.Errorf("allow() should return true for token %d", i+1)
		}
	}

	// 11th should fail (tokens exhausted)
	if rl.allow() {
		t.Error("allow() should return false when tokens exhausted")
	}
}

func TestRateLimiterZeroRate(t *testing.T) {
	rl := &rateLimiter{
		tokens:     0,
		lastRefill: timeNow(),
		ratePerSec: 0,
	}
	if rl.allow() {
		t.Error("allow() should return false for zero rate")
	}
}

func timeNow() time.Time {
	return time.Now()
}
