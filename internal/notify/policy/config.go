package policy

import (
	"log"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// CompensationStrategy defines how failures are handled after max retries.
type CompensationStrategy string

const (
	CompensationRetryThenDLQ   CompensationStrategy = "retry_then_dlq"
	CompensationRetryThenDrop  CompensationStrategy = "retry_then_drop"
	CompensationRetryThenAlert CompensationStrategy = "retry_then_alert"
	CompensationImmediateDLQ   CompensationStrategy = "immediate_dlq"
)

// NotificationPolicy is a configurable delivery policy for one event type.
type NotificationPolicy struct {
	Priority             string               `yaml:"priority" json:"priority"`
	TTLSeconds           int                  `yaml:"ttl_seconds" json:"ttl_seconds"`
	AckStrategy          string               `yaml:"ack_strategy" json:"ack_strategy"`
	MaxRetry             int                  `yaml:"max_retry" json:"max_retry"`
	DLQEnabled           bool                 `yaml:"dlq_enabled" json:"dlq_enabled"`
	CompensationStrategy CompensationStrategy `yaml:"compensation_strategy" json:"compensation_strategy"`
	TemplateName         string               `yaml:"template_name" json:"template_name"`
}

// PolicyConfig is the root YAML structure for notification policies.
type PolicyConfig struct {
	NotificationPolicy struct {
		Default NotificationPolicy            `yaml:"default"`
		Events  map[string]NotificationPolicy `yaml:"events"`
	} `yaml:"notification_policy"`
}

// PolicyManager loads, caches, and hot-reloads notification policies.
type PolicyManager struct {
	mu         sync.RWMutex
	config     PolicyConfig
	configPath string
	stopCh     chan struct{}
	builtin    bool
}

var defaultManager *PolicyManager
var defaultManagerOnce sync.Once

// InitPolicyManager initializes the global policy manager.
// configPath is the path to notify_policy.yaml. If empty, uses built-in defaults.
func InitPolicyManager(configPath string) *PolicyManager {
	defaultManagerOnce.Do(func() {
		defaultManager = NewPolicyManager(configPath)
	})
	return defaultManager
}

// GetPolicyManager returns the global policy manager, or nil if not initialized.
func GetPolicyManager() *PolicyManager {
	return defaultManager
}

// NewPolicyManager creates a PolicyManager, loads config, and starts watching.
func NewPolicyManager(configPath string) *PolicyManager {
	pm := &PolicyManager{
		configPath: configPath,
		stopCh:     make(chan struct{}),
	}
	pm.loadConfig()
	pm.startWatcher()
	return pm
}

// loadConfig attempts to load from YAML file; falls back to built-in defaults.
func (pm *PolicyManager) loadConfig() {
	cfg, err := loadPolicyFile(pm.configPath)
	if err != nil {
		log.Printf("[policy] failed to load config file %s: %v — using built-in defaults", pm.configPath, err)
		cfg = builtinPolicyConfig()
		pm.builtin = true
	} else {
		pm.builtin = false
	}
	pm.mu.Lock()
	pm.config = cfg
	pm.mu.Unlock()
	log.Printf("[policy] config loaded (builtin=%v, events=%d)", pm.builtin, len(cfg.NotificationPolicy.Events))
}

// GetPolicy returns the policy for an event type, falling back to default.
func (pm *PolicyManager) GetPolicy(eventType string) NotificationPolicy {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if p, ok := pm.config.NotificationPolicy.Events[eventType]; ok {
		return normalizePolicy(p)
	}
	return normalizePolicy(pm.config.NotificationPolicy.Default)
}

// GetConfig returns a copy of the current policy config (for inspection/debug).
func (pm *PolicyManager) GetConfig() PolicyConfig {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.config
}

// startWatcher polls the config file for changes (hot-reload without fsnotify dependency).
func (pm *PolicyManager) startWatcher() {
	if pm.configPath == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		var lastMod time.Time
		for {
			select {
			case <-ticker.C:
				if fi, err := os.Stat(pm.configPath); err == nil {
					if fi.ModTime().After(lastMod) {
						lastMod = fi.ModTime()
						pm.reloadConfig()
					}
				}
			case <-pm.stopCh:
				return
			}
		}
	}()
}

func (pm *PolicyManager) reloadConfig() {
	cfg, err := loadPolicyFile(pm.configPath)
	if err != nil {
		log.Printf("[policy] reload failed: %v — keeping current config", err)
		return
	}
	pm.mu.Lock()
	pm.config = cfg
	pm.builtin = false
	pm.mu.Unlock()
	log.Printf("[policy] reload success (events=%d)", len(cfg.NotificationPolicy.Events))
}

// Stop stops the file watcher goroutine.
func (pm *PolicyManager) Stop() {
	close(pm.stopCh)
}

