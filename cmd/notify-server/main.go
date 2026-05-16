package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/Terry-Mao/goim/internal/notify/conf"

	notify "github.com/Terry-Mao/goim/internal/notify"
)

const ver = "1.0.0"

func main() {
	confPath := flag.String("conf", "notify-example.toml", "config path")
	simulate := flag.String("simulate", "", "auto-start simulation mode: lifecycle|normal|peak|flash_sale")
	simQPS := flag.Int("qps", 100, "simulation QPS")
	simUsers := flag.Int("users", 10000, "simulation user count")
	flag.Parse()

	cfg := conf.DefaultConfig()
	if _, err := os.Stat(*confPath); err == nil {
		if _, err := toml.DecodeFile(*confPath, cfg); err != nil {
			panic(err)
		}
	}

	srv := notify.New(cfg)

	if *simulate != "" {
		if err := srv.StartSimulator(*simulate, *simQPS, *simUsers); err != nil {
			panic(err)
		}
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		s := <-c
		switch s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			srv.Close()
			return
		case syscall.SIGHUP:
		default:
			return
		}
	}
}
