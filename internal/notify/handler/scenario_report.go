package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HandleScenarioReport returns a comprehensive report for a scenario run.
// GET /api/scenarios/:id/report
func (h *Handler) HandleScenarioReport(c *gin.Context) {
	runID := c.Param("id")
	if runID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scenario_id required"})
		return
	}
	report, err := h.orderSvc.Store().GenerateScenarioReport(runID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "scenario not found: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, report)
}
