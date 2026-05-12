package logic

import (
	"context"
	"strconv"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/model"

	log "github.com/Terry-Mao/goim/pkg/log"
)

// PushKeys push a message by connection keys.
// Resolves key -> server via session, pushes to the specific key (not all
// sessions of the uid). Body is wrapped as MsgBody so the client can parse
// msg_id and ACK.
func (l *Logic) PushKeys(c context.Context, op int32, keys []string, msg []byte) (err error) {
	pushKeys := make(map[string][]string)
	var uid int64
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
		if uid == 0 {
			if v, ok := sess["uid"]; ok && v != "" {
				uid, _ = strconv.ParseInt(v, 10, 64)
			}
		}
	}
	msgID := l.GenerateMsgID()
	body := wrapAsMsgBody(msgID, uid, msg)
	for server, skeys := range pushKeys {
		if err = l.dao.PushMsg(c, op, server, skeys, body); err != nil {
			return
		}
	}
	return
}

// PushMids push a message by user IDs.
// Routes through DispatchEngine for msg_id generation, delivery tracking,
// and offline queue support.
// The raw body is wrapped as MsgBody so the client can parse msg_id and ACK.
func (l *Logic) PushMids(c context.Context, op int32, mids []int64, msg []byte) (err error) {
	for _, mid := range mids {
		msgID := l.GenerateMsgID()
		body := wrapAsMsgBody(msgID, mid, msg)
		if e := l.router.RouteByUser(c, msgID, mid, op, body, 0); e != nil {
			log.Warningf("push mid:%d msg_id:%s failed: %v", mid, msgID, e)
		}
	}
	return nil
}

// wrapAsMsgBody wraps raw bytes into a MsgBody with the given msgID and toUID.
func wrapAsMsgBody(msgID string, toUID int64, content []byte) []byte {
	mb := &protocol.MsgBody{
		MsgID:     msgID,
		ToUID:     toUID,
		Timestamp: time.Now().UnixMilli(),
		Content:   content,
	}
	body, err := protocol.MarshalMsgBody(mb)
	if err != nil {
		log.Warningf("marshal MsgBody failed: %v", err)
		return content
	}
	return body
}

// PushRoom push a message by room.
// Body is wrapped as MsgBody so the client can parse msg_id.
func (l *Logic) PushRoom(c context.Context, op int32, typ, room string, msg []byte) (err error) {
	roomKey := model.EncodeRoomKey(typ, room)
	body := wrapAsMsgBody(l.GenerateMsgID(), 0, msg)
	return l.router.RouteByRoom(c, op, roomKey, body)
}

// PushAll push a message to all.
// Body is wrapped as MsgBody so the client can parse msg_id.
func (l *Logic) PushAll(c context.Context, op, speed int32, msg []byte) (err error) {
	body := wrapAsMsgBody(l.GenerateMsgID(), 0, msg)
	return l.router.RouteBroadcast(c, op, speed, body)
}
