package notify

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/mq"
	mqkafka "github.com/Terry-Mao/goim/internal/mq/kafka"
	"github.com/Terry-Mao/goim/internal/notify/conf"
	"github.com/Terry-Mao/goim/internal/notify/handler"
	"github.com/Terry-Mao/goim/internal/notify/policy"
	"github.com/Terry-Mao/goim/internal/notify/service"
	"github.com/Terry-Mao/goim/internal/notify/simulator"
	"github.com/Terry-Mao/goim/internal/notify/store"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server is the notify HTTP server.
type Server struct {
	engine       *gin.Engine
	srv          *http.Server
	handler      *handler.Handler
	simulator    *simulator.Engine
	store        *store.SQLStore
	outboxWorker *service.OutboxWorker
	outboxRelay  *service.OutboxRelay
	pushConsumer *service.NotifyPushConsumer
	ackBridge    *service.AckBridgeConsumer
	campaignGen  *service.CampaignBatchGenerator
	mqProducer   mq.Producer
	statsStop    chan struct{}
	statsDone    chan struct{}
	statsOnce    sync.Once
}

// New creates a new Server from config.
func New(cfg *conf.Config) *Server {
	engine := gin.New()
	engine.Use(gin.Logger(), gin.Recovery(), corsMiddleware())

	pushClient := service.NewPushClient(cfg.LogicAddr)
	notifyStore, err := store.Open(cfg.Storage.DSN)
	if err != nil {
		panic(err)
	}

	// Initialize policy manager with hot-reload support
	policy.InitPolicyManager("configs/notify_policy.yaml")
	log.Println("[server] policy manager initialized")

	// Seed default notification templates
	if err := notifyStore.SeedDefaultTemplates(); err != nil {
		log.Printf("[server] seed templates warning: %v", err)
	} else {
		log.Println("[server] default templates seeded")
	}

	orderSvc := service.NewOrderNotifyServiceWithStore(pushClient, notifyStore)
	chatSvc := service.NewChatService(notifyStore, pushClient)
	flashSaleSvc := service.NewFlashSaleService(pushClient, orderSvc.GetStatsCollector())
	flashSaleSvc.SetOrderService(orderSvc)
	flashSaleSvc.SetDefaultRateLimit(cfg.Campaign.DefaultRateLimit)
	flashSaleSvc.SetBatchGeneration(cfg.Campaign.AsyncFanoutThreshold, cfg.Campaign.AudienceBatchSize)
	campaignGen := service.NewCampaignBatchGenerator(notifyStore, orderSvc, service.CampaignBatchGeneratorConfig{
		Enabled:          cfg.Campaign.BatchGeneratorEnabled,
		BatchSize:        cfg.Campaign.BatchGeneratorBatchSize,
		WorkerCount:      cfg.Campaign.BatchGeneratorWorkerCount,
		TargetBatchSize:  cfg.Campaign.BatchGeneratorTargetBatchSize,
		PollInterval:     cfg.Campaign.BatchGeneratorPollInterval,
		LockTTL:          cfg.Campaign.BatchGeneratorLockTTL,
		DefaultRateLimit: cfg.Campaign.DefaultRateLimit,
	})
	campaignGen.Start()
	var (
		outboxWorker *service.OutboxWorker
		outboxRelay  *service.OutboxRelay
		pushConsumer *service.NotifyPushConsumer
		ackBridge    *service.AckBridgeConsumer
		mqProducer   mq.Producer
	)
	if cfg.MQ.Enabled {
		producer, err := mqkafka.NewProducer(cfg.MQ.Brokers, cfg.MQ.NotifyTopic, "", "", cfg.MQ.ACKTopic, cfg.MQ.NotifyTopic)
		if err != nil {
			panic(err)
		}
		mqProducer = producer
		outboxRelay = service.NewOutboxRelay(notifyStore, service.NewNotifyEventProducer(producer, cfg.MQ.NotifyTopic), service.OutboxRelayConfig{
			Enabled:      cfg.Relay.Enabled,
			BatchSize:    cfg.Relay.BatchSize,
			WorkerCount:  cfg.Relay.WorkerCount,
			PollInterval: cfg.Relay.PollInterval,
			MaxRetries:   cfg.Relay.MaxRetries,
			LockTTL:      cfg.Relay.LockTTL,
		})
		outboxRelay.Start()
		pushConsumer = service.NewNotifyPushConsumer(notifyStore, pushClient, func() (mq.Consumer, error) {
			return mqkafka.NewConsumer(cfg.MQ.Brokers, nonEmptyConfig(cfg.MQ.ConsumerGroup, "goim-notify-push"), []string{cfg.MQ.NotifyTopic})
		}, service.NotifyPushConsumerConfig{
			Enabled:      cfg.PushConsumer.Enabled,
			WorkerCount:  cfg.PushConsumer.WorkerCount,
			MaxInflight:  cfg.PushConsumer.MaxInflight,
			BatchSize:    cfg.PushConsumer.BatchSize,
			PollInterval: cfg.PushConsumer.PollInterval,
			MaxRetries:   cfg.PushConsumer.MaxRetries,
			Backoff:      cfg.PushConsumer.Backoff,
		})
		if err := pushConsumer.Start(); err != nil {
			panic(err)
		}
		ackConsumer, err := service.NewAckBridgeConsumer(orderSvc, service.AckBridgeConfig{
			Brokers:       cfg.MQ.Brokers,
			Topic:         cfg.MQ.ACKTopic,
			GroupID:       nonEmptyConfig(cfg.MQ.ConsumerGroup, "goim-notify-push") + "-ack",
			BatchSize:     cfg.PushConsumer.BatchSize,
			FlushInterval: cfg.PushConsumer.PollInterval,
		})
		if err != nil {
			panic(err)
		}
		if ackConsumer != nil {
			ackBridge = ackConsumer
			ackBridge.Start()
		}
		log.Printf("[server] notify MQ mode enabled topic=%s group=%s", cfg.MQ.NotifyTopic, cfg.MQ.ConsumerGroup)
	} else {
		outboxWorker = service.NewOutboxWorker(orderSvc, service.OutboxWorkerConfig{
			Enabled:      cfg.Outbox.Enabled,
			BatchSize:    cfg.Outbox.BatchSize,
			PollInterval: cfg.Outbox.PollInterval,
			MaxRetries:   cfg.Outbox.MaxRetries,
			LockTTL:      cfg.Outbox.LockTTL,
		})
		outboxWorker.Start()
		log.Println("[server] notify legacy direct outbox mode enabled")
	}

	h := handler.New(orderSvc, flashSaleSvc)
	h.SetChatService(chatSvc)

	statsStop, statsDone := startStatsRefresher(orderSvc, 3*time.Second)

	simEngine := simulator.NewEngine(orderSvc, flashSaleSvc, cfg)
	h.SetSimulator(simEngine)

	s := &Server{
		engine:       engine,
		handler:      h,
		simulator:    simEngine,
		store:        notifyStore,
		outboxWorker: outboxWorker,
		outboxRelay:  outboxRelay,
		pushConsumer: pushConsumer,
		ackBridge:    ackBridge,
		campaignGen:  campaignGen,
		mqProducer:   mqProducer,
		statsStop:    statsStop,
		statsDone:    statsDone,
	}

	s.initRouter()

	s.srv = &http.Server{
		Addr:         cfg.Listen,
		Handler:      engine,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()

	return s
}

func (s *Server) initRouter() {
	// Prometheus metrics — independent of /api group
	s.engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	api := s.engine.Group("/api")
	api.GET("/market/merchants", s.handler.HandleListMerchants)
	api.GET("/market/products", s.handler.HandleListProducts)
	api.GET("/market/groups", s.handler.HandleListMerchantGroups)
	api.POST("/purchase-orders", s.handler.HandleCreatePurchaseOrder)
	api.GET("/purchase-orders/:order_id", s.handler.HandleGetPurchaseOrder)
	api.GET("/purchase-orders/user/:uid", s.handler.HandleGetUserPurchaseOrders)
	api.POST("/order/create", s.handler.HandleCreateOrder)
	api.POST("/order/status-change", s.handler.HandleOrderStatusChange)
	api.GET("/orders/:order_id", s.handler.HandleGetOrder)
	api.GET("/orders/:order_id/timeline", s.handler.HandleGetOrderTimeline)
	api.GET("/orders/user/:uid", s.handler.HandleGetUserOrders)
	api.POST("/flash-sale/notify", s.handler.HandleCreateFlashSale)
	api.POST("/logistics/update", s.handler.HandleLogistisUpdate)
	api.GET("/user/:uid/notifications", s.handler.HandleGetUserNotifications)
	api.GET("/notifications/:notify_id/attempts", s.handler.HandleListAttempts)
	api.GET("/notifications/:notify_id/trace", s.handler.HandleNotificationTrace)
	api.GET("/delivery/messages", s.handler.HandleListDeliveryMessages)
	api.GET("/delivery/messages/:id", s.handler.HandleGetDeliveryMessage)
	api.GET("/platform/stats", s.handler.HandleGetPlatformStats)
	api.GET("/platform/sla", s.handler.HandleGetBusinessSLA)
	api.POST("/simulate/start", s.handler.HandleSimulateStart)
	api.POST("/simulate/stop", s.handler.HandleSimulateStop)
	api.GET("/simulate/status", s.handler.HandleSimulateStatus)
	api.POST("/scenarios", s.handler.HandleCreateScenario)
	api.GET("/scenarios/:id", s.handler.HandleGetScenario)
	api.POST("/scenarios/:id/stop", s.handler.HandleStopScenario)
	api.GET("/scenarios/:id/events", s.handler.HandleScenarioEvents)
	api.GET("/dlq", s.handler.HandleListDLQ)
	api.GET("/dlq/:id", s.handler.HandleGetDLQ)
	api.GET("/dlq/:id/audits", s.handler.HandleDLQAudits)
	api.POST("/dlq/bulk/replay", s.handler.HandleBulkReplayDLQ)
	api.POST("/dlq/bulk/resolve", s.handler.HandleBulkResolveDLQ)
	api.POST("/dlq/:id/replay", s.handler.HandleReplayDLQ)
	api.POST("/dlq/:id/resolve", s.handler.HandleResolveDLQ)
	api.GET("/recovery/audits", s.handler.HandleRecoveryAudits)
	api.GET("/recovery/replay-requests", s.handler.HandleListReplayRequests)
	api.POST("/recovery/replay-requests", s.handler.HandleCreateReplayRequest)
	api.GET("/recovery/replay-requests/:id", s.handler.HandleGetReplayRequest)
	api.PATCH("/recovery/replay-requests/:id/approve", s.handler.HandleApproveReplayRequest)
	api.PATCH("/recovery/replay-requests/:id/reject", s.handler.HandleRejectReplayRequest)
	api.PATCH("/recovery/replay-requests/:id/cancel", s.handler.HandleCancelReplayRequest)
	api.POST("/recovery/replay-requests/:id/execute", s.handler.HandleExecuteReplayRequest)
	api.POST("/ack", s.handler.HandleACK)
	api.POST("/chat/conversations", s.handler.HandleCreateChatConversation)
	api.GET("/chat/conversations", s.handler.HandleListChatConversations)
	api.GET("/chat/conversations/:id/messages", s.handler.HandleListChatMessages)
	api.POST("/chat/conversations/:id/messages", s.handler.HandleSendChatMessage)
	api.PATCH("/chat/messages/:id/status", s.handler.HandleUpdateChatMessageStatus)
	api.POST("/chat/groups/:room_id/join", s.handler.HandleJoinChatGroup)

	// Phase 3: Campaign lifecycle
	api.GET("/campaigns/:id", s.handler.HandleGetCampaign)
	api.POST("/campaigns/:id/audience/import", s.handler.HandleImportCampaignAudience)
	api.GET("/campaigns/:id/audiences/:audience_id/targets", s.handler.HandleListCampaignAudienceTargets)
	api.GET("/campaigns/:id/audiences/:audience_id/batches", s.handler.HandleListCampaignAudienceBatches)
	api.POST("/campaigns/:id/audiences/:audience_id/batches/:batch_id/retry", s.handler.HandleRetryCampaignAudienceBatch)
	api.PATCH("/campaigns/:id/pause", s.handler.HandlePauseCampaign)
	api.PATCH("/campaigns/:id/resume", s.handler.HandleResumeCampaign)
	api.PATCH("/campaigns/:id/cancel", s.handler.HandleCancelCampaign)

	// Phase 3: Scenario report
	api.GET("/scenarios/:id/report", s.handler.HandleScenarioReport)
}

func startStatsRefresher(orderSvc *service.OrderNotifyService, interval time.Duration) (chan struct{}, chan struct{}) {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		orderSvc.RefreshRealtimeStats()
		for {
			select {
			case <-ticker.C:
				orderSvc.RefreshRealtimeStats()
			case <-stop:
				return
			}
		}
	}()
	return stop, done
}

// Close gracefully shuts down the server.
func (s *Server) Close() {
	if s.simulator != nil {
		s.simulator.Stop()
	}
	if s.outboxWorker != nil {
		s.outboxWorker.Stop()
	}
	if s.outboxRelay != nil {
		s.outboxRelay.Stop()
	}
	if s.pushConsumer != nil {
		s.pushConsumer.Stop()
	}
	if s.ackBridge != nil {
		s.ackBridge.Stop()
	}
	if s.campaignGen != nil {
		s.campaignGen.Stop()
	}
	if s.mqProducer != nil {
		_ = s.mqProducer.Close()
	}
	s.statsOnce.Do(func() {
		if s.statsStop != nil {
			close(s.statsStop)
			<-s.statsDone
		}
	})
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.srv.Shutdown(ctx)
	}
	if s.store != nil {
		_ = s.store.Close()
	}
}

// StartSimulator starts the load generator. Call this after server is running.
func (s *Server) StartSimulator(mode string, qps, users int) error {
	return s.simulator.Start(mode, qps, users)
}

func nonEmptyConfig(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
