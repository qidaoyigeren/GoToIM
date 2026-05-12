package logic

import (
	"context"

	"github.com/Terry-Mao/goim/internal/logic/model"

	log "github.com/Terry-Mao/goim/pkg/log"
)

// PushKeys push a message by connection keys.
// Uses Session system to resolve key -> server mapping.
func (l *Logic) PushKeys(c context.Context, op int32, keys []string, msg []byte) (err error) {
	pushKeys := make(map[string][]string)
	for _, key := range keys {
		if key == "" {
			continue
		}
		sid, err := l.dao.GetSessionByKey(c, key)
		if err != nil || sid == "" {
			log.Warningf("push key:%s session not found: err=%v", key, err)
			continue
		}
		sess, err := l.dao.GetSession(c, sid)
		if err != nil || len(sess) == 0 {
			continue
		}
		server := sess["server"]
		if server != "" {
			pushKeys[server] = append(pushKeys[server], key)
		}
	}
	for server, skeys := range pushKeys {
		if err = l.dao.PushMsg(c, op, server, skeys, msg); err != nil {
			return
		}
	}
	return
}

// PushMids push a message by user IDs.
// Uses Session system to resolve uid -> server + keys mapping.
func (l *Logic) PushMids(c context.Context, op int32, mids []int64, msg []byte) (err error) {
	keys := make(map[string][]string)
	for _, mid := range mids {
		sessions, err := l.sessionMgr.GetSessions(c, mid)
		if err != nil || len(sessions) == 0 {
			log.Warningf("push mid:%d no active sessions", mid)
			continue
		}
		for _, sess := range sessions {
			if sess.Server != "" && sess.Key != "" {
				keys[sess.Server] = append(keys[sess.Server], sess.Key)
			}
		}
	}
	for server, skeys := range keys {
		if err = l.dao.PushMsg(c, op, server, skeys, msg); err != nil {
			return
		}
	}
	return
}

// PushRoom push a message by room.
func (l *Logic) PushRoom(c context.Context, op int32, typ, room string, msg []byte) (err error) {
	roomKey := model.EncodeRoomKey(typ, room)
	return l.router.RouteByRoom(c, op, roomKey, msg)
}

// PushAll push a message to all.
func (l *Logic) PushAll(c context.Context, op, speed int32, msg []byte) (err error) {
	return l.router.RouteBroadcast(c, op, speed, msg)
}
