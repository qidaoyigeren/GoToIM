package handler

import (
	"net/http"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/service"
	"github.com/gin-gonic/gin"
)

type purchaseOrderRequest struct {
	UserID          string                           `json:"user_id"`
	MerchantID      string                           `json:"merchant_id"`
	MerchantUID     int64                            `json:"merchant_uid,omitempty"`
	OrderType       model.OrderType                  `json:"order_type,omitempty"`
	Importance      model.OrderImportance            `json:"importance,omitempty"`
	BuyerNote       string                           `json:"buyer_note,omitempty"`
	FulfillmentMode string                           `json:"fulfillment_mode,omitempty"`
	Items           []service.PurchaseOrderItemInput `json:"items"`
	IdempotencyKey  string                           `json:"idempotency_key,omitempty"`
}

// HandleCreatePurchaseOrder handles POST /api/purchase-orders.
func (h *Handler) HandleCreatePurchaseOrder(c *gin.Context) {
	var req purchaseOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	result, err := h.orderSvc.CreatePurchaseOrderIdempotent(service.PurchaseOrderInput{
		UserID:          req.UserID,
		MerchantID:      req.MerchantID,
		MerchantUID:     req.MerchantUID,
		OrderType:       req.OrderType,
		Importance:      req.Importance,
		BuyerNote:       req.BuyerNote,
		FulfillmentMode: req.FulfillmentMode,
		Items:           req.Items,
	}, idempotencyKey(c, req.IdempotencyKey))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": result})
}

// HandleGetPurchaseOrder handles GET /api/purchase-orders/:order_id.
func (h *Handler) HandleGetPurchaseOrder(c *gin.Context) {
	h.HandleGetOrder(c)
}

// HandleGetUserPurchaseOrders handles GET /api/purchase-orders/user/:uid.
func (h *Handler) HandleGetUserPurchaseOrders(c *gin.Context) {
	h.HandleGetUserOrders(c)
}
