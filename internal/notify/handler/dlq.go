package handler

import (
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/gin-gonic/gin"
)

// HandleListDLQ returns unresolved and recent DLQ notifications.
func (h *Handler) HandleListDLQ(c *gin.Context) {
	filter := dlqFilterFromQuery(c)
	items, err := h.orderSvc.ListDLQFiltered(filter)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": items})
}

// HandleDLQAudits returns recovery audit history for one DLQ item.
func (h *Handler) HandleDLQAudits(c *gin.Context) {
	audits, err := h.orderSvc.ListRecoveryAuditsByDLQ(c.Param("id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": audits})
}

// HandleRecoveryAudits returns recovery audit history across DLQ items.
func (h *Handler) HandleRecoveryAudits(c *gin.Context) {
	filter, err := recoveryAuditFilterFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	audits, err := h.orderSvc.ListRecoveryAudits(filter)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": audits})
}

// HandleGetDLQ returns one DLQ record.
func (h *Handler) HandleGetDLQ(c *gin.Context) {
	item, err := h.orderSvc.GetDLQ(c.Param("id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": item})
}

// HandleReplayDLQ requeues a DLQ item through the outbox.
func (h *Handler) HandleReplayDLQ(c *gin.Context) {
	var req struct {
		ResolvedBy string `json:"resolved_by,omitempty"`
		Operator   string `json:"operator,omitempty"`
		Note       string `json:"note,omitempty"`
	}
	_ = c.ShouldBindJSON(&req)
	operator := recoveryOperator(c, req.Operator, req.ResolvedBy)
	item, err := h.orderSvc.ReplayDLQWithNote(c.Param("id"), operator, req.Note)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"replayed": true, "item": item}})
}

// HandleBulkReplayDLQ requeues matching DLQ items through the outbox.
func (h *Handler) HandleBulkReplayDLQ(c *gin.Context) {
	var req struct {
		model.DLQBulkFilter
		Operator string `json:"operator,omitempty"`
		Note     string `json:"note,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	operator := recoveryOperator(c, req.Operator, "")
	if approval, ok, err := h.maybeCreateReplayApproval("replay", req.DLQBulkFilter, operator, "", req.Note, 0); err != nil {
		writeServiceError(c, err)
		return
	} else if ok {
		c.JSON(http.StatusAccepted, gin.H{"code": 0, "data": gin.H{"approval_required": true, "request": approval}})
		return
	}
	result, err := h.orderSvc.BulkReplayDLQ(req.DLQBulkFilter, operator, req.Note)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": result})
}

// HandleResolveDLQ marks a DLQ item resolved without replay.
func (h *Handler) HandleResolveDLQ(c *gin.Context) {
	var req struct {
		ResolvedBy string `json:"resolved_by,omitempty"`
		Operator   string `json:"operator,omitempty"`
		Resolution string `json:"resolution,omitempty"`
		Note       string `json:"note,omitempty"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Resolution == "" {
		req.Resolution = "resolved"
	}
	item, err := h.orderSvc.ResolveDLQWithNote(c.Param("id"), recoveryOperator(c, req.Operator, req.ResolvedBy), req.Resolution, req.Note)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"resolved": true, "item": item}})
}

// HandleBulkResolveDLQ resolves matching DLQ items without replay.
func (h *Handler) HandleBulkResolveDLQ(c *gin.Context) {
	var req struct {
		model.DLQBulkFilter
		Operator   string `json:"operator,omitempty"`
		Resolution string `json:"resolution,omitempty"`
		Note       string `json:"note,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.Resolution == "" {
		req.Resolution = "resolved"
	}
	operator := recoveryOperator(c, req.Operator, "")
	if approval, ok, err := h.maybeCreateReplayApproval("resolve", req.DLQBulkFilter, operator, req.Resolution, req.Note, 0); err != nil {
		writeServiceError(c, err)
		return
	} else if ok {
		c.JSON(http.StatusAccepted, gin.H{"code": 0, "data": gin.H{"approval_required": true, "request": approval}})
		return
	}
	result, err := h.orderSvc.BulkResolveDLQ(req.DLQBulkFilter, operator, req.Resolution, req.Note)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": result})
}

// HandleCreateReplayRequest creates an approval request for a bulk recovery action.
func (h *Handler) HandleCreateReplayRequest(c *gin.Context) {
	var req struct {
		Action         string              `json:"action"`
		Filter         model.DLQBulkFilter `json:"filter"`
		Operator       string              `json:"operator,omitempty"`
		Resolution     string              `json:"resolution,omitempty"`
		Note           string              `json:"note,omitempty"`
		ThrottlePerSec int                 `json:"throttle_per_sec,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	if req.Action != "replay" && req.Action != "resolve" {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": "action must be replay or resolve"})
		return
	}
	operator := recoveryOperator(c, req.Operator, "")
	items, err := h.orderSvc.ListDLQFiltered(req.Filter)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	approval := &model.ReplayApprovalRequest{
		Action:         req.Action,
		Status:         model.ReplayRequestPending,
		Operator:       operator,
		Filter:         req.Filter,
		MatchedCount:   int64(len(items)),
		Threshold:      int64(replayApprovalThreshold()),
		Resolution:     req.Resolution,
		Note:           req.Note,
		ThrottlePerSec: req.ThrottlePerSec,
	}
	if err := h.orderSvc.CreateReplayApprovalRequest(approval); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"code": 0, "data": approval})
}

