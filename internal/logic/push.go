package logic

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	routerpb "github.com/Terry-Mao/goim/api/router"
	"github.com/Terry-Mao/goim/internal/logic/model"

	log "github.com/Terry-Mao/goim/pkg/log"
)

// DeliveryResult explains the concrete path selected for one push request.
type DeliveryResult struct {
	MsgID        string  `json:"msg_id"`
	Path         string  `json:"path"`
	TargetNode   string  `json:"target_node,omitempty"`
	ErrorCode    string  `json:"error_code,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	LatencyMs    float64 `json:"latency_ms"`
}

// PushKeys push a message by connection keys.
// Resolves key -> server via session, pushes to the specific key (not all
// sessions of the uid). Body is wrapped as MsgBody so the client can parse
// msg_id and ACK.
// Returns the server-generated msgID(s) so the caller can track delivery.
func (l *Logic) PushKeys(c context.Context, op int32, keys []string, msg []byte) (msgIDs []string, err error) {
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
	msgIDs = append(msgIDs, msgID)
	body := wrapAsMsgBody(msgID, uid, msg)
	for server, skeys := range pushKeys {
		if err = l.dao.PushViaMQ(c, op, server, skeys, body); err != nil {
			return msgIDs, err
		}
	}
	return msgIDs, nil
}

// PushMids push a message by user IDs.
// Routes through the standalone Router service for delivery tracking,
// and offline queue support.
// The raw body is wrapped as MsgBody so the client can parse msg_id and ACK.
// Returns server-generated msgIDs for delivery tracking.
func (l *Logic) PushMids(c context.Context, op int32, mids []int64, msg []byte) (msgIDs []string, err error) {
	results, err := l.PushMidsDetailed(c, op, mids, msg)
	for _, result := range results {
		msgIDs = append(msgIDs, result.MsgID)
	}
	return msgIDs, err
}

// PushMidsDetailed push a message by user IDs and returns per-user delivery path details.
func (l *Logic) PushMidsDetailed(c context.Context, op int32, mids []int64, msg []byte) (results []DeliveryResult, err error) {
	var firstErr error
	for _, mid := range mids {
		msgID := l.GenerateMsgID()
		body := wrapAsMsgBody(msgID, mid, msg)
		routerCtx, cancel := l.routerRPCContext(c)
		reply, e := l.routerClient.RouteByUser(routerCtx, &routerpb.RouteByUserReq{
			MsgId: msgID,
			ToUid: mid,
			Op:    op,
			Body:  body,
		})
		cancel()
		result := deliveryResultFromReply(reply, msgID)
		if e == nil && result.ErrorCode != "" {
			e = fmt.Errorf("router route by user failed: %s: %s", result.ErrorCode, result.ErrorMessage)
		}
		if result.MsgID == "" {
			result.MsgID = msgID
		}
		results = append(results, result)
		if e != nil {
			log.Warningf("push mid:%d msg_id:%s failed: %v", mid, msgID, e)
			if firstErr == nil {
				firstErr = e
			}
		}
	}
	return results, firstErr
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
// Returns the server-generated msgID.
func (l *Logic) PushRoom(c context.Context, op int32, typ, room string, msg []byte) (msgID string, err error) {
	roomKey := model.EncodeRoomKey(typ, room)
	msgID = l.GenerateMsgID()
	body := wrapAsMsgBody(msgID, 0, msg)
	routerCtx, cancel := l.routerRPCContext(c)
	defer cancel()
	_, err = l.routerClient.RouteByRoom(routerCtx, &routerpb.RouteByRoomReq{Op: op, RoomKey: roomKey, Body: body})
	return msgID, err
}

// PushAll push a message to all.
// Body is wrapped as MsgBody so the client can parse msg_id.
// Returns the server-generated msgID.
func (l *Logic) PushAll(c context.Context, op, speed int32, msg []byte) (msgID string, err error) {
	msgID = l.GenerateMsgID()
	body := wrapAsMsgBody(msgID, 0, msg)
	routerCtx, cancel := l.routerRPCContext(c)
	defer cancel()
	_, err = l.routerClient.RouteBroadcast(routerCtx, &routerpb.RouteBroadcastReq{Op: op, Speed: speed, Body: body})
	return msgID, err
}

func deliveryResultFromReply(reply *routerpb.RouteByUserReply, fallbackMsgID string) DeliveryResult {
	if reply == nil {
		return DeliveryResult{MsgID: fallbackMsgID, Path: "failed", ErrorCode: "empty_router_reply"}
	}
	msgID := reply.MsgId
	if msgID == "" {
		msgID = fallbackMsgID
	}
	return DeliveryResult{
		MsgID:        msgID,
		Path:         reply.Path,
		TargetNode:   reply.TargetNode,
		ErrorCode:    reply.ErrorCode,
		ErrorMessage: reply.ErrorMessage,
		LatencyMs:    reply.LatencyMs,
	}
}
