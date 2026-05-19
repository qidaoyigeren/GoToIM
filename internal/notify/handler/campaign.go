package handler

import (
	"log"
	"net/http"
	"strconv"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/gin-gonic/gin"
)

// HandlePauseCampaign pauses an active campaign.
// PATCH /api/campaigns/:id/pause
func (h *Handler) HandlePauseCampaign(c *gin.Context) {
	campaignID := c.Param("id")
	if campaignID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "campaign_id required"})
		return
	}
	if err := h.orderSvc.Store().PauseCampaign(campaignID); err != nil {
		log.Printf("[campaign] pause failed for %s: %v", campaignID, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[campaign] paused %s", campaignID)
	c.JSON(http.StatusOK, gin.H{"status": "paused", "campaign_id": campaignID})
}

// HandleResumeCampaign resumes a paused campaign.
// PATCH /api/campaigns/:id/resume
func (h *Handler) HandleResumeCampaign(c *gin.Context) {
	campaignID := c.Param("id")
	if campaignID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "campaign_id required"})
		return
	}
	if err := h.orderSvc.Store().ResumeCampaign(campaignID); err != nil {
		log.Printf("[campaign] resume failed for %s: %v", campaignID, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[campaign] resumed %s", campaignID)
	c.JSON(http.StatusOK, gin.H{"status": "active", "campaign_id": campaignID})
}

// HandleCancelCampaign cancels a campaign and its pending outbox items.
// PATCH /api/campaigns/:id/cancel
func (h *Handler) HandleCancelCampaign(c *gin.Context) {
	campaignID := c.Param("id")
	if campaignID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "campaign_id required"})
		return
	}
	if err := h.orderSvc.Store().CancelCampaign(campaignID); err != nil {
		log.Printf("[campaign] cancel failed for %s: %v", campaignID, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[campaign] cancelled %s", campaignID)
	c.JSON(http.StatusOK, gin.H{"status": "cancelled", "campaign_id": campaignID})
}

// HandleGetCampaign returns campaign details.
// GET /api/campaigns/:id
func (h *Handler) HandleGetCampaign(c *gin.Context) {
	campaignID := c.Param("id")
	if campaignID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "campaign_id required"})
		return
	}
	campaign, err := h.orderSvc.Store().GetCampaign(campaignID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "campaign not found"})
		return
	}
	c.JSON(http.StatusOK, campaign)
}

// HandleImportCampaignAudience imports an audience snapshot and batch records.
func (h *Handler) HandleImportCampaignAudience(c *gin.Context) {
	var req struct {
		Name       string            `json:"name,omitempty"`
		Definition map[string]string `json:"definition,omitempty"`
		TargetUIDs []string          `json:"target_uids"`
		BatchSize  int               `json:"batch_size,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -400, "message": err.Error()})
		return
	}
	audience, batches, err := h.orderSvc.ImportCampaignAudience(c.Param("id"), req.Name, req.Definition, req.TargetUIDs, req.BatchSize)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"audience": audience, "batches": batches}})
}

func (h *Handler) HandleListCampaignAudienceTargets(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	statuses := []string{}
	if status := c.Query("status"); status != "" {
		statuses = append(statuses, status)
	}
	targets, err := h.orderSvc.ListCampaignAudienceTargets(c.Param("audience_id"), statuses, limit)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": targets})
}

func (h *Handler) HandleListCampaignAudienceBatches(c *gin.Context) {
	batches, err := h.orderSvc.ListCampaignAudienceBatches(c.Param("audience_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": batches})
}

func (h *Handler) HandleRetryCampaignAudienceBatch(c *gin.Context) {
	if err := h.orderSvc.RetryCampaignAudienceBatch(c.Param("batch_id")); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": model.CampaignAudienceBatch{BatchID: c.Param("batch_id"), Status: "retrying"}})
}
