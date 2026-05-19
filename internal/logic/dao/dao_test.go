package dao

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/Terry-Mao/goim/internal/logic/conf"
)

var (
	d *Dao
)

func TestMain(m *testing.M) {
	if os.Getenv("GOIM_ENABLE_INFRA_TESTS") != "1" {
		fmt.Println("GOIM_ENABLE_INFRA_TESTS != 1; skipping logic DAO infrastructure tests")
		os.Exit(0)
	}
	if err := flag.Set("conf", "../../../cmd/logic/logic-example.toml"); err != nil {
		panic(err)
	}
	flag.Parse()
	if err := conf.Init(); err != nil {
		panic(err)
	}
	d = New(conf.Conf)
	if err := d.Ping(context.TODO()); err != nil {
		os.Exit(-1)
	}
	if err := d.Close(); err != nil {
		os.Exit(-1)
	}
	if err := d.Ping(context.TODO()); err == nil {
		os.Exit(-1)
	}
	d = New(conf.Conf)
	os.Exit(m.Run())
}
