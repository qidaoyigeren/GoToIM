package logic

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/conf"
)

var (
	lg *Logic
)

func TestMain(m *testing.M) {
	if err := flag.Set("conf", "../../cmd/logic/logic-example.toml"); err != nil {
		panic(err)
	}
	flag.Parse()
	if err := conf.Init(); err != nil {
		panic(err)
	}
	// Try to initialize Logic with a timeout.
	// If discovery/Redis/Kafka are not available, lg stays nil and dependent tests skip.
	done := make(chan struct{})
	go func() {
		defer close(done)
		lg = New(conf.Conf)
	}()
	select {
	case <-done:
		if lg != nil {
			if err := lg.Ping(context.TODO()); err != nil {
				lg = nil
			}
		}
	case <-time.After(15 * time.Second):
		// Infrastructure not available; mock-based tests can still run
		lg = nil
	}
	os.Exit(m.Run())
}
