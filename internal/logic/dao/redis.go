package dao

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/model"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/gomodule/redigo/redis"

	"github.com/zhenjl/cityhash"
)

const (
	_prefixMidServer     = "mid_%d"               // mid -> key:server
	_prefixKeyServer     = "key_%s"               // key -> server
	_prefixServerOnline  = "ol_%s"                // server -> online
	_prefixSession       = "session:%s"           // sid -> session metadata
	_prefixUserSessions  = "user_sessions:%d"     // uid -> {sid: device_info}
	_prefixDeviceSession = "device_session:%d:%s" // uid:device_id -> sid
	_prefixMsgStatus     = "msg:%s"               // msg_id -> message status
	_prefixUserSeq       = "user_seq:%d"          // uid -> monotonic seq
	_prefixOfflineQueue  = "offline:%d"           // uid -> ZSET of msg_id:seq
	_prefixKeySession    = "key_sid:%s"           // connection key -> sid (reverse index for O(1) heartbeat)
	_prefixRetryCnt      = "retry_cnt:%s:%d:%d"   // topic:partition:offset -> retry count
	_prefixDeviceACKs    = "msg_acks:%s"          // msg_id -> device ACK records (hash)
)

var trackMessageScript = redis.NewScript(1, `
if redis.call("EXISTS", KEYS[1]) == 1 then
	return 0
end
redis.call("HSET", KEYS[1], unpack(ARGV, 2))
redis.call("EXPIRE", KEYS[1], ARGV[1])
return 1
`)

func keyMidServer(mid int64) string {
	return fmt.Sprintf(_prefixMidServer, mid)
}

func keyKeyServer(key string) string {
	return fmt.Sprintf(_prefixKeyServer, key)
}

func keyServerOnline(key string) string {
	return fmt.Sprintf(_prefixServerOnline, key)
}

func keySession(sid string) string {
	return fmt.Sprintf(_prefixSession, sid)
}

func keyUserSessions(uid int64) string {
	return fmt.Sprintf(_prefixUserSessions, uid)
}

func keyDeviceSession(uid int64, deviceID string) string {
	return fmt.Sprintf(_prefixDeviceSession, uid, deviceID)
}

func keyMsgStatus(msgID string) string {
	return fmt.Sprintf(_prefixMsgStatus, msgID)
}

func keyUserSeq(uid int64) string {
	return fmt.Sprintf(_prefixUserSeq, uid)
}

func keyOfflineQueue(uid int64) string {
	return fmt.Sprintf(_prefixOfflineQueue, uid)
}

func keyKeySession(key string) string {
	return fmt.Sprintf(_prefixKeySession, key)
}

func keyDeviceACKs(msgID string) string {
	return fmt.Sprintf(_prefixDeviceACKs, msgID)
}

// pingRedis check redis connection.
func (d *Dao) pingRedis(c context.Context) (err error) {
	conn := d.redis.Get()
	_, err = conn.Do("SET", "PING", "PONG")
	conn.Close()
	return
}

// AddMapping add a mapping.
// Mapping:
//
//	mid -> key_server
//	key -> server
func (d *Dao) AddMapping(c context.Context, mid int64, key, server string) (err error) {
	conn := d.redis.Get()
	defer conn.Close()
	var n = 2
	if mid > 0 {
		if err = conn.Send("HSET", keyMidServer(mid), key, server); err != nil {
			log.Errorf("conn.Send(HSET %d,%s,%s) error(%v)", mid, server, key, err)
			return
		}
		if err = conn.Send("EXPIRE", keyMidServer(mid), d.redisExpire); err != nil {
			log.Errorf("conn.Send(EXPIRE %d,%s,%s) error(%v)", mid, key, server, err)
			return
		}
		n += 2
	}
	if err = conn.Send("SET", keyKeyServer(key), server); err != nil {
		log.Errorf("conn.Send(HSET %d,%s,%s) error(%v)", mid, server, key, err)
		return
	}
	if err = conn.Send("EXPIRE", keyKeyServer(key), d.redisExpire); err != nil {
		log.Errorf("conn.Send(EXPIRE %d,%s,%s) error(%v)", mid, key, server, err)
		return
	}
	if err = conn.Flush(); err != nil {
		log.Errorf("conn.Flush() error(%v)", err)
		return
	}
	for i := 0; i < n; i++ {
		if _, err = conn.Receive(); err != nil {
			log.Errorf("conn.Receive() error(%v)", err)
			return
		}
	}
	return
}