// Resolve is the compat bridge — uses the global manager if available, otherwise built-in.
func Resolve(businessType, eventType string) Policy {
	mgr := GetPolicyManager()
	if mgr != nil {
		np := mgr.GetPolicy(businessType + "." + eventType)
		return policyFromNotificationPolicy(businessType, eventType, np)
	}
	return resolveBuiltin(businessType, eventType)
}

// policyFromNotificationPolicy converts a NotificationPolicy to the legacy Policy struct.
func policyFromNotificationPolicy(businessType, eventType string, np NotificationPolicy) Policy {
	ackPolicy := np.AckStrategy
	if ackPolicy == "" {
		ackPolicy = AckAnyDevice
	}
	expectedAck := int64(1)
	if ackPolicy == AckNone || ackPolicy == AckBestEffort {
		expectedAck = 0
	}
	return Policy{
		BusinessType:         businessType,
		EventType:            eventType,
		Priority:             np.Priority,
		TTL:                  time.Duration(np.TTLSeconds) * time.Second,
		AckPolicy:            ackPolicy,
		ExpectedAckCount:     expectedAck,
		MaxRetries:           int64(np.MaxRetry),
		FallbackEnabled:      true,
		DLQEnabled:           np.DLQEnabled,
		CompensationStrategy: np.CompensationStrategy,
		TemplateName:         np.TemplateName,
	}
}

func loadPolicyFile(path string) (PolicyConfig, error) {
	var cfg PolicyConfig
	if path == "" {
		return cfg, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.NotificationPolicy.Default.Priority == "" {
		cfg.NotificationPolicy.Default = builtinDefaultPolicy()
	}
	return cfg, nil
}

func builtinPolicyConfig() PolicyConfig {
	return PolicyConfig{
		NotificationPolicy: struct {
			Default NotificationPolicy            `yaml:"default"`
			Events  map[string]NotificationPolicy `yaml:"events"`
		}{
			Default: builtinDefaultPolicy(),
			Events: map[string]NotificationPolicy{
				"order.created": {
					Priority: "high", TTLSeconds: 300, AckStrategy: AckAnyDevice,
					MaxRetry: 3, DLQEnabled: true, CompensationStrategy: CompensationRetryThenDLQ,
					TemplateName: "order_created_template",
				},
				"order.paid": {
					Priority: "high", TTLSeconds: 300, AckStrategy: AckAnyDevice,
					MaxRetry: 3, DLQEnabled: true, CompensationStrategy: CompensationRetryThenDLQ,
					TemplateName: "order_paid_template",
				},
				"order.shipped": {
					Priority: "normal", TTLSeconds: 1800, AckStrategy: AckBestEffort,
					MaxRetry: 2, DLQEnabled: true, CompensationStrategy: CompensationRetryThenDLQ,
					TemplateName: "order_shipped_template",
				},
				"order.delivered": {
					Priority: "normal", TTLSeconds: 86400, AckStrategy: AckBestEffort,
					MaxRetry: 3, DLQEnabled: true, CompensationStrategy: CompensationRetryThenDLQ,
					TemplateName: "default_notification",
				},
				"order.delivery_failed": {
					Priority: "critical", TTLSeconds: 86400, AckStrategy: AckAnyDevice,
					MaxRetry: 5, DLQEnabled: true, CompensationStrategy: CompensationRetryThenAlert,
					TemplateName: "default_notification",
				},
				"logistics.update": {
					Priority: "normal", TTLSeconds: 86400, AckStrategy: AckBestEffort,
					MaxRetry: 3, DLQEnabled: true, CompensationStrategy: CompensationRetryThenDLQ,
					TemplateName: "default_notification",
				},
				"flash_sale.notify": {
					Priority: "low", TTLSeconds: 60, AckStrategy: AckNone,
					MaxRetry: 1, DLQEnabled: false, CompensationStrategy: CompensationRetryThenDrop,
					TemplateName: "flash_sale_template",
				},
			},
		},
	}
}

func builtinDefaultPolicy() NotificationPolicy {
	return NotificationPolicy{
		Priority:             "normal",
		TTLSeconds:           3600,
		AckStrategy:          AckBestEffort,
		MaxRetry:             3,
		DLQEnabled:           true,
		CompensationStrategy: CompensationRetryThenDLQ,
		TemplateName:         "default_notification",
	}
}

func normalizePolicy(p NotificationPolicy) NotificationPolicy {
	if p.Priority == "" {
		p.Priority = "normal"
	}
	if p.TTLSeconds <= 0 {
		p.TTLSeconds = 3600
	}
	if p.AckStrategy == "" {
		p.AckStrategy = AckBestEffort
	}
	if p.MaxRetry <= 0 {
		p.MaxRetry = 3
	}
	if p.CompensationStrategy == "" {
		p.CompensationStrategy = CompensationRetryThenDLQ
	}
	if p.TemplateName == "" {
		p.TemplateName = "default_notification"
	}
	return p
}
