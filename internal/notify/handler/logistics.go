package handler

import (
	"fmt"
	"net/http"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/gin-gonic/gin"
)

// LogistisUpdateRequest is the request body for POST /api/logistics/update.
type LogistisUpdateRequest struct {
	OrderID        string `json:"order_id" binding:"required"`
	Location       string `json:"location"`
	Status         string `json:"status"`
	Desc           string `json:"desc"`
	Description    string `json:"description,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// HandleLogistisUpdate handles POST /api/logistics/update.
func (h *Handler) HandleLogistisUpdate(c *gin.Context) {
	var req LogistisUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}

	order, ok := h.orderSvc.GetOrder(req.OrderID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"code": -404, "message": "order not found"})
		return
	}

	desc := req.Desc
	if desc == "" {
		desc = req.Description
	}
	title, content := buildLogisticsMsg(req.OrderID, req.Location, desc)
	notif, err := h.orderSvc.SendCustomNotificationIdempotent(order.UserID, model.NotifyLogistics, req.OrderID, title, content, idempotencyKey(c, req.IdempotencyKey))
	if err != nil {
		writeServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": notif})
}

func buildLogisticsMsg(orderID, location, desc string) (string, string) {
	if location == "" {
		location = "配送处理中"
	}
	if desc == "" {
		desc = "订单物流节点已更新"
	}
	return "物流更新", fmt.Sprintf("订单 %s - %s：%s", orderID, location, desc)
}