// ExpireMapping expire a mapping.
func (d *Dao) ExpireMapping(c context.Context, mid int64, key string) (has bool, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	var n = 1
	if mid > 0 {
		if err = conn.Send("EXPIRE", keyMidServer(mid), d.redisExpire); err != nil {
			log.Errorf("conn.Send(EXPIRE %d,%s) error(%v)", mid, key, err)
			return
		}
		n++
	}
	if err = conn.Send("EXPIRE", keyKeyServer(key), d.redisExpire); err != nil {
		log.Errorf("conn.Send(EXPIRE %d,%s) error(%v)", mid, key, err)
		return
	}
	if err = conn.Flush(); err != nil {
		log.Errorf("conn.Flush() error(%v)", err)
		return
	}
	for i := 0; i < n; i++ {
		if has, err = redis.Bool(conn.Receive()); err != nil {
			log.Errorf("conn.Receive() error(%v)", err)
			return
		}
	}
	return
}

// DelMapping del a mapping.
func (d *Dao) DelMapping(c context.Context, mid int64, key, server string) (has bool, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	n := 1
	if mid > 0 {
		if err = conn.Send("HDEL", keyMidServer(mid), key); err != nil {
			log.Errorf("conn.Send(HDEL %d,%s,%s) error(%v)", mid, key, server, err)
			return
		}
		n++
	}
	if err = conn.Send("DEL", keyKeyServer(key)); err != nil {
		log.Errorf("conn.Send(HDEL %d,%s,%s) error(%v)", mid, key, server, err)
		return
	}
	if err = conn.Flush(); err != nil {
		log.Errorf("conn.Flush() error(%v)", err)
		return
	}
	for i := 0; i < n; i++ {
		if has, err = redis.Bool(conn.Receive()); err != nil {
			log.Errorf("conn.Receive() error(%v)", err)
			return
		}
	}
	return
}

// ServersByKeys get a server by key.
func (d *Dao) ServersByKeys(c context.Context, keys []string) (res []string, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	var args []interface{}
	for _, key := range keys {
		args = append(args, keyKeyServer(key))
	}
	if res, err = redis.Strings(conn.Do("MGET", args...)); err != nil {
		log.Errorf("conn.Do(MGET %v) error(%v)", args, err)
	}
	return
}

// KeysByMids get a key server by mid.
func (d *Dao) KeysByMids(c context.Context, mids []int64) (ress map[string]string, olMids []int64, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	ress = make(map[string]string)
	for _, mid := range mids {
		if err = conn.Send("HGETALL", keyMidServer(mid)); err != nil {
			log.Errorf("conn.Do(HGETALL %d) error(%v)", mid, err)
			return
		}
	}
	if err = conn.Flush(); err != nil {
		log.Errorf("conn.Flush() error(%v)", err)
		return
	}
	for idx := 0; idx < len(mids); idx++ {
		var (
			res map[string]string
		)
		if res, err = redis.StringMap(conn.Receive()); err != nil {
			log.Errorf("conn.Receive() error(%v)", err)
			return
		}
		if len(res) > 0 {
			olMids = append(olMids, mids[idx])
		}
		for k, v := range res {
			ress[k] = v
		}
	}
	return
}

