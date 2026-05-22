package conf

import (
	"os"
	"strconv"
	"time"

	logicconf "github.com/Terry-Mao/goim/internal/logic/conf"
	xtime "github.com/Terry-Mao/goim/pkg/time"
	"github.com/bilibili/discovery/naming"

	"github.com/BurntSushi/toml"
)

// Config is the standalone Router process configuration.
type Config struct {
	Env       *logicconf.Env
	Discovery *Discovery
	GRPC      *logicconf.RPCServer
	Redis     *logicconf.Redis
	Kafka     *Kafka
	MQ        *logicconf.MQ
	Snowflake *logicconf.Snowflake
	Limiter   *Limiter
}

// Discovery accepts both nodes (used by existing configs) and addrs (router prompt).
type Discovery struct {
	Nodes  []string
	Addrs  []string
	Region string
	Zone   string
	Env    string
	Host   string
}

// Kafka config supports both existing camelCase names and the router prompt snake_case names.
type Kafka struct {
	Topic            string
	Brokers          []string
	PushTopic        string `toml:"pushTopic"`
	OnlinePushTopic  string `toml:"onlinePushTopic"`
	OfflinePushTopic string `toml:"offlinePushTopic"`
	RoomTopic        string `toml:"roomTopic"`
	AllTopic         string `toml:"allTopic"`
	ACKTopic         string `toml:"ackTopic"`

	OnlinePushTopicSnake  string `toml:"online_push_topic"`
	OfflinePushTopicSnake string `toml:"offline_push_topic"`
	RoomTopicSnake        string `toml:"room_topic"`
	AllTopicSnake         string `toml:"all_topic"`
	ACKTopicSnake         string `toml:"ack_topic"`
}

// Limiter controls the Router entry token bucket.
type Limiter struct {
	Rate  float64
	Burst float64
}

// Load reads a Router config file.
func Load(path string) (*Config, error) {
	c := Default()
	_, err := toml.DecodeFile(path, c)
	if err != nil {
		return nil, err
	}
	c.normalize()
	return c, nil
}

// Default returns a runnable local Router configuration.
func Default() *Config {
	host, _ := os.Hostname()
	weight, _ := strconv.ParseInt(os.Getenv("WEIGHT"), 10, 64)
	return &Config{
		Env: &logicconf.Env{
			Region:    os.Getenv("REGION"),
			Zone:      os.Getenv("ZONE"),
			DeployEnv: os.Getenv("DEPLOY_ENV"),
			Host:      host,
			Weight:    weight,
		},
		Discovery: &Discovery{
			Nodes:  []string{"127.0.0.1:7171"},
			Region: os.Getenv("REGION"),
			Zone:   os.Getenv("ZONE"),
			Env:    os.Getenv("DEPLOY_ENV"),
			Host:   host,
		},
		GRPC: &logicconf.RPCServer{
			Network:           "tcp",
			Addr:              ":7172",
			Timeout:           xtime.Duration(time.Second),
			IdleTimeout:       xtime.Duration(time.Minute),
			MaxLifeTime:       xtime.Duration(2 * time.Hour),
			ForceCloseWait:    xtime.Duration(20 * time.Second),
			KeepAliveInterval: xtime.Duration(time.Minute),
			KeepAliveTimeout:  xtime.Duration(20 * time.Second),
		},
		Redis: &logicconf.Redis{
			Network:      "tcp",
			Addr:         "127.0.0.1:6379",
			Active:       60000,
			Idle:         1024,
			DialTimeout:  xtime.Duration(200 * time.Millisecond),
			ReadTimeout:  xtime.Duration(500 * time.Millisecond),
			WriteTimeout: xtime.Duration(500 * time.Millisecond),
			IdleTimeout:  xtime.Duration(120 * time.Second),
			Expire:       xtime.Duration(24 * time.Hour),
		},
		Kafka: &Kafka{
			Topic:            "goim-push-topic",
			Brokers:          []string{"127.0.0.1:9092"},
			PushTopic:        "goim-push-topic",
			OnlinePushTopic:  "goim-online-push-topic",
			OfflinePushTopic: "goim-offline-push-topic",
			RoomTopic:        "goim-room-topic",
			AllTopic:         "goim-all-topic",
			ACKTopic:         "goim-ack-topic",
		},
		Snowflake: &logicconf.Snowflake{MachineID: 1},
		Limiter:   &Limiter{Rate: 10000, Burst: 20000},
	}
}

// NamingConfig converts Router discovery config into the bilibili/discovery type.
func (c *Config) NamingConfig() *naming.Config {
	if c == nil || c.Discovery == nil {
		return &naming.Config{}
	}
	nodes := c.Discovery.Nodes
	if len(nodes) == 0 {
		nodes = c.Discovery.Addrs
	}
	return &naming.Config{
		Nodes:  nodes,
		Region: c.Discovery.Region,
		Zone:   c.Discovery.Zone,
		Env:    c.Discovery.Env,
		Host:   c.Discovery.Host,
	}
}

// LogicConfig adapts Router config to the existing DAO constructor contract.
func (c *Config) LogicConfig() *logicconf.Config {
	k := c.logicKafka()
	return &logicconf.Config{
		Env:       c.Env,
		Discovery: c.NamingConfig(),
		RPCServer: c.GRPC,
		Redis:     c.Redis,
		Kafka:     k,
		MQ: &logicconf.MQ{
			Type:      "kafka",
			Brokers:   k.Brokers,
			PushTopic: k.PushTopic,
			RoomTopic: k.RoomTopic,
			AllTopic:  k.AllTopic,
			ACKTopic:  k.ACKTopic,
		},
		Snowflake: c.Snowflake,
	}
}

func (c *Config) logicKafka() *logicconf.Kafka {
	if c == nil || c.Kafka == nil {
		return &logicconf.Kafka{}
	}
	k := *c.Kafka
	if k.OnlinePushTopic == "" {
		k.OnlinePushTopic = k.OnlinePushTopicSnake
	}
	if k.OfflinePushTopic == "" {
		k.OfflinePushTopic = k.OfflinePushTopicSnake
	}
	if k.RoomTopic == "" {
		k.RoomTopic = k.RoomTopicSnake
	}
	if k.AllTopic == "" {
		k.AllTopic = k.AllTopicSnake
	}
	if k.ACKTopic == "" {
		k.ACKTopic = k.ACKTopicSnake
	}
	pushTopic := k.PushTopic
	if pushTopic == "" {
		pushTopic = k.OnlinePushTopic
	}
	if pushTopic == "" {
		pushTopic = k.OfflinePushTopic
	}
	if k.Topic == "" {
		k.Topic = pushTopic
	}
	return &logicconf.Kafka{
		Topic:            k.Topic,
		Brokers:          k.Brokers,
		PushTopic:        pushTopic,
		OnlinePushTopic:  k.OnlinePushTopic,
		OfflinePushTopic: k.OfflinePushTopic,
		RoomTopic:        k.RoomTopic,
		AllTopic:         k.AllTopic,
		ACKTopic:         k.ACKTopic,
	}
}

func (c *Config) normalize() {
	if c.Env == nil {
		c.Env = Default().Env
	}
	if c.Discovery == nil {
		c.Discovery = Default().Discovery
	}
	if c.GRPC == nil {
		c.GRPC = Default().GRPC
	}
	if c.Redis == nil {
		c.Redis = Default().Redis
	}
	if c.Kafka == nil {
		c.Kafka = Default().Kafka
	}
	if c.Snowflake == nil {
		c.Snowflake = Default().Snowflake
	}
	if c.Limiter == nil {
		c.Limiter = Default().Limiter
	}
}
