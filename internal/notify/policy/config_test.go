package policy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPolicyManagerBuiltinDefaults(t *testing.T) {
	pm := NewPolicyManager("")
	defer pm.Stop()

	p := pm.GetPolicy("order.created")
	if p.Priority != "high" {
		t.Errorf("expected priority 'high', got %q", p.Priority)
	}
	if p.MaxRetry != 3 {
		t.Errorf("expected max_retry 3, got %d", p.MaxRetry)
	}
	if p.CompensationStrategy != CompensationRetryThenDLQ {
		t.Errorf("expected retry_then_dlq, got %s", p.CompensationStrategy)
	}
	if p.TemplateName != "order_created_template" {
		t.Errorf("expected order_created_template, got %s", p.TemplateName)
	}
}

func TestPolicyManagerEventTypeHit(t *testing.T) {
	pm := NewPolicyManager("")
	defer pm.Stop()

	p := pm.GetPolicy("flash_sale.notify")
	if p.Priority != "low" {
		t.Errorf("expected priority 'low', got %q", p.Priority)
	}
	if p.CompensationStrategy != CompensationRetryThenDrop {
		t.Errorf("expected retry_then_drop, got %s", p.CompensationStrategy)
	}
	if p.DLQEnabled {
		t.Error("expected dlq_enabled=false for flash_sale")
	}
}

func TestPolicyManagerDefaultFallback(t *testing.T) {
	pm := NewPolicyManager("")
	defer pm.Stop()

	p := pm.GetPolicy("nonexistent.event")
	if p.Priority != "normal" {
		t.Errorf("expected priority 'normal', got %q", p.Priority)
	}
	if p.CompensationStrategy != CompensationRetryThenDLQ {
		t.Errorf("expected retry_then_dlq, got %s", p.CompensationStrategy)
	}
}

func TestResolveUsesPolicyManager(t *testing.T) {
	// Init global manager
	InitPolicyManager("")
	defer GetPolicyManager().Stop()

	p := Resolve("order", "paid")
	if p.Priority != "high" {
		t.Errorf("expected priority 'high', got %q", p.Priority)
	}
	if p.MaxRetries != 3 {
		t.Errorf("expected max_retries 3, got %d", p.MaxRetries)
	}
	if p.CompensationStrategy != CompensationRetryThenDLQ {
		t.Errorf("expected retry_then_dlq, got %s", p.CompensationStrategy)
	}
}

func TestResolveBuiltinFallback(t *testing.T) {
	p := resolveBuiltin("flash_sale", "notify")
	if p.Priority != "low" {
		t.Errorf("expected priority 'low', got %q", p.Priority)
	}
	if p.DLQEnabled {
		t.Error("expected DLQ disabled for flash_sale in builtin")
	}
	if p.CompensationStrategy != CompensationRetryThenDrop {
		t.Errorf("expected retry_then_drop for flash_sale builtin, got %s", p.CompensationStrategy)
	}
}

func TestPolicyManagerLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test_policy.yaml")
	yaml := `
notification_policy:
  default:
    priority: normal
    ttl_seconds: 100
    ack_strategy: best_effort
    max_retry: 2
    dlq_enabled: true
    compensation_strategy: retry_then_dlq
    template_name: default_notification
  events:
    test.event:
      priority: critical
      ttl_seconds: 50
      ack_strategy: all_devices
      max_retry: 10
      dlq_enabled: true
      compensation_strategy: retry_then_alert
      template_name: test_template
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	pm := NewPolicyManager(path)
	defer pm.Stop()

	p := pm.GetPolicy("test.event")
	if p.Priority != "critical" {
		t.Errorf("expected priority 'critical', got %q", p.Priority)
	}
	if p.TTLSeconds != 50 {
		t.Errorf("expected ttl 50, got %d", p.TTLSeconds)
	}
	if p.CompensationStrategy != CompensationRetryThenAlert {
		t.Errorf("expected retry_then_alert, got %s", p.CompensationStrategy)
	}
}

func TestPolicyManagerHotReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test_policy.yaml")
	yaml := `
notification_policy:
  default:
    priority: normal
    ttl_seconds: 100
    ack_strategy: best_effort
    max_retry: 2
    dlq_enabled: true
    compensation_strategy: retry_then_dlq
    template_name: default_notification
  events:
    test.event:
      priority: normal
      ttl_seconds: 100
      ack_strategy: best_effort
      max_retry: 1
      dlq_enabled: true
      compensation_strategy: retry_then_dlq
      template_name: default_notification
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	pm := NewPolicyManager(path)
	defer pm.Stop()

	// Initial load
	p := pm.GetPolicy("test.event")
	if p.MaxRetry != 1 {
		t.Errorf("expected max_retry 1, got %d", p.MaxRetry)
	}

	// Update file
	yaml2 := `
notification_policy:
  default:
    priority: normal
    ttl_seconds: 100
    ack_strategy: best_effort
    max_retry: 2
    dlq_enabled: true
    compensation_strategy: retry_then_dlq
    template_name: default_notification
  events:
    test.event:
      priority: critical
      ttl_seconds: 200
      ack_strategy: any_device
      max_retry: 5
      dlq_enabled: true
      compensation_strategy: retry_then_drop
      template_name: flash_sale_template
`
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(path, []byte(yaml2), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for hot reload (3s poll interval + some buffer)
	time.Sleep(4 * time.Second)

	p = pm.GetPolicy("test.event")
	if p.MaxRetry != 5 {
		t.Errorf("expected max_retry 5 after reload, got %d", p.MaxRetry)
	}
	if p.CompensationStrategy != CompensationRetryThenDrop {
		t.Errorf("expected retry_then_drop after reload, got %s", p.CompensationStrategy)
	}
}

func TestPolicyManagerCorruptFileKeepsOldConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test_policy.yaml")
	yaml := `
notification_policy:
  default:
    priority: normal
    ttl_seconds: 100
    ack_strategy: best_effort
    max_retry: 2
    dlq_enabled: true
    compensation_strategy: retry_then_dlq
    template_name: default_notification
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	pm := NewPolicyManager(path)
	defer pm.Stop()

	p := pm.GetPolicy("default")
	if p.MaxRetry != 2 {
		t.Errorf("expected max_retry 2, got %d", p.MaxRetry)
	}

	// Write corrupt YAML
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(path, []byte("{{{bad yaml!!!"), 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(4 * time.Second)

	// Should keep old config
	p = pm.GetPolicy("default")
	if p.MaxRetry != 2 {
		t.Errorf("expected max_retry 2 (old config kept), got %d", p.MaxRetry)
	}
}

func TestCompensationStrategyConstants(t *testing.T) {
	if CompensationRetryThenDLQ != "retry_then_dlq" {
		t.Error("constant mismatch")
	}
	if CompensationRetryThenDrop != "retry_then_drop" {
		t.Error("constant mismatch")
	}
	if CompensationRetryThenAlert != "retry_then_alert" {
		t.Error("constant mismatch")
	}
	if CompensationImmediateDLQ != "immediate_dlq" {
		t.Error("constant mismatch")
	}
}