// AddServerOnline add a server online.
func (d *Dao) AddServerOnline(c context.Context, server string, online *model.Online) (err error) {
	roomsMap := map[uint32]map[string]int32{}
	for room, count := range online.RoomCount {
		rMap := roomsMap[cityhash.CityHash32([]byte(room), uint32(len(room)))%64]
		if rMap == nil {
			rMap = make(map[string]int32)
			roomsMap[cityhash.CityHash32([]byte(room), uint32(len(room)))%64] = rMap
		}
		rMap[room] = count
	}
	key := keyServerOnline(server)
	for hashKey, value := range roomsMap {
		err = d.addServerOnline(c, key, strconv.FormatInt(int64(hashKey), 10), &model.Online{RoomCount: value, Server: online.Server, Updated: online.Updated})
		if err != nil {
			return
		}
	}
	return
}

func (d *Dao) addServerOnline(c context.Context, key string, hashKey string, online *model.Online) (err error) {
	conn := d.redis.Get()
	defer conn.Close()
	b, _ := json.Marshal(online)
	if err = conn.Send("HSET", key, hashKey, b); err != nil {
		log.Errorf("conn.Send(SET %s,%s) error(%v)", key, hashKey, err)
		return
	}
	if err = conn.Send("EXPIRE", key, d.redisExpire); err != nil {
		log.Errorf("conn.Send(EXPIRE %s) error(%v)", key, err)
		return
	}
	if err = conn.Flush(); err != nil {
		log.Errorf("conn.Flush() error(%v)", err)
		return
	}
	for i := 0; i < 2; i++ {
		if _, err = conn.Receive(); err != nil {
			log.Errorf("conn.Receive() error(%v)", err)
			return
		}
	}
	return
}

// ServerOnline get a server online.
func (d *Dao) ServerOnline(c context.Context, server string) (online *model.Online, err error) {
	online = &model.Online{RoomCount: map[string]int32{}}
	key := keyServerOnline(server)
	for i := 0; i < 64; i++ {
		ol, err := d.serverOnline(c, key, strconv.FormatInt(int64(i), 10))
		if err == nil && ol != nil {
			online.Server = ol.Server
			if ol.Updated > online.Updated {
				online.Updated = ol.Updated
			}
			for room, count := range ol.RoomCount {
				online.RoomCount[room] = count
			}
		}
	}
	return
}

func (d *Dao) serverOnline(c context.Context, key string, hashKey string) (online *model.Online, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	b, err := redis.Bytes(conn.Do("HGET", key, hashKey))
	if err != nil {
		if err != redis.ErrNil {
			log.Errorf("conn.Do(HGET %s %s) error(%v)", key, hashKey, err)
		}
		return
	}
	online = new(model.Online)
	if err = json.Unmarshal(b, online); err != nil {
		log.Errorf("serverOnline json.Unmarshal(%s) error(%v)", b, err)
		return
	}
	return
}

// DelServerOnline del a server online.
func (d *Dao) DelServerOnline(c context.Context, server string) (err error) {
	conn := d.redis.Get()
	defer conn.Close()
	key := keyServerOnline(server)
	if _, err = conn.Do("DEL", key); err != nil {
		log.Errorf("conn.Do(DEL %s) error(%v)", key, err)
	}
	return
}

// ============ Session Operations ============

