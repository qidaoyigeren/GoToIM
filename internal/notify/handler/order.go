package handler

import (
	"net/http"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/gin-gonic/gin"
)

// OrderStatusChangeRequest is the request body for POST /api/order/status-change.
type OrderStatusChangeRequest struct {
	OrderID   string            `json:"order_id"`
	NewStatus model.OrderStatus `json:"new_status"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// CreateOrderRequest is the request body for creating a new order.
type CreateOrderRequest struct {
	UserID string            `json:"user_id"`
	Items  []model.OrderItem `json:"items"`
	Total  float64           `json:"total"`
}

// HandleCreateOrder handles POST /api/order/create.
func (h *Handler) HandleCreateOrder(c *gin.Context) {
	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.UserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "user_id is required"})
		return
	}
	if req.Total <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "total must be positive"})
		return
	}
	if len(req.Items) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "items must not be empty"})
		return
	}

	order, notif, err := h.orderSvc.CreateOrder(req.UserID, req.Items, req.Total)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"order": order, "notification": notif}})
}

// HandleOrderStatusChange handles POST /api/order/status-change.
func (h *Handler) HandleOrderStatusChange(c *gin.Context) {
	var req OrderStatusChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.OrderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "order_id is required"})
		return
	}

	order, notif, err := h.orderSvc.ChangeOrderStatus(req.OrderID, req.NewStatus, req.Extra)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"order": order, "notification": notif}})
}

// HandleGetOrder handles GET /api/orders/:order_id.
func (h *Handler) HandleGetOrder(c *gin.Context) {
	orderID := c.Param("order_id")
	order, ok := h.orderSvc.GetOrder(orderID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"code": -404, "message": "order not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": order})
}

// HandleGetUserOrders handles GET /api/orders/:uid.
func (h *Handler) HandleGetUserOrders(c *gin.Context) {
	uid := c.Param("uid")
	orders := h.orderSvc.GetUserOrders(uid)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": orders})
}
