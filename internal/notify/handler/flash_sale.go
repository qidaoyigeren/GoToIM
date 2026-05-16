package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// FlashSaleRequest is the request body for POST /api/flash-sale/notify.
type FlashSaleRequest struct {
	Title       string   `json:"title" binding:"required"`
	Description string   `json:"description"`
	TargetUIDs  []string `json:"target_uids"`
}

// HandleCreateFlashSale handles POST /api/flash-sale/notify.
func (h *Handler) HandleCreateFlashSale(c *gin.Context) {
	var req FlashSaleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}

	sale, err := h.flashSaleSvc.CreateFlashSale(req.Title, req.Description, req.TargetUIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": sale})
}