func (h *Handler) HandleListReplayRequests(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	requests, err := h.orderSvc.ListReplayApprovalRequests(c.Query("status"), limit)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": requests})
}

func (h *Handler) HandleGetReplayRequest(c *gin.Context) {
	request, err := h.orderSvc.GetReplayApprovalRequest(c.Param("id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": request})
}

func (h *Handler) HandleApproveReplayRequest(c *gin.Context) {
	h.updateReplayRequestStatus(c, model.ReplayRequestApproved)
}

func (h *Handler) HandleRejectReplayRequest(c *gin.Context) {
	h.updateReplayRequestStatus(c, model.ReplayRequestRejected)
}

func (h *Handler) HandleCancelReplayRequest(c *gin.Context) {
	h.updateReplayRequestStatus(c, model.ReplayRequestCancelled)
}

func (h *Handler) updateReplayRequestStatus(c *gin.Context, status string) {
	var req struct {
		Operator string `json:"operator,omitempty"`
		Note     string `json:"note,omitempty"`
	}
	_ = c.ShouldBindJSON(&req)
	request, err := h.orderSvc.UpdateReplayApprovalStatus(c.Param("id"), status, recoveryOperator(c, req.Operator, ""), req.Note)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": request})
}

func (h *Handler) HandleExecuteReplayRequest(c *gin.Context) {
	var req struct {
		Operator string `json:"operator,omitempty"`
	}
	_ = c.ShouldBindJSON(&req)
	request, err := h.orderSvc.ExecuteReplayApprovalRequest(c.Param("id"), recoveryOperator(c, req.Operator, ""))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": request})
}

func (h *Handler) maybeCreateReplayApproval(action string, filter model.DLQBulkFilter, operator, resolution, note string, throttlePerSec int) (*model.ReplayApprovalRequest, bool, error) {
	items, err := h.orderSvc.ListDLQFiltered(filter)
	if err != nil {
		return nil, false, err
	}
	threshold := replayApprovalThreshold()
	if int64(len(items)) <= int64(threshold) {
		return nil, false, nil
	}
	approval := &model.ReplayApprovalRequest{
		Action:         action,
		Status:         model.ReplayRequestPending,
		Operator:       operator,
		Filter:         filter,
		MatchedCount:   int64(len(items)),
		Threshold:      int64(threshold),
		Resolution:     resolution,
		Note:           note,
		ThrottlePerSec: throttlePerSec,
	}
	if err := h.orderSvc.CreateReplayApprovalRequest(approval); err != nil {
		return nil, false, err
	}
	return approval, true, nil
}

func replayApprovalThreshold() int {
	if v, err := strconv.Atoi(os.Getenv("GOIM_REPLAY_APPROVAL_THRESHOLD")); err == nil && v > 0 {
		return v
	}
	return 100
}

func recoveryOperator(c *gin.Context, values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	if header := c.GetHeader("X-Operator"); header != "" {
		return header
	}
	return "api"
}

func dlqFilterFromQuery(c *gin.Context) model.DLQBulkFilter {
	older, _ := strconv.ParseInt(c.Query("older_than_seconds"), 10, 64)
	limit, _ := strconv.Atoi(c.Query("limit"))
	return model.DLQBulkFilter{
		Resolved:         c.Query("resolved"),
		Reason:           c.Query("reason"),
		BusinessType:     c.Query("business_type"),
		OlderThanSeconds: older,
		Operator:         c.Query("operator"),
		Limit:            limit,
	}
}

func recoveryAuditFilterFromQuery(c *gin.Context) (model.RecoveryAuditFilter, error) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	filter := model.RecoveryAuditFilter{
		Operator:     c.Query("operator"),
		Action:       c.Query("action"),
		BusinessType: c.Query("business_type"),
		Limit:        limit,
	}
	if since := c.Query("since"); since != "" {
		t, err := time.Parse(time.RFC3339Nano, since)
		if err != nil {
			return filter, err
		}
		filter.Since = t
	}
	if until := c.Query("until"); until != "" {
		t, err := time.Parse(time.RFC3339Nano, until)
		if err != nil {
			return filter, err
		}
		filter.Until = t
	}
	return filter, nil
}