// AddSession creates a new session in Redis.
func (d *Dao) AddSession(c context.Context, sid string, uid int64, key, deviceID, platform, server string) (err error) {
	conn := d.redis.Get()
	defer conn.Close()
	sKey := keySession(sid)
	expire := d.redisExpire
	// HSET session:{sid}
	fields := map[string]interface{}{
		"uid":        uid,
		"key":        key,
		"device_id":  deviceID,
		"platform":   platform,
		"server":     server,
		"created_at": time.Now().UnixMilli(),
		"last_hb":    time.Now().UnixMilli(),
	}
	args := []interface{}{sKey}
	for k, v := range fields {
		args = append(args, k, v)
	}
	if err = conn.Send("HSET", args...); err != nil {
		log.Errorf("conn.Send(HSET %s) error(%v)", sKey, err)
		return
	}
	if err = conn.Send("EXPIRE", sKey, expire); err != nil {
		log.Errorf("conn.Send(EXPIRE %s) error(%v)", sKey, err)
		return
	}
	// HSET user_sessions:{uid} sid "device_id:platform"
	if err = conn.Send("HSET", keyUserSessions(uid), sid, deviceID+":"+platform); err != nil {
		log.Errorf("conn.Send(HSET user_sessions) error(%v)", err)
		return
	}
	if err = conn.Send("EXPIRE", keyUserSessions(uid), expire); err != nil {
		log.Errorf("conn.Send(EXPIRE user_sessions) error(%v)", err)
		return
	}
	// SET device_session:{uid}:{device_id} sid (for same-device kick)
	if err = conn.Send("SET", keyDeviceSession(uid, deviceID), sid, "EX", expire); err != nil {
		log.Errorf("conn.Send(SET device_session) error(%v)", err)
		return
	}
	// SET key_sid:{key} sid (reverse index for O(1) heartbeat lookup)
	if err = conn.Send("SET", keyKeySession(key), sid, "EX", expire); err != nil {
		log.Errorf("conn.Send(SET key_sid) error(%v)", err)
		return
	}
	if err = conn.Flush(); err != nil {
		log.Errorf("conn.Flush() error(%v)", err)
		return
	}
	for i := 0; i < 6; i++ {
		if _, err = conn.Receive(); err != nil {
			log.Errorf("conn.Receive() error(%v)", err)
			return
		}
	}
	return
}

// GetSession gets session metadata by sid.
func (d *Dao) GetSession(c context.Context, sid string) (res map[string]string, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if res, err = redis.StringMap(conn.Do("HGETALL", keySession(sid))); err != nil {
		log.Errorf("conn.Do(HGETALL %s) error(%v)", sid, err)
	}
	return
}

// GetSessionByKey returns the sid for a connection key via the reverse index.
func (d *Dao) GetSessionByKey(c context.Context, key string) (sid string, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if sid, err = redis.String(conn.Do("GET", keyKeySession(key))); err != nil {
		if err == redis.ErrNil {
			err = nil
		} else {
			log.Errorf("conn.Do(GET key_sid:%s) error(%v)", key, err)
		}
	}
	return
}

// GetUserSessions gets all session IDs for a user.
func (d *Dao) GetUserSessions(c context.Context, uid int64) (res map[string]string, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if res, err = redis.StringMap(conn.Do("HGETALL", keyUserSessions(uid))); err != nil {
		log.Errorf("conn.Do(HGETALL %s) error(%v)", keyUserSessions(uid), err)
	}
	return
}

// GetDeviceSession gets the session ID for a specific device.
func (d *Dao) GetDeviceSession(c context.Context, uid int64, deviceID string) (sid string, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if sid, err = redis.String(conn.Do("GET", keyDeviceSession(uid, deviceID))); err != nil {
		if err == redis.ErrNil {
			err = nil
		}
	}
	return
}

// DelSession deletes a session.
func (d *Dao) DelSession(c context.Context, sid string, uid int64, deviceID, key string) (err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if err = conn.Send("DEL", keySession(sid)); err != nil {
		return
	}
	if uid > 0 {
		if err = conn.Send("HDEL", keyUserSessions(uid), sid); err != nil {
			return
		}
	}
	if deviceID != "" {
		if err = conn.Send("DEL", keyDeviceSession(uid, deviceID)); err != nil {
			return
		}
	}
	if key != "" {
		if err = conn.Send("DEL", keyKeySession(key)); err != nil {
			return
		}
	}
	if err = conn.Flush(); err != nil {
		return
	}
	n := 1
	if uid > 0 {
		n++
	}
	if deviceID != "" {
		n++
	}
	if key != "" {
		n++
	}
	for i := 0; i < n; i++ {
		if _, err = conn.Receive(); err != nil {
			return
		}
	}
	return
}

