package conf

import "testing"

func TestLoadRouterTOML(t *testing.T) {
	c, err := Load("../../../cmd/router/router.toml")
	if err != nil {
		t.Fatalf("Load router.toml: %v", err)
	}
	if c.GRPC.Addr != ":7172" {
		t.Fatalf("grpc addr = %q, want :7172", c.GRPC.Addr)
	}
	lc := c.LogicConfig()
	if lc.Kafka.OnlinePushTopic == "" || lc.Kafka.OfflinePushTopic == "" {
		t.Fatalf("split push topics not loaded: %+v", lc.Kafka)
	}
	if len(c.NamingConfig().Nodes) == 0 {
		t.Fatal("discovery nodes not loaded")
	}
}
