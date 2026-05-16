package notify

import (
	"context"
	"net/http"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/conf"
	"github.com/Terry-Mao/goim/internal/notify/handler"
	"github.com/Terry-Mao/goim/internal/notify/service"
	"github.com/Terry-Mao/goim/internal/notify/simulator"

	"github.com/gin-gonic/gin"
)

// Server is the notify HTTP server.
type Server struct {
	engine    *gin.Engine
	srv       *http.Server
	handler   *handler.Handler
	simulator *simulator.Engine
}

// New creates a new Server from config.
func New(cfg *conf.Config) *Server {
	engine := gin.New()
	engine.Use(gin.Logger(), gin.Recovery())

	pushClient := service.NewPushClient(cfg.LogicAddr)
	orderSvc := service.NewOrderNotifyService(pushClient)
	flashSaleSvc := service.NewFlashSaleService(pushClient, orderSvc.GetStatsCollector())

	h := handler.New(orderSvc, flashSaleSvc)

	simEngine := simulator.NewEngine(orderSvc, flashSaleSvc, cfg)
	h.SetSimulator(simEngine)

	s := &Server{
		engine:    engine,
		handler:   h,
		simulator: simEngine,
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
	api := s.engine.Group("/api")
	api.POST("/order/create", s.handler.HandleCreateOrder)
	api.POST("/order/status-change", s.handler.HandleOrderStatusChange)
	api.GET("/orders/:order_id", s.handler.HandleGetOrder)
	api.GET("/orders/user/:uid", s.handler.HandleGetUserOrders)
	api.POST("/flash-sale/notify", s.handler.HandleCreateFlashSale)
	api.POST("/logistics/update", s.handler.HandleLogistisUpdate)
	api.GET("/user/:uid/notifications", s.handler.HandleGetUserNotifications)
	api.GET("/platform/stats", s.handler.HandleGetPlatformStats)
	api.POST("/simulate/start", s.handler.HandleSimulateStart)
	api.POST("/simulate/stop", s.handler.HandleSimulateStop)
	api.GET("/simulate/status", s.handler.HandleSimulateStatus)
}

// Close gracefully shuts down the server.
func (s *Server) Close() {
	if s.simulator != nil {
		s.simulator.Stop()
	}
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.srv.Shutdown(ctx)
	}
}

// StartSimulator starts the load generator. Call this after server is running.
func (s *Server) StartSimulator(mode string, qps, users int) error {
	return s.simulator.Start(mode, qps, users)
}
