package dao

import (
	"context"
	"testing"

	"github.com/Terry-Mao/goim/internal/logic/model"
	"github.com/stretchr/testify/assert"
)

func TestDaopingRedis(t *testing.T) {
	err := d.pingRedis(context.Background())
	assert.Nil(t, err)
}

func TestDaoAddMapping(t *testing.T) {
	var (
		c      = context.Background()
		mid    = int64(1)
		key    = "test_key"
		server = "test_server"
	)
	err := d.AddMapping(c, 0, "test", server)
	assert.Nil(t, err)
	err = d.AddMapping(c, mid, key, server)
	assert.Nil(t, err)

	has, err := d.ExpireMapping(c, 0, "test")
	assert.Nil(t, err)
	assert.NotEqual(t, false, has)
	has, err = d.ExpireMapping(c, mid, key)
	assert.Nil(t, err)
	assert.NotEqual(t, false, has)

	res, err := d.ServersByKeys(c, []string{key})
	assert.Nil(t, err)
	assert.Equal(t, server, res[0])

	ress, mids, err := d.KeysByMids(c, []int64{mid})
	assert.Nil(t, err)
	assert.Equal(t, server, ress[key])
	assert.Equal(t, mid, mids[0])

	has, err = d.DelMapping(c, 0, "test", server)
	assert.Nil(t, err)
	assert.NotEqual(t, false, has)
	has, err = d.DelMapping(c, mid, key, server)
	assert.Nil(t, err)
	assert.NotEqual(t, false, has)
}

func TestDaoAddServerOnline(t *testing.T) {
	var (
		c      = context.Background()
		server = "test_server"
		online = &model.Online{
			RoomCount: map[string]int32{"room": 10},
		}
	)
	err := d.AddServerOnline(c, server, online)
	assert.Nil(t, err)

	r, err := d.ServerOnline(c, server)
	assert.Nil(t, err)
	assert.Equal(t, online.RoomCount["room"], r.RoomCount["room"])

	err = d.DelServerOnline(c, server)
	assert.Nil(t, err)
}

func TestDaoSession(t *testing.T) {
	c := context.Background()
	sid := "test_sid_1"
	uid := int64(99001)
	key := "test_key_1"
	deviceID := "dev_1"
	platform := "android"
	server := "comet_test"

	// AddSession
	err := d.AddSession(c, sid, uid, key, deviceID, platform, server)
	assert.Nil(t, err)

	// GetSession
	data, err := d.GetSession(c, sid)
	assert.Nil(t, err)
	assert.Equal(t, key, data["key"])
	assert.Equal(t, deviceID, data["device_id"])
	assert.Equal(t, platform, data["platform"])
	assert.Equal(t, server, data["server"])

	// GetUserSessions
	sessions, err := d.GetUserSessions(c, uid)
	assert.Nil(t, err)
	assert.NotEmpty(t, sessions)
	_, ok := sessions[sid]
	assert.True(t, ok)

	// ExpireSession
	err = d.ExpireSession(c, sid, uid)
	assert.Nil(t, err)

	// DelSession
	err = d.DelSession(c, sid, uid, deviceID, key)
	assert.Nil(t, err)

	// Verify deleted
	data, err = d.GetSession(c, sid)
	assert.Nil(t, err)
	assert.Empty(t, data)
}

func TestDaoDeviceSession(t *testing.T) {
	c := context.Background()
	sid := "test_sid_dev"
	uid := int64(99002)
	key := "test_key_dev"
	deviceID := "dev_x"
	platform := "ios"
	server := "comet_test"

	// AddSession
	err := d.AddSession(c, sid, uid, key, deviceID, platform, server)
	assert.Nil(t, err)

	// GetDeviceSession
	gotSID, err := d.GetDeviceSession(c, uid, deviceID)
	assert.Nil(t, err)
	assert.Equal(t, sid, gotSID)

	// DelSession
	err = d.DelSession(c, sid, uid, deviceID, key)
	assert.Nil(t, err)

	// Verify deleted
	gotSID, err = d.GetDeviceSession(c, uid, deviceID)
	assert.Nil(t, err)
	assert.Empty(t, gotSID)
}

func TestDaoMessageStatus(t *testing.T) {
	c := context.Background()
	msgID := "test_msg_1"

	// SetMessageStatus
	fields := map[string]interface{}{
		"status":   "pending",
		"from_uid": int64(100),
		"to_uid":   int64(200),
	}
	err := d.SetMessageStatus(c, msgID, fields)
	assert.Nil(t, err)

	// GetMessageStatus
	data, err := d.GetMessageStatus(c, msgID)
	assert.Nil(t, err)
	assert.Equal(t, "pending", data["status"])

	// UpdateMessageStatus
	err = d.UpdateMessageStatus(c, msgID, "delivered")
	assert.Nil(t, err)

	data, err = d.GetMessageStatus(c, msgID)
	assert.Nil(t, err)
	assert.Equal(t, "delivered", data["status"])
}

func TestDaoUserSeq(t *testing.T) {
	c := context.Background()
	uid := int64(99003)

	// IncrUserSeq — should increment
	seq1, err := d.IncrUserSeq(c, uid)
	assert.Nil(t, err)

	seq2, err := d.IncrUserSeq(c, uid)
	assert.Nil(t, err)

	assert.Equal(t, seq1+1, seq2)
}

func TestDaoOfflineQueue(t *testing.T) {
	c := context.Background()
	uid := int64(99004)

	// AddToOfflineQueue
	err := d.AddToOfflineQueue(c, uid, "msg_a", 1.0)
	assert.Nil(t, err)
	err = d.AddToOfflineQueue(c, uid, "msg_b", 2.0)
	assert.Nil(t, err)
	err = d.AddToOfflineQueue(c, uid, "msg_c", 3.0)
	assert.Nil(t, err)

	// GetOfflineQueueSize
	size, err := d.GetOfflineQueueSize(c, uid)
	assert.Nil(t, err)
	assert.Equal(t, int64(3), size)

	// GetOfflineQueue with lastSeq=1.0 (should return msg_b, msg_c)
	msgIDs, err := d.GetOfflineQueue(c, uid, 1.0, 100)
	assert.Nil(t, err)
	assert.Len(t, msgIDs, 2)
	assert.Equal(t, "msg_b", msgIDs[0])
	assert.Equal(t, "msg_c", msgIDs[1])

	// RemoveFromOfflineQueue
	err = d.RemoveFromOfflineQueue(c, uid, "msg_b")
	assert.Nil(t, err)

	size, err = d.GetOfflineQueueSize(c, uid)
	assert.Nil(t, err)
	assert.Equal(t, int64(2), size)

	// Cleanup
	d.RemoveFromOfflineQueue(c, uid, "msg_a")
	d.RemoveFromOfflineQueue(c, uid, "msg_c")
}
