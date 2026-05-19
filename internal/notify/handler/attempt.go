package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HandleListAttempts handles GET /api/notifications/:notify_id/attempts.
func (h *Handler) HandleListAttempts(c *gin.Context) {
	notifyID := c.Param("notify_id")
	attempts, err := h.orderSvc.Store().ListAttemptsByNotifyID(notifyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": attempts})
}

// HandleNotificationTrace handles GET /api/notifications/:notify_id/trace.
func (h *Handler) HandleNotificationTrace(c *gin.Context) {
	trace, err := h.orderSvc.GetNotificationTrace(c.Param("notify_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": trace})
}
