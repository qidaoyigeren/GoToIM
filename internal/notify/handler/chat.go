package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/service"
	"github.com/Terry-Mao/goim/internal/notify/store"
	"github.com/gin-gonic/gin"
)

type createChatConversationRequest struct {
	OrderID     string `json:"order_id"`
	CustomerUID int64  `json:"customer_uid"`
	MerchantUID int64  `json:"merchant_uid"`
}

type sendChatMessageRequest struct {
	SenderUID int64  `json:"sender_uid"`
	Body      string `json:"body"`
}

type updateChatStatusRequest struct {
	Status string `json:"status"`
}

type joinChatGroupRequest struct {
	UserID int64 `json:"user_id"`
}

func (h *Handler) HandleCreateChatConversation(c *gin.Context) {
	var req createChatConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.OrderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "order_id is required"})
		return
	}
	if req.CustomerUID == 0 {
		req.CustomerUID = 10001
	}
	if req.MerchantUID == 0 {
		req.MerchantUID = 90001
	}
	conv, err := h.chatSvc.CreateConversation(req.OrderID, req.CustomerUID, req.MerchantUID)
	if err != nil {
		writeChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": conv})
}

func (h *Handler) HandleListChatConversations(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Query("user_id"), 10, 64)
	if err != nil || userID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "valid user_id is required"})
		return
	}
	convs, err := h.chatSvc.ListConversations(userID)
	if err != nil {
		writeChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": convs})
}

func (h *Handler) HandleListChatMessages(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Query("user_id"), 10, 64)
	if err != nil || userID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "valid user_id is required"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	messages, err := h.chatSvc.ListMessages(c.Param("id"), userID, limit)
	if err != nil {
		writeChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": messages})
}

func (h *Handler) HandleSendChatMessage(c *gin.Context) {
	var req sendChatMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.SenderUID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "sender_uid is required"})
		return
	}
	if req.Body == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "body is required"})
		return
	}
	msg, err := h.chatSvc.SendMessage(c.Request.Context(), c.Param("id"), req.SenderUID, req.Body)
	if err != nil {
		writeChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": msg})
}

func (h *Handler) HandleUpdateChatMessageStatus(c *gin.Context) {
	var req updateChatStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.Status == "" {
		req.Status = model.ChatStatusRead
	}
	if err := h.chatSvc.MarkMessageStatus(c.Param("id"), req.Status); err != nil {
		writeChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"message_id": c.Param("id"), "status": req.Status}})
}

func (h *Handler) HandleJoinChatGroup(c *gin.Context) {
	var req joinChatGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.UserID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "user_id is required"})
		return
	}
	conv, err := h.chatSvc.JoinMerchantGroup(c.Param("room_id"), req.UserID)
	if err != nil {
		writeChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": conv})
}

func writeChatError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrChatForbidden):
		c.JSON(http.StatusForbidden, gin.H{"code": -403, "message": err.Error()})
	case errors.Is(err, store.ErrNotFound), errors.Is(err, service.ErrChatConversationNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": -404, "message": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": -500, "message": err.Error()})
	}
}
