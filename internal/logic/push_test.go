package logic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPushKeys(t *testing.T) {
	if lg == nil {
		t.Skip("logic not initialized (infrastructure unavailable)")
	}
	var (
		c    = context.TODO()
		op   = int32(100)
		keys = []string{"test_key"}
		msg  = []byte("hello")
	)
	_, err := lg.PushKeys(c, op, keys, msg)
	assert.Nil(t, err)
}

func TestPushMids(t *testing.T) {
	if lg == nil {
		t.Skip("logic not initialized (infrastructure unavailable)")
	}
	var (
		c    = context.TODO()
		op   = int32(100)
		mids = []int64{1, 2, 3}
		msg  = []byte("hello")
	)
	msgIDs, err := lg.PushMids(c, op, mids, msg)
	assert.Nil(t, err)
	assert.Len(t, msgIDs, 3)
}

func TestPushRoom(t *testing.T) {
	if lg == nil {
		t.Skip("logic not initialized (infrastructure unavailable)")
	}
	var (
		c    = context.TODO()
		op   = int32(100)
		typ  = "test"
		room = "test_room"
		msg  = []byte("hello")
	)
	msgID, err := lg.PushRoom(c, op, typ, room, msg)
	assert.Nil(t, err)
	assert.NotEmpty(t, msgID)
}

func TestPushAll(t *testing.T) {
	if lg == nil {
		t.Skip("logic not initialized (infrastructure unavailable)")
	}
	var (
		c     = context.TODO()
		op    = int32(100)
		speed = int32(100)
		msg   = []byte("hello")
	)
	msgID, err := lg.PushAll(c, op, speed, msg)
	assert.Nil(t, err)
	assert.NotEmpty(t, msgID)
}
