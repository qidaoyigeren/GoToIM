package http

import (
	"context"
	"net/http"
	"time"

	"github.com/Terry-Mao/goim/internal/logic"
	"github.com/Terry-Mao/goim/internal/logic/conf"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server is http server.
type Server struct {
	engine *gin.Engine
	logic  *logic.Logic
	srv    *http.Server
}

// New new a http server.
func New(c *conf.HTTPServer, l *logic.Logic) *Server {
	engine := gin.New()
	engine.Use(loggerHandler, recoverHandler)
	s := &Server{
		engine: engine,
		logic:  l,
	}
	s.initRouter()

	readTimeout := time.Duration(c.ReadTimeout)
	writeTimeout := time.Duration(c.WriteTimeout)
	if readTimeout == 0 {
		readTimeout = 10 * time.Second
	}
	if writeTimeout == 0 {
		writeTimeout = 10 * time.Second
	}
	s.srv = &http.Server{
		Addr:         c.Addr,
		Handler:      engine,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()
	return s
}

func (s *Server) initRouter() {
	group := s.engine.Group("/goim")
	group.POST("/push/keys", s.pushKeys)
	group.POST("/push/mids", s.pushMids)
	group.POST("/push/room", s.pushRoom)
	group.POST("/push/all", s.pushAll)
	group.GET("/online/top", s.onlineTop)
	group.GET("/online/room", s.onlineRoom)
	group.GET("/online/total", s.onlineTotal)
	group.GET("/nodes/weighted", s.nodesWeighted)
	group.GET("/nodes/instances", s.nodesInstances)
	group.GET("/sync", s.syncOffline)
	group.POST("/push/offline", s.pushOffline)
	// Prometheus metrics endpoint
	s.engine.GET("/metrics", gin.WrapH(promhttp.Handler()))
}

// Close gracefully shuts down the HTTP server.
func (s *Server) Close() {
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.srv.Shutdown(ctx)
	}
}
