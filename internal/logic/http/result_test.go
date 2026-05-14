package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestResultOK(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	result(c, map[string]string{"key": "value"}, OK)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var r resp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if r.Code != OK {
		t.Errorf("code = %d, want %d", r.Code, OK)
	}
	if r.Message != "" {
		t.Errorf("message = %q, want empty", r.Message)
	}
}

func TestResultNilData(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	result(c, nil, OK)

	var r resp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if r.Data != nil {
		t.Errorf("data = %v, want nil", r.Data)
	}
}

func TestErrorsFunction(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	errors(c, RequestErr, "bad request")

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var r resp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if r.Code != RequestErr {
		t.Errorf("code = %d, want %d", r.Code, RequestErr)
	}
	if r.Message != "bad request" {
		t.Errorf("message = %q, want %q", r.Message, "bad request")
	}
	if r.Data != nil {
		t.Errorf("data = %v, want nil", r.Data)
	}
}

func TestErrorsServerErr(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	errors(c, ServerErr, "internal error")

	var r resp
	json.Unmarshal(w.Body.Bytes(), &r)
	if r.Code != ServerErr {
		t.Errorf("code = %d, want %d", r.Code, ServerErr)
	}
}

func TestConstants(t *testing.T) {
	if OK != 0 {
		t.Errorf("OK = %d, want 0", OK)
	}
	if RequestErr != -400 {
		t.Errorf("RequestErr = %d, want -400", RequestErr)
	}
	if ServerErr != -500 {
		t.Errorf("ServerErr = %d, want -500", ServerErr)
	}
}

func TestContextErrCode(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	result(c, nil, OK)

	val, exists := c.Get(contextErrCode)
	if !exists {
		t.Fatal("contextErrCode not set")
	}
	if val != OK {
		t.Errorf("contextErrCode = %d, want %d", val, OK)
	}
}
