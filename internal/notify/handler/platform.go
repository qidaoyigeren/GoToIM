package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/service"
	"github.com/gin-gonic/gin"
)

// SimulateRequest is the request body for POST /api/simulate/start.
type SimulateRequest struct {
	Mode  string `json:"mode"`  // "lifecycle", "normal", "peak", "flash_sale"
	QPS   int    `json:"qps"`   // requests per second
	Users int    `json:"users"` // number of users for flash sale
}

// HandleGetPlatformStats handles GET /api/platform/stats.
func (h *Handler) HandleGetPlatformStats(c *gin.Context) {
	stats := h.orderSvc.GetStats()
	if h.simulator != nil {
		stats.Simulation = h.simulator.Status()
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": stats})
}

// HandleGetBusinessSLA handles GET /api/platform/sla.
func (h *Handler) HandleGetBusinessSLA(c *gin.Context) {
	window, err := parseWindow(c.DefaultQuery("window", "24h"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	sla, err := h.orderSvc.BusinessSLA(window, c.Query("business_type"), c.Query("delivery_path"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": sla})
}

func parseWindow(value string) (time.Duration, error) {
	if value == "" {
		return 24 * time.Hour, nil
	}
	if d, err := time.ParseDuration(value); err == nil {
		return d, nil
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return 0, strconv.ErrSyntax
}

// HandleSimulateStart handles POST /api/simulate/start.
func (h *Handler) HandleSimulateStart(c *gin.Context) {
	if h.simulator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": -500, "message": "simulator not available"})
		return
	}

	var req SimulateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.Mode == "" {
		req.Mode = "normal"
	}
	if req.QPS <= 0 {
		req.QPS = 100
	}
	if req.Users <= 0 {
		req.Users = 10000
	}

	if err := h.simulator.Start(req.Mode, req.QPS, req.Users); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -500, "message": err.Error()})
		return
	}
	run, _ := h.orderSvc.CreateScenarioRun(req.Mode, req.QPS, req.Users)
	if run != nil {
		h.activeRunID = run.RunID
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"message": "simulation started", "mode": req.Mode, "qps": req.QPS, "run_id": h.activeRunID}})
}

// HandleSimulateStop handles POST /api/simulate/stop.
func (h *Handler) HandleSimulateStop(c *gin.Context) {
	if h.simulator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": -500, "message": "simulator not available"})
		return
	}
	h.simulator.Stop()
	if h.activeRunID != "" {
		_ = h.orderSvc.StopScenarioRun(h.activeRunID)
		h.activeRunID = ""
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"message": "simulation stopped"}})
}

// HandleACK handles POST /api/ack — called by frontend when it receives a push message.
func (h *Handler) HandleACK(c *gin.Context) {
	var req struct {
		NotifyID       string `json:"notify_id"`
		MsgID          string `json:"msg_id,omitempty"`
		DeviceID       string `json:"device_id,omitempty"`
		SessionID      string `json:"session_id,omitempty"`
		IdempotencyKey string `json:"idempotency_key,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.NotifyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "notify_id is required"})
		return
	}
	recorded, err := h.orderSvc.RecordAckIdempotent(service.AckInput{
		NotifyID:       req.NotifyID,
		MsgID:          req.MsgID,
		DeviceID:       req.DeviceID,
		SessionID:      req.SessionID,
		IdempotencyKey: req.IdempotencyKey,
	}, idempotencyKey(c, req.IdempotencyKey))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"recorded": recorded}})
}

// HandleSimulateStatus handles GET /api/simulate/status.
func (h *Handler) HandleSimulateStatus(c *gin.Context) {
	if h.simulator == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"active": false}})
		return
	}
	state := h.simulator.Status()
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": state})
}

// HandleCreateScenario starts a trackable scenario run.
func (h *Handler) HandleCreateScenario(c *gin.Context) {
	var req SimulateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.Mode == "" {
		req.Mode = "normal"
	}
	if req.QPS <= 0 {
		req.QPS = 100
	}
	if req.Users <= 0 {
		req.Users = 10000
	}
	if h.simulator != nil {
		if err := h.simulator.Start(req.Mode, req.QPS, req.Users); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": -500, "message": err.Error()})
			return
		}
	}
	run, err := h.orderSvc.CreateScenarioRun(req.Mode, req.QPS, req.Users)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	h.activeRunID = run.RunID
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": run})
}

// HandleGetScenario returns a persisted scenario run.
func (h *Handler) HandleGetScenario(c *gin.Context) {
	run, err := h.orderSvc.GetScenarioRun(c.Param("id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": run})
}

// HandleStopScenario stops a scenario run.
func (h *Handler) HandleStopScenario(c *gin.Context) {
	if h.simulator != nil {
		h.simulator.Stop()
	}
	runID := c.Param("id")
	if err := h.orderSvc.StopScenarioRun(runID); err != nil {
		writeServiceError(c, err)
		return
	}
	if h.activeRunID == runID {
		h.activeRunID = ""
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"message": "scenario stopped", "run_id": runID}})
}

// HandleScenarioEvents lists events for a scenario run.
func (h *Handler) HandleScenarioEvents(c *gin.Context) {
	events, err := h.orderSvc.ListScenarioEvents(c.Param("id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": events})
}
