package dao

import (
	"context"
	"fmt"
	"time"

	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/gomodule/redigo/redis"
)

// Phase 2: 设备级离线 cursor — 同一用户不同设备独立同步进度。

const (
	_prefixDeviceCursor = "device_cursor:%d:%s" // uid:device_id → last synced seq
	_prefixMergeIndex   = "merge_idx:%d:%s:%s"  // uid:business_type:biz_id → msg_id (for merge lookup)
	_mergeIdxTTL        = 300                   // merge index TTL: 5 minutes
	_cursorTTL          = 30 * 24 * 3600        // cursor TTL: 30 days
)

func keyDeviceCursor(uid int64, deviceID string) string {
	return fmt.Sprintf(_prefixDeviceCursor, uid, deviceID)
}

func keyMergeIndex(uid int64, bizType, bizID string) string {
	return fmt.Sprintf(_prefixMergeIndex, uid, bizType, bizID)
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

// SetDeviceCursor updates the last synced sequence number for a device.
func (d *Dao) SetDeviceCursor(c context.Context, uid int64, deviceID string, seq int64) error {
	if deviceID == "" {
		deviceID = "default"
	}
	conn := d.redis.Get()
	defer conn.Close()
	key := keyDeviceCursor(uid, deviceID)
	if _, err := conn.Do("SET", key, seq, "EX", _cursorTTL); err != nil {
		log.Errorf("SetDeviceCursor uid=%d device=%s seq=%d err=%v", uid, deviceID, seq, err)
		return err
	}
	return nil
}

// GetOfflineMessagesByDeviceCursor fetches offline messages after the device-specific cursor.
func (d *Dao) GetOfflineMessagesByDeviceCursor(c context.Context, uid int64, deviceID string, limit int) ([]string, error) {
	lastSeq, err := d.GetDeviceCursor(c, uid, deviceID)
	if err != nil {
		return nil, err
	}
	return d.GetOfflineQueue(c, uid, float64(lastSeq), limit)
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

// SetMergeIndex records a mergeable message for dedup lookup.
func (d *Dao) SetMergeIndex(c context.Context, uid int64, bizType, bizID, msgID string) error {
	if bizType == "" || bizID == "" {
		return nil
	}
	conn := d.redis.Get()
	defer conn.Close()
	key := keyMergeIndex(uid, bizType, bizID)
	if _, err := conn.Do("SET", key, msgID, "EX", _mergeIdxTTL); err != nil {
		log.Warningf("SetMergeIndex uid=%d bizType=%s bizID=%s err=%v", uid, bizType, bizID, err)
		return err
	}
	return nil
}

// GetMergeIndex retrieves the mergeable message ID for dedup.
func (d *Dao) GetMergeIndex(c context.Context, uid int64, bizType, bizID string) (string, error) {
	if bizType == "" || bizID == "" {
		return "", nil
	}
	conn := d.redis.Get()
	defer conn.Close()
	msgID, err := redis.String(conn.Do("GET", keyMergeIndex(uid, bizType, bizID)))
	if err != nil {
		if err == redis.ErrNil {
			return "", nil
		}
		return "", err
	}
	return msgID, nil
}

// StoreOfflineMsgPayload stores the full payload of an offline message.
func (d *Dao) StoreOfflineMsgPayload(c context.Context, msgID string, data []byte) error {
	conn := d.redis.Get()
	defer conn.Close()
	key := fmt.Sprintf("offline_msg:%s", msgID)
	if _, err := conn.Do("SET", key, data, "EX", 7*24*3600); err != nil {
		log.Warningf("StoreOfflineMsgPayload msg_id=%s err=%v", msgID, err)
		return err
	}
	return nil
}

// GetOfflineMsgPayload retrieves the payload of an offline message.
func (d *Dao) GetOfflineMsgPayload(c context.Context, msgID string) ([]byte, error) {
	conn := d.redis.Get()
	defer conn.Close()
	data, err := redis.Bytes(conn.Do("GET", fmt.Sprintf("offline_msg:%s", msgID)))
	if err != nil {
		if err == redis.ErrNil {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// UpdateOfflineMsgPayload replaces the payload of an existing offline message.
func (d *Dao) UpdateOfflineMsgPayload(c context.Context, msgID string, data []byte) error {
	conn := d.redis.Get()
	defer conn.Close()
	key := fmt.Sprintf("offline_msg:%s", msgID)
	if _, err := conn.Do("SET", key, data, "EX", 7*24*3600); err != nil {
		return err
	}
	return nil
}

// UpdateOfflineMsgTime refreshes the timestamp of a message in the offline ZSET.
func (d *Dao) UpdateOfflineMsgTime(c context.Context, uid int64, msgID string, newSeq float64) error {
	conn := d.redis.Get()
	defer conn.Close()
	// ZADD with the same msgID updates the score
	if _, err := conn.Do("ZADD", keyOfflineQueue(uid), newSeq, msgID); err != nil {
		return err
	}
	if _, err := conn.Do("EXPIRE", keyOfflineQueue(uid), 7*24*3600); err != nil {
		log.Warningf("EXPIRE offline queue err=%v", err)
	}
	return nil
}

// dummy time reference
var _ = time.Now
