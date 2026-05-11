package log

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestInit_Development(t *testing.T) {
	err := Init(true)
	assert.NoError(t, err)
	assert.NotNil(t, L)
}

func TestInit_Production(t *testing.T) {
	err := Init(false)
	assert.NoError(t, err)
	assert.NotNil(t, L)
}

func TestInfof_NoPanic(t *testing.T) {
	Init(true)
	assert.NotPanics(t, func() {
		Infof("test message: %s %d", "hello", 42)
	})
}

func TestWarningf_NoPanic(t *testing.T) {
	Init(true)
	assert.NotPanics(t, func() {
		Warningf("warning: %v", "something")
	})
}

func TestErrorf_NoPanic(t *testing.T) {
	Init(true)
	assert.NotPanics(t, func() {
		Errorf("error: %d", 500)
	})
}

func TestV_Infof(t *testing.T) {
	Init(true)
	assert.NotPanics(t, func() {
		V(0).Infof("verbose info: %s", "test")
		V(1).Infof("verbose debug: %s", "test")
		V(2).Infof("verbose trace: %s", "test")
	})
}

func TestDefaultNopLogger(t *testing.T) {
	// Reset to default nop logger
	L = zap.NewNop()
	sugar = L.Sugar()
	assert.NotPanics(t, func() {
		Infof("should not panic with nop logger")
		Warningf("should not panic")
		Errorf("should not panic")
		V(0).Infof("should not panic")
	})
}

func TestSync(t *testing.T) {
	Init(true)
	err := Sync()
	// Sync may return an error on stdout in tests, that's ok
	_ = err
}
