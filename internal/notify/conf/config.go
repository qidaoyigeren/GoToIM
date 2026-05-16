package conf

import "time"

// Config holds the notify server configuration.
type Config struct {
	Listen     string           `toml:"listen"`     // HTTP listen address, e.g. ":3121"
	LogicAddr  string           `toml:"logic_addr"` // goim Logic HTTP address, e.g. "localhost:3111"
	Simulation SimulationConfig `toml:"simulation"`
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
