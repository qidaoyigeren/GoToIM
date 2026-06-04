package dao

import (
	"context"
	"fmt"

	"github.com/gomodule/redigo/redis"
)

// Phase 2: 设备级离线 cursor — 同一用户不同设备独立同步进度。

const (
	_prefixDeviceCursor = "device_cursor:%d:%s" // uid:device_id → last synced seq
	_cursorTTL          = 30 * 24 * 3600        // cursor TTL: 30 days
)

func keyDeviceCursor(uid int64, deviceID string) string {
	return fmt.Sprintf(_prefixDeviceCursor, uid, deviceID)
}

// GetDeviceCursor returns the last synced sequence number for a device.
func (d *Dao) GetDeviceCursor(c context.Context, uid int64, deviceID string) (int64, error) {
	if deviceID == "" {
		deviceID = "default"
	}
	conn := d.redis.Get()
	defer conn.Close()
	seq, err := redis.Int64(conn.Do("GET", keyDeviceCursor(uid, deviceID)))
	if err != nil {
		if err == redis.ErrNil {
			return 0, nil
		}
		return 0, err
	}
	return seq, nil
}

// GetOfflineMessagesByDeviceCursor fetches offline messages after the device-specific cursor.
func (d *Dao) GetOfflineMessagesByDeviceCursor(c context.Context, uid int64, deviceID string, limit int) ([]string, error) {
	lastSeq, err := d.GetDeviceCursor(c, uid, deviceID)
	if err != nil {
		return nil, err
	}
	return d.GetUserMessagesAfterSeq(c, uid, lastSeq, limit)
}

// AdvanceDeviceCursor moves the device cursor forward to the given seq.
// Only updates if seq > current cursor (monotonic progress).
func (d *Dao) AdvanceDeviceCursor(c context.Context, uid int64, deviceID string, seq int64) error {
	if deviceID == "" {
		deviceID = "default"
	}
	conn := d.redis.Get()
	defer conn.Close()
	key := keyDeviceCursor(uid, deviceID)
	current, _ := redis.Int64(conn.Do("GET", key))
	if seq <= current {
		return nil
	}
	if _, err := conn.Do("SET", key, seq, "EX", _cursorTTL); err != nil {
		return err
	}
	return nil
}
