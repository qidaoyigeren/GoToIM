package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/service"
	"github.com/Terry-Mao/goim/internal/notify/store"
	"github.com/gin-gonic/gin"
)

func openMySQLStore(t *testing.T) *store.SQLStore {
	t.Helper()
	dsn := os.Getenv("GOIM_NOTIFY_MYSQL_DSN")
	if dsn == "" {
		t.Skip("GOIM_NOTIFY_MYSQL_DSN not set, skipping MySQL test")
	}
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("open mysql store: %v", err)
	}
	return st
}

func TestObservabilityHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	st := openMySQLStore(t)
	t.Cleanup(func() { _ = st.Close() })
	svc := service.NewOrderNotifyServiceWithStore(nil, st)
	t.Cleanup(func() { _ = svc.Close() })

	order, notif, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	outboxes, err := st.ClaimOutbox("handler-test", 10, time.Minute)
	if err != nil || len(outboxes) != 1 {
		t.Fatalf("ClaimOutbox len=%d err=%v", len(outboxes), err)
	}
	finished := time.Now()
	if err := st.MarkOutboxSent(outboxes[0].OutboxID, &model.NotificationAttempt{
		AttemptID: "atm-handler", NotifyID: notif.NotifyID, Channel: "grpc_direct", Target: notif.UserID,
		Status: "direct_sent", Path: "grpc_direct", LatencyMs: 5, AttemptNo: 1, StartedAt: finished, FinishedAt: &finished,
	}, finished); err != nil {
		t.Fatalf("MarkOutboxSent: %v", err)
	}
	if _, err := svc.RecordAckIdempotent(service.AckInput{NotifyID: notif.NotifyID, MsgID: "msg-1", DeviceID: "dev-1"}, "ack-handler"); err != nil {
		t.Fatalf("RecordAckIdempotent: %v", err)
	}

	h := New(svc, nil)
	router := gin.New()
	router.GET("/api/notifications/:notify_id/trace", h.HandleNotificationTrace)
	router.GET("/api/orders/:order_id/timeline", h.HandleGetOrderTimeline)
	router.GET("/api/platform/sla", h.HandleGetBusinessSLA)

	assertOK(t, router, http.MethodGet, "/api/notifications/"+notif.NotifyID+"/trace", nil)
	assertOK(t, router, http.MethodGet, "/api/orders/"+order.OrderID+"/timeline", nil)
	assertOK(t, router, http.MethodGet, "/api/platform/sla?window=24h", nil)
}

func TestBulkDLQHandlersUseOperatorHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	st := openMySQLStore(t)
	t.Cleanup(func() { _ = st.Close() })
	svc := service.NewOrderNotifyServiceWithStore(nil, st)
	t.Cleanup(func() { _ = svc.Close() })

	order, notif, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	outboxes, err := st.ClaimOutbox("handler-dlq", 10, time.Minute)
	if err != nil || len(outboxes) != 1 {
		t.Fatalf("ClaimOutbox len=%d err=%v", len(outboxes), err)
	}
	if err := st.MoveOutboxToDLQ(outboxes[0], &model.NotificationDLQ{
		DLQID: "dlq-handler", NotifyID: notif.NotifyID, OutboxID: outboxes[0].OutboxID, UserID: notif.UserID,
		OrderID: order.OrderID, Reason: "http_4xx", LastError: "bad request", PayloadJSON: outboxes[0].PayloadJSON, RetryCount: 1, CreatedAt: time.Now(),
	}, nil, "dlq"); err != nil {
		t.Fatalf("MoveOutboxToDLQ: %v", err)
	}

	h := New(svc, nil)
	router := gin.New()
	router.POST("/api/dlq/bulk/replay", h.HandleBulkReplayDLQ)
	router.GET("/api/dlq/:id/audits", h.HandleDLQAudits)
	router.GET("/api/recovery/audits", h.HandleRecoveryAudits)
	body, _ := json.Marshal(map[string]any{"reason": "http_4xx", "business_type": "order", "limit": 10})
	req := httptest.NewRequest(http.MethodPost, "/api/dlq/bulk/replay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Operator", "support-user")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	operator, err := st.LatestRecoveryAuditOperator()
	if err != nil {
		t.Fatalf("audit operator: %v", err)
	}
	if operator != "support-user" {
		t.Fatalf("operator = %q, want support-user", operator)
	}
	assertAuditOperator(t, router, "/api/dlq/dlq-handler/audits", "support-user")
	assertAuditOperator(t, router, "/api/recovery/audits?operator=support-user&action=replay&business_type=order&limit=10", "support-user")
}

func assertOK(t *testing.T, router *gin.Engine, method, target string, body []byte) {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s status=%d body=%s", method, target, w.Code, w.Body.String())
	}
}

func assertAuditOperator(t *testing.T, router *gin.Engine, target, want string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", target, w.Code, w.Body.String())
	}
	var resp struct {
		Code int                               `json:"code"`
		Data []model.NotificationRecoveryAudit `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode audits: %v body=%s", err, w.Body.String())
	}
	if len(resp.Data) != 1 || resp.Data[0].Operator != want {
		t.Fatalf("audits = %+v, want operator %s", resp.Data, want)
	}
}
