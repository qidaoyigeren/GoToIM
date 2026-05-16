package handler

import (
	"net/http"

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
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"message": "simulation started", "mode": req.Mode, "qps": req.QPS}})
}

// HandleSimulateStop handles POST /api/simulate/stop.
func (h *Handler) HandleSimulateStop(c *gin.Context) {
	if h.simulator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": -500, "message": "simulator not available"})
		return
	}
	h.simulator.Stop()
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"message": "simulation stopped"}})
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
