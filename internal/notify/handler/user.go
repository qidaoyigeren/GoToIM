package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HandleGetUserNotifications handles GET /api/user/:uid/notifications.
func (h *Handler) HandleGetUserNotifications(c *gin.Context) {
	uid := c.Param("uid")
	notifs := h.orderSvc.GetUserNotifications(uid)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": notifs})
}
