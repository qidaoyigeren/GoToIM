package router

import (
	"context"
	"strconv"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	"github.com/Terry-Mao/goim/internal/logic/model"
	"github.com/Terry-Mao/goim/internal/logic/service"
	routerconf "github.com/Terry-Mao/goim/internal/router/conf"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/snowflake"
	"github.com/bilibili/discovery/naming"
)

// Service owns the standalone Router process dependencies.
type Service struct {
	c            *routerconf.Config
	dis          *naming.Discovery
	dao          *dao.Dao
	sessionMgr   *service.SessionManager
	onlineRouter *service.OnlineRouter
	cometPusher  *CometPusherPool
	engine       *DispatchEngine
	spoolReplay  *SpoolReplayWorker
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewService initializes the standalone Router process.
func NewService(c *routerconf.Config, dis *naming.Discovery) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	d := dao.New(c.LogicConfig())
	onlineRouter := service.NewOnlineRouter()
	sessionMgr := service.NewSessionManager(d, time.Duration(c.Redis.Expire))
	sessionMgr.SetOnlineRouter(onlineRouter)
	cometPusher := NewCometPusher()

	engine := NewDispatchEngine(d, d, sessionMgr, cometPusher)
	engine.SetOnlineRouter(onlineRouter)
	engine.SetBroadcastFallback(cometPusher)
	if c.Snowflake != nil {
		idGen, err := snowflake.New(c.Snowflake.MachineID)
		if err != nil {
			panic(err)
		}
		engine.SetIDGenerator(idGen)
	}
	if c.Limiter != nil && c.Limiter.Rate > 0 && c.Limiter.Burst > 0 {
		engine.SetRateLimiter(NewRateLimiter(c.Limiter.Rate, c.Limiter.Burst))
	}
	if d.MQProducer() != nil {
		engine.SetMQProducer(d.MQProducer())
		if lc := c.LogicConfig(); lc.Kafka != nil {
			engine.SetPushTopics(lc.Kafka.OnlinePushTopic, lc.Kafka.OfflinePushTopic)
		}
	}

	s := &Service{
		c:            c,
		dis:          dis,
		dao:          d,
		sessionMgr:   sessionMgr,
		onlineRouter: onlineRouter,
		cometPusher:  cometPusher,
		engine:       engine,
		ctx:          ctx,
		cancel:       cancel,
	}
	if d.MQProducer() != nil {
		s.spoolReplay = NewSpoolReplayWorker(d, d.MQProducer(), SpoolReplayConfig{})
		s.spoolReplay.Start()
	}
	s.initCometNodes()
	return s
}

// Engine returns the router dispatch engine used by the gRPC adapter.
func (s *Service) Engine() *DispatchEngine {
	return s.engine
}

// Close releases Router process resources.
func (s *Service) Close() {
	s.cancel()
	if s.spoolReplay != nil {
		s.spoolReplay.Stop()
	}
	if s.cometPusher != nil {
		s.cometPusher.Close()
	}
	if s.dao != nil {
		_ = s.dao.Close()
	}
}

func (s *Service) initCometNodes() {
	if s.dis == nil {
		return
	}
	res := s.dis.Build("goim.comet")
	event := res.Watch()
	select {
	case _, ok := <-event:
		if ok {
			s.newCometNodes(res)
		} else {
			log.Error("router discovery watch for goim.comet failed")
		}
	case <-time.After(10 * time.Second):
		log.Error("router discovery goim.comet start timeout")
	}
	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			case _, ok := <-event:
				if !ok {
					return
				}
				s.newCometNodes(res)
			}
		}
	}()
}

func (s *Service) newCometNodes(res naming.Resolver) {
	if zoneIns, ok := res.Fetch(); ok {
		var nodes []*naming.Instance
		for _, zins := range zoneIns.Instances {
			for _, ins := range zins {
				if ins == nil {
					continue
				}
				if ins.Metadata != nil {
					offline, err := strconv.ParseBool(ins.Metadata[model.MetaOffline])
					if err == nil && offline {
						continue
					}
				}
				nodes = append(nodes, ins)
			}
		}
		s.cometPusher.UpdateNodes(nodes)
	}
}
