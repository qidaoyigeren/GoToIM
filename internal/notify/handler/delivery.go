package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// HandleListDeliveryMessages handles GET /api/delivery/messages.
func (h *Handler) HandleListDeliveryMessages(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	messages, err := h.orderSvc.ListDeliveryMessages(limit)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": messages})
}

// HandleGetDeliveryMessage handles GET /api/delivery/messages/:id.
func (h *Handler) HandleGetDeliveryMessage(c *gin.Context) {
	detail, err := h.orderSvc.GetDeliveryMessageDetail(c.Param("id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": detail})
}