// ExpireSession refreshes the TTL of a session.
func (d *Dao) ExpireSession(c context.Context, sid string, uid int64) (err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if err = conn.Send("EXPIRE", keySession(sid), d.redisExpire); err != nil {
		return
	}
	if uid > 0 {
		if err = conn.Send("EXPIRE", keyUserSessions(uid), d.redisExpire); err != nil {
			return
		}
	}
	if err = conn.Flush(); err != nil {
		return
	}
	receives := 1
	if uid > 0 {
		receives = 2
	}
	for i := 0; i < receives; i++ {
		if _, err = conn.Receive(); err != nil {
			return
		}
	}
	return
}

// ============ Message ACK Operations ============

// SetMessageStatus stores message status and metadata.
func (d *Dao) SetMessageStatus(c context.Context, msgID string, fields map[string]interface{}) (err error) {
	conn := d.redis.Get()
	defer conn.Close()
	args := []interface{}{keyMsgStatus(msgID)}
	for k, v := range fields {
		args = append(args, k, v)
	}
	if err = conn.Send("HSET", args...); err != nil {
		log.Errorf("conn.Send(HSET msg:%s) error(%v)", msgID, err)
		return
	}
	// TTL 7 days
	if err = conn.Send("EXPIRE", keyMsgStatus(msgID), 7*24*3600); err != nil {
		return
	}
	if err = conn.Flush(); err != nil {
		return
	}
	for i := 0; i < 2; i++ {
		if _, err = conn.Receive(); err != nil {
			return
		}
	}
	return
}

// TrackMessageAtomic stores a complete message status hash only when the
// message key does not already exist. The existence check, HSET, and EXPIRE
// run in one Redis script so readers never observe a partially initialized
// message.
func (d *Dao) TrackMessageAtomic(c context.Context, msgID string, fields map[string]interface{}) (added bool, err error) {
	if len(fields) == 0 {
		return false, nil
	}
	conn := d.redis.Get()
	defer conn.Close()

	args := []interface{}{keyMsgStatus(msgID), 7 * 24 * 3600}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, k, fields[k])
	}
	n, err := redis.Int(trackMessageScript.Do(conn, args...))
	if err != nil {
		log.Errorf("trackMessageScript msg:%s error(%v)", msgID, err)
		return false, err
	}
	return n == 1, nil
}

// SetMessageStatusNX sets a single field on a message status hash only if it does not already exist (HSETNX).
// Returns true if the field was set, false if it already existed.
func (d *Dao) SetMessageStatusNX(c context.Context, msgID, field string, value interface{}) (added bool, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if added, err = redis.Bool(conn.Do("HSETNX", keyMsgStatus(msgID), field, value)); err != nil {
		log.Errorf("conn.Do(HSETNX msg:%s %s) error(%v)", msgID, field, err)
	}
	return
}

// GetMessageStatus gets message status.
func (d *Dao) GetMessageStatus(c context.Context, msgID string) (res map[string]string, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if res, err = redis.StringMap(conn.Do("HGETALL", keyMsgStatus(msgID))); err != nil {
		log.Errorf("conn.Do(HGETALL msg:%s) error(%v)", msgID, err)
	}
	return
}

