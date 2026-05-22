package main

import (
	"context"
	"flag"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/Terry-Mao/goim/internal/logic/model"
	routercore "github.com/Terry-Mao/goim/internal/router"
	routerconf "github.com/Terry-Mao/goim/internal/router/conf"
	routergrpc "github.com/Terry-Mao/goim/internal/router/grpc"
	"github.com/Terry-Mao/goim/pkg/ip"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/tracing"
	"github.com/bilibili/discovery/naming"
	resolver "github.com/bilibili/discovery/naming/grpc"
)

const (
	ver   = "2.0.0"
	appid = "goim.router"
)

func main() {
	if f := flag.Lookup("conf"); f != nil && f.Value.String() == "logic-example.toml" {
		_ = flag.Set("conf", "cmd/router/router.toml")
	}
	flag.Parse()
	confPath := "cmd/router/router.toml"
	if f := flag.Lookup("conf"); f != nil {
		confPath = f.Value.String()
	}
	cfg, err := routerconf.Load(confPath)
	if err != nil {
		panic(err)
	}
	log.Init(false)

	tp, err := tracing.InitTracer(context.Background(), "goim-router", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		log.Errorf("tracing init error(%v)", err)
	}
	defer func() {
		if tp != nil {
			_ = tp.Shutdown(context.Background())
		}
	}()

	log.Infof("goim-router [version: %s env: %+v] start", ver, cfg.Env)
	dis := naming.New(cfg.NamingConfig())
	resolver.Register(dis)

	srv := routercore.NewService(cfg, dis)
	rpcSrv := routergrpc.New(cfg.GRPC, srv.Engine())
	cancel := register(dis, cfg)

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		s := <-c
		log.Infof("goim-router get a signal %s", s.String())
		switch s {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			if cancel != nil {
				cancel()
			}
			rpcSrv.GracefulStop()
			srv.Close()
			log.Infof("goim-router [version: %s] exit", ver)
			log.Sync()
			return
		case syscall.SIGHUP:
		default:
			return
		}
	}
}

func register(dis *naming.Discovery, cfg *routerconf.Config) context.CancelFunc {
	env := cfg.Env
	addr := ip.InternalIP()
	_, port, _ := net.SplitHostPort(cfg.GRPC.Addr)
	ins := &naming.Instance{
		Region:   env.Region,
		Zone:     env.Zone,
		Env:      env.DeployEnv,
		Hostname: env.Host,
		AppID:    appid,
		Addrs: []string{
			"grpc://" + addr + ":" + port,
		},
		Metadata: map[string]string{
			model.MetaWeight: strconv.FormatInt(env.Weight, 10),
		},
	}
	cancel, err := dis.Register(ins)
	if err != nil {
		panic(err)
	}
	return cancel
}
