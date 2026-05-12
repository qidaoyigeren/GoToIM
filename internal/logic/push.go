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
// Resolves key -> uid via session, then routes through DispatchEngine
// for msg_id generation, delivery tracking, and offline queue support.
// The raw body is wrapped as MsgBody so the client can parse msg_id and ACK.
func (l *Logic) PushKeys(c context.Context, op int32, keys []string, msg []byte) (err error) {
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
		uidStr, ok := sess["uid"]
		if !ok || uidStr == "" {
			log.Warningf("push key:%s session has no uid", key)
			continue
		}
		uid, _ := strconv.ParseInt(uidStr, 10, 64)
		if uid == 0 {
			continue
		}
		msgID := l.GenerateMsgID()
		body := wrapAsMsgBody(msgID, uid, msg)
		if e := l.router.RouteByUser(c, msgID, uid, op, body, 0); e != nil {
			log.Warningf("push key:%s uid:%d failed: %v", key, uid, e)
		}
	}
	return nil
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
func (l *Logic) PushRoom(c context.Context, op int32, typ, room string, msg []byte) (err error) {
	roomKey := model.EncodeRoomKey(typ, room)
	return l.router.RouteByRoom(c, op, roomKey, msg)
}

// PushAll push a message to all.
func (l *Logic) PushAll(c context.Context, op, speed int32, msg []byte) (err error) {
	return l.router.RouteBroadcast(c, op, speed, msg)
}