// BatchGetMessageStatus fetches multiple message status hashes using a Redis
// pipeline. The result slice keeps the same order as msgIDs.
func (d *Dao) BatchGetMessageStatus(c context.Context, msgIDs []string) ([]map[string]string, error) {
	if len(msgIDs) == 0 {
		return nil, nil
	}
	conn := d.redis.Get()
	defer conn.Close()

	for _, msgID := range msgIDs {
		if err := conn.Send("HGETALL", keyMsgStatus(msgID)); err != nil {
			log.Errorf("conn.Send(HGETALL msg:%s) error(%v)", msgID, err)
			return nil, err
		}
	}
	if err := conn.Flush(); err != nil {
		return nil, err
	}

	res := make([]map[string]string, 0, len(msgIDs))
	for _, msgID := range msgIDs {
		data, err := redis.StringMap(conn.Receive())
		if err != nil {
			log.Errorf("conn.Receive(HGETALL msg:%s) error(%v)", msgID, err)
			return nil, err
		}
		res = append(res, data)
	}
	return res, nil
}

// UpdateMessageStatus updates the status field of a message.
func (d *Dao) UpdateMessageStatus(c context.Context, msgID, status string) (err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if _, err = conn.Do("HSET", keyMsgStatus(msgID), "status", status, "updated_at", time.Now().UnixMilli()); err != nil {
		log.Errorf("conn.Do(HSET msg:%s status) error(%v)", msgID, err)
	}
	return
}

// ============ Sequence Operations ============

// IncrUserSeq increments and returns the user's message sequence number.
func (d *Dao) IncrUserSeq(c context.Context, uid int64) (seq int64, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if seq, err = redis.Int64(conn.Do("INCR", keyUserSeq(uid))); err != nil {
		log.Errorf("conn.Do(INCR %s) error(%v)", keyUserSeq(uid), err)
	}
	return
}

// ============ Offline Queue Operations ============

// AddToOfflineQueue adds a message to the user's offline queue (ZSET).
func (d *Dao) AddToOfflineQueue(c context.Context, uid int64, msgID string, seq float64) (err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if _, err = conn.Do("ZADD", keyOfflineQueue(uid), seq, msgID); err != nil {
		log.Errorf("conn.Do(ZADD offline:%d %s) error(%v)", uid, msgID, err)
		return
	}
	// Set TTL to match message status expiry (7 days) to prevent unbounded growth
	if _, err = conn.Do("EXPIRE", keyOfflineQueue(uid), 7*24*3600); err != nil {
		log.Errorf("conn.Do(EXPIRE offline:%d) error(%v)", uid, err)
	}
	return
}

// GetOfflineQueue gets messages from the offline queue with seq > lastSeq.
func (d *Dao) GetOfflineQueue(c context.Context, uid int64, lastSeq float64, limit int) (msgIDs []string, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if msgIDs, err = redis.Strings(conn.Do("ZRANGEBYSCORE", keyOfflineQueue(uid),
		fmt.Sprintf("(%f", lastSeq), "+inf", "LIMIT", 0, limit)); err != nil {
		if err == redis.ErrNil {
			err = nil
		} else {
			log.Errorf("conn.Do(ZRANGEBYSCORE offline:%d) error(%v)", uid, err)
		}
	}
	return
}

// RemoveFromOfflineQueue removes a message from the offline queue.
func (d *Dao) RemoveFromOfflineQueue(c context.Context, uid int64, msgID string) (err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if _, err = conn.Do("ZREM", keyOfflineQueue(uid), msgID); err != nil {
		log.Errorf("conn.Do(ZREM offline:%d %s) error(%v)", uid, msgID, err)
	}
	return
}

// GetOfflineQueueSize returns the number of offline messages for a user.
func (d *Dao) GetOfflineQueueSize(c context.Context, uid int64) (size int64, err error) {
	conn := d.redis.Get()
	defer conn.Close()
	if size, err = redis.Int64(conn.Do("ZCARD", keyOfflineQueue(uid))); err != nil {
		log.Errorf("conn.Do(ZCARD offline:%d) error(%v)", uid, err)
	}
	return
}

// CountActiveUsers returns the number of users that currently have at least one
// active session hash entry.
func (d *Dao) CountActiveUsers(c context.Context) (count int64, err error) {
	return d.countNonEmptyKeys(c, "user_sessions:*", "HLEN")
}

