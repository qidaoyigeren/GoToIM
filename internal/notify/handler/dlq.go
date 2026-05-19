package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HandleListDLQ returns unresolved and recent DLQ notifications.
func (h *Handler) HandleListDLQ(c *gin.Context) {
	items, err := h.orderSvc.ListDLQ()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": items})
}

// HandleGetDLQ returns one DLQ record.
func (h *Handler) HandleGetDLQ(c *gin.Context) {
	item, err := h.orderSvc.GetDLQ(c.Param("id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": item})
}

// HandleReplayDLQ requeues a DLQ item through the outbox.
func (h *Handler) HandleReplayDLQ(c *gin.Context) {
	var req struct {
		ResolvedBy string `json:"resolved_by,omitempty"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.ResolvedBy == "" {
		req.ResolvedBy = "api"
	}
	if err := h.orderSvc.ReplayDLQ(c.Param("id"), req.ResolvedBy); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"replayed": true}})
}

// HandleResolveDLQ marks a DLQ item resolved without replay.
func (h *Handler) HandleResolveDLQ(c *gin.Context) {
	var req struct {
		ResolvedBy string `json:"resolved_by,omitempty"`
		Resolution string `json:"resolution,omitempty"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.ResolvedBy == "" {
		req.ResolvedBy = "api"
	}
	if req.Resolution == "" {
		req.Resolution = "resolved"
	}
	if err := h.orderSvc.ResolveDLQ(c.Param("id"), req.ResolvedBy, req.Resolution); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"resolved": true}})
}
