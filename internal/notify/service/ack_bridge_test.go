package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeAckBridgeMsgIDs(t *testing.T) {
	got := normalizeAckBridgeMsgIDs("m2", []string{"m1", "m2", "m1"})
	assert.Equal(t, []string{"m1", "m2"}, got)
}
