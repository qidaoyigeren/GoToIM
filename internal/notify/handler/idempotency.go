package handler

import (
	"errors"
	"net/http"

	"github.com/Terry-Mao/goim/internal/notify/service"
	"github.com/Terry-Mao/goim/internal/notify/store"
	"github.com/gin-gonic/gin"
)

func idempotencyKey(c *gin.Context, bodyKey string) string {
	if key := c.GetHeader("Idempotency-Key"); key != "" {
		return key
	}
	return bodyKey
}

func writeServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrOrderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": -404, "message": "order not found"})
	case errors.Is(err, service.ErrInvalidTransition):
		c.JSON(http.StatusConflict, gin.H{"code": -409, "message": err.Error()})
	case errors.Is(err, store.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": -404, "message": "not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": -500, "message": err.Error()})
	}
}
