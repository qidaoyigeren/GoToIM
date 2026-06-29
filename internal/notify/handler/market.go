package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HandleListMerchants handles GET /api/market/merchants.
func (h *Handler) HandleListMerchants(c *gin.Context) {
	merchants, err := h.orderSvc.ListMerchants()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": merchants})
}

// HandleListProducts handles GET /api/market/products?merchant_id=...
func (h *Handler) HandleListProducts(c *gin.Context) {
	products, err := h.orderSvc.ListProducts(c.Query("merchant_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": products})
}

// HandleListMerchantGroups handles GET /api/market/groups?merchant_id=...
func (h *Handler) HandleListMerchantGroups(c *gin.Context) {
	groups, err := h.orderSvc.ListMerchantGroups(c.Query("merchant_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": groups})
}
