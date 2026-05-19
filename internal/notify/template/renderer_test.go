package template

import (
	"testing"
)

func TestRenderTemplate(t *testing.T) {
	result, err := renderTemplate("Hello {{.name}}", map[string]interface{}{"name": "World"})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if result != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", result)
	}
}

func TestRenderTemplateMultipleVars(t *testing.T) {
	vars := map[string]interface{}{
		"order_id": "ORD-123",
		"amount":   "99.99",
		"user_id":  "user-1",
	}
	result, err := renderTemplate("订单 {{.order_id}} 已支付，金额 {{.amount}} 元", vars)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if result != "订单 ORD-123 已支付，金额 99.99 元" {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestRenderTemplateMissingVar(t *testing.T) {
	// Go templates render missing vars as "<no value>"
	result, err := renderTemplate("Hello {{.missing}}", map[string]interface{}{})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	// Should not error, just renders placeholder
	if result == "" {
		t.Error("expected non-empty result even with missing var")
	}
}

func TestRenderTemplateInvalidSyntax(t *testing.T) {
	_, err := renderTemplate("Hello {{.name", map[string]interface{}{"name": "World"})
	if err == nil {
		t.Error("expected error for invalid template syntax")
	}
}
