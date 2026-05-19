package conf

import "time"

// Config holds the notify server configuration.
type Config struct {
	Listen     string           `toml:"listen"`     // HTTP listen address, e.g. ":3121"
	LogicAddr  string           `toml:"logic_addr"` // goim Logic HTTP address, e.g. "localhost:3111"
	Storage    StorageConfig    `toml:"storage"`
	Outbox     OutboxConfig     `toml:"outbox"`
	Simulation SimulationConfig `toml:"simulation"`
}

// StorageConfig holds Notify Server persistence settings.
type StorageConfig struct {
	Driver string `toml:"driver"`
	DSN    string `toml:"dsn"`
}

// OutboxConfig controls asynchronous notification delivery.
type OutboxConfig struct {
	Enabled      bool          `toml:"enabled"`
	BatchSize    int           `toml:"batch_size"`
	PollInterval time.Duration `toml:"poll_interval"`
	MaxRetries   int64         `toml:"max_retries"`
	LockTTL      time.Duration `toml:"lock_ttl"`
}

// SimulationConfig holds load generator defaults.
type SimulationConfig struct {
	DefaultQPS        int           `toml:"default_qps"`
	FlashSaleUsers    int           `toml:"flash_sale_users"`
	FlashSaleBurstMs  int           `toml:"flash_sale_burst_ms"`
	OrderLifecycleMin time.Duration `toml:"order_lifecycle_min"`
	OrderLifecycleMax time.Duration `toml:"order_lifecycle_max"`
	UserPoolSize      int           `toml:"user_pool_size"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Listen:    ":3121",
		LogicAddr: "localhost:3111",
		Storage: StorageConfig{
			Driver: "sqlite",
			DSN:    "target/notify.db",
		},
		Outbox: OutboxConfig{
			Enabled:      true,
			BatchSize:    100,
			PollInterval: time.Second,
			MaxRetries:   5,
			LockTTL:      30 * time.Second,
		},
		Simulation: SimulationConfig{
			DefaultQPS:        100,
			FlashSaleUsers:    10000,
			FlashSaleBurstMs:  2000,
			OrderLifecycleMin: 3 * time.Second,
			OrderLifecycleMax: 10 * time.Second,
			UserPoolSize:      10000,
		},
	}
}
