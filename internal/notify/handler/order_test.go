package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/service"
	"github.com/Terry-Mao/goim/internal/notify/store"
	"github.com/gin-gonic/gin"
)

func TestHandleOrderStatusChangeInvalidTransitionReturnsConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	svc := service.NewOrderNotifyServiceWithStore(nil, st)
	t.Cleanup(func() { _ = svc.Close() })

	order, _, err := svc.CreateOrder("1001", []model.OrderItem{{ProductName: "phone", Quantity: 1, Price: 99}}, 99)
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}

	h := New(svc, nil)
	router := gin.New()
	router.POST("/api/order/status-change", h.HandleOrderStatusChange)

	body := []byte(`{"order_id":"` + order.OrderID + `","new_status":"delivered"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/order/status-change", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", w.Code, w.Body.String())
	}
}
