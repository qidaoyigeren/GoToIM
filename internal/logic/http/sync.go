package http

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/gin-gonic/gin"
)

// syncOffline handles sync requests via HTTP, returning raw sync reply bytes.
// This bypasses the gRPC ReceiveReply proto descriptor limitation.
func (s *Server) syncOffline(c *gin.Context) {
	midStr := c.Query("mid")
	lastSeqStr := c.DefaultQuery("last_seq", "0")
	limitStr := c.DefaultQuery("limit", "100")

	mid, err := strconv.ParseInt(midStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mid"})
		return
	}
	lastSeq, _ := strconv.ParseInt(lastSeqStr, 10, 64)
	limit, _ := strconv.ParseInt(limitStr, 10, 32)

	reply, err := s.logic.GetOfflineMessages(context.TODO(), mid, lastSeq, int32(limit))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	data, err := protocol.MarshalSyncReply(reply)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("marshal: %v", err)})
		return
	}

	c.Data(http.StatusOK, "application/octet-stream", data)
}
