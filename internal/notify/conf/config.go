package conf

import "time"

// Config holds the notify server configuration.
type Config struct {
	Listen       string             `toml:"listen"`     // HTTP listen address, e.g. ":3121"
	LogicAddr    string             `toml:"logic_addr"` // goim Logic HTTP address, e.g. "localhost:3111"
	Storage      StorageConfig      `toml:"storage"`
	Outbox       OutboxConfig       `toml:"outbox"`
	MQ           MQConfig           `toml:"mq"`
	Relay        RelayConfig        `toml:"relay"`
	PushConsumer PushConsumerConfig `toml:"push_consumer"`
	Campaign     CampaignConfig     `toml:"campaign"`
	Simulation   SimulationConfig   `toml:"simulation"`
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

// MQConfig controls Notify Server MQ integration.
type MQConfig struct {
	Enabled       bool     `toml:"enabled"`
	Brokers       []string `toml:"brokers"`
	NotifyTopic   string   `toml:"notify_topic"`
	ACKTopic      string   `toml:"ack_topic"`
	ConsumerGroup string   `toml:"consumer_group"`
}

// RelayConfig controls MySQL outbox to MQ publishing.
type RelayConfig struct {
	Enabled      bool          `toml:"enabled"`
	BatchSize    int           `toml:"batch_size"`
	WorkerCount  int           `toml:"worker_count"`
	PollInterval time.Duration `toml:"poll_interval"`
	MaxRetries   int64         `toml:"max_retries"`
	LockTTL      time.Duration `toml:"lock_ttl"`
}

// PushConsumerConfig controls notify.push consumption.
type PushConsumerConfig struct {
	Enabled      bool          `toml:"enabled"`
	WorkerCount  int           `toml:"worker_count"`
	MaxInflight  int           `toml:"max_inflight"`
	BatchSize    int           `toml:"batch_size"`
	PollInterval time.Duration `toml:"poll_interval"`
	MaxRetries   int64         `toml:"max_retries"`
	Backoff      time.Duration `toml:"backoff"`
}

// CampaignConfig holds campaign-level traffic shaping defaults.
type CampaignConfig struct {
	DefaultRateLimit              int           `toml:"default_rate_limit"`
	BatchGeneratorEnabled         bool          `toml:"batch_generator_enabled"`
	BatchGeneratorWorkerCount     int           `toml:"batch_generator_worker_count"`
	BatchGeneratorBatchSize       int           `toml:"batch_generator_batch_size"`
	BatchGeneratorTargetBatchSize int           `toml:"batch_generator_target_batch_size"`
	BatchGeneratorPollInterval    time.Duration `toml:"batch_generator_poll_interval"`
	BatchGeneratorLockTTL         time.Duration `toml:"batch_generator_lock_ttl"`
	AsyncFanoutThreshold          int           `toml:"async_fanout_threshold"`
	AudienceBatchSize             int           `toml:"audience_batch_size"`
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
			Driver: "mysql",
			DSN:    "goim:goim@tcp(127.0.0.1:3306)/goim_notify?charset=utf8mb4&parseTime=true&loc=Local",
		},
		Outbox: OutboxConfig{
			Enabled:      true,
			BatchSize:    100,
			PollInterval: time.Second,
			MaxRetries:   5,
			LockTTL:      30 * time.Second,
		},
		MQ: MQConfig{
			Enabled:       false,
			Brokers:       []string{"127.0.0.1:9092"},
			NotifyTopic:   "notify.push",
			ACKTopic:      "notify.ack",
			ConsumerGroup: "goim-notify-push",
		},
		Relay: RelayConfig{
			Enabled:      true,
			BatchSize:    100,
			WorkerCount:  4,
			PollInterval: time.Second,
			MaxRetries:   5,
			LockTTL:      30 * time.Second,
		},
		PushConsumer: PushConsumerConfig{
			Enabled:      true,
			WorkerCount:  1,
			MaxInflight:  64,
			BatchSize:    100,
			PollInterval: time.Second,
			MaxRetries:   3,
			Backoff:      time.Second,
		},
		Campaign: CampaignConfig{
			DefaultRateLimit:              1000,
			BatchGeneratorEnabled:         true,
			BatchGeneratorWorkerCount:     2,
			BatchGeneratorBatchSize:       10,
			BatchGeneratorTargetBatchSize: 500,
			BatchGeneratorPollInterval:    time.Second,
			BatchGeneratorLockTTL:         30 * time.Second,
			AsyncFanoutThreshold:          500,
			AudienceBatchSize:             500,
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