// CountOfflinePending returns the total number of messages pending in all user
// offline queues.
func (d *Dao) CountOfflinePending(c context.Context) (count int64, err error) {
	return d.countNonEmptyKeys(c, "offline:*", "ZCARD")
}

func (d *Dao) countNonEmptyKeys(c context.Context, pattern, countCommand string) (count int64, err error) {
	conn := d.redis.Get()
	defer conn.Close()

	cursor := 0
	for {
		select {
		case <-c.Done():
			return count, c.Err()
		default:
		}

		var keys []string
		values, scanErr := redis.Values(conn.Do("SCAN", cursor, "MATCH", pattern, "COUNT", 100))
		if scanErr != nil {
			log.Errorf("conn.Do(SCAN %s) error(%v)", pattern, scanErr)
			return count, scanErr
		}
		if _, scanErr = redis.Scan(values, &cursor, &keys); scanErr != nil {
			log.Errorf("redis.Scan(%s) error(%v)", pattern, scanErr)
			return count, scanErr
		}

		for _, key := range keys {
			n, countErr := redis.Int64(conn.Do(countCommand, key))
			if countErr != nil {
				log.Errorf("conn.Do(%s %s) error(%v)", countCommand, key, countErr)
				return count, countErr
			}
			if countCommand == "HLEN" {
				if n > 0 {
					count++
				}
			} else {
				count += n
			}
		}
		if cursor == 0 {
			return count, nil
		}
	}
}

// ============ Retry Queue Operations ============

// IncrMessageRetryCount atomically increments the retry counter stored in the
// message status hash. Returns the new count value.
func (d *Dao) IncrMessageRetryCount(c context.Context, msgID string) (int64, error) {
	conn := d.redis.Get()
	defer conn.Close()
	return redis.Int64(conn.Do("HINCRBY", keyMsgStatus(msgID), "retry_cnt", 1))
}

// Incr increments the Kafka-offset-based retry counter. Implements mq.RetryCounter.
// The counter auto-expires after 1 hour to prevent unbounded key accumulation.
func (d *Dao) Incr(ctx context.Context, topic string, partition int32, offset int64) (int64, error) {
	conn := d.redis.Get()
	defer conn.Close()
	key := fmt.Sprintf(_prefixRetryCnt, topic, partition, offset)
	return redis.Int64(retryCounterIncrScript.Do(conn, key, 3600))
}

// ============ Device-Level ACK Operations ============

// RecordDeviceACK stores a device-level ACK record for a message.
// Uses msg_acks:{msg_id} hash with device_id as field and JSON {session_id, ack_time} as value.
// TTL matches message status expiry (7 days).
func (d *Dao) RecordDeviceACK(c context.Context, msgID, deviceID, sessionID string, ackTime int64) error {
	conn := d.redis.Get()
	defer conn.Close()

	value := fmt.Sprintf(`{"session_id":"%s","ack_time":%d}`, sessionID, ackTime)
	key := keyDeviceACKs(msgID)
	if err := conn.Send("HSET", key, deviceID, value); err != nil {
		log.Errorf("conn.Send(HSET %s %s) error(%v)", key, deviceID, err)
		return err
	}
	if err := conn.Send("EXPIRE", key, 7*24*3600); err != nil {
		return err
	}
	if err := conn.Flush(); err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		if _, err := conn.Receive(); err != nil {
			return err
		}
	}
	return nil
}

// GetDeviceACKs returns all device ACK records for a message.
func (d *Dao) GetDeviceACKs(c context.Context, msgID string) (map[string]string, error) {
	conn := d.redis.Get()
	defer conn.Close()
	res, err := redis.StringMap(conn.Do("HGETALL", keyDeviceACKs(msgID)))
	if err != nil {
		if err == redis.ErrNil {
			return nil, nil
		}
		log.Errorf("conn.Do(HGETALL %s) error(%v)", keyDeviceACKs(msgID), err)
	}
	return res, err
}
