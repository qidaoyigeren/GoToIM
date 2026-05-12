package logic

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/model"
	"github.com/Terry-Mao/goim/internal/logic/service"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/google/uuid"
)

// Connect connected a conn.
func (l *Logic) Connect(c context.Context, server, cookie string, token []byte) (mid int64, key, roomID string, accepts []int32, hb int64, err error) {
	var params struct {
		Mid      int64   `json:"mid"`
		Key      string  `json:"key"`
		RoomID   string  `json:"room_id"`
		Platform string  `json:"platform"`
		DeviceID string  `json:"device_id"`
		Accepts  []int32 `json:"accepts"`
		LastSeq  int64   `json:"last_seq"` // client's last known seq for incremental sync
	}
	if err = json.Unmarshal(token, &params); err != nil {
		log.Errorf("json.Unmarshal(%s) error(%v)", token, err)
		return
	}
	mid = params.Mid
	roomID = params.RoomID
	accepts = params.Accepts
	hb = int64(l.c.Node.Heartbeat) * int64(l.c.Node.HeartbeatMax)
	if key = params.Key; key == "" {
		key = uuid.New().String()
	}

	// Create session with device tracking
	if params.DeviceID == "" {
		params.DeviceID = params.Platform + ":" + key
	}
	sess := &service.Session{
		SID:       uuid.New().String(),
		UID:       mid,
		Key:       key,
		DeviceID:  params.DeviceID,
		Platform:  params.Platform,
		Server:    server,
		CreatedAt: time.Now(),
		LastHBAt:  time.Now(),
	}
	if err = l.sessionMgr.Create(c, sess); err != nil {
		log.Errorf("session create(%d,%s,%s) error(%v)", mid, key, server, err)
		return
	}

	log.Infof("conn connected key:%s server:%s mid:%d device:%s platform:%s", key, server, mid, params.DeviceID, params.Platform)

	// Trigger offline message sync in background (non-blocking)
	go func() {
		syncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := l.syncSvc.OnUserOnline(syncCtx, mid, params.LastSeq); err != nil {
			log.Warningf("on user online sync failed: mid=%d err=%v", mid, err)
		}
	}()
	return
}

// Disconnect disconnect a conn.
func (l *Logic) Disconnect(c context.Context, mid int64, key, server string) (has bool, err error) {
	if sessErr := l.sessionMgr.Disconnect(c, mid, key, server); sessErr != nil {
		log.Warningf("session disconnect(%d,%s,%s) error(%v)", mid, key, server, sessErr)
		err = sessErr
		return
	}
	has = true
	log.Infof("conn disconnected key:%s server:%s mid:%d", key, server, mid)
	return
}

// Heartbeat heartbeat a conn.
func (l *Logic) Heartbeat(c context.Context, mid int64, key, server string) (err error) {
	if err = l.sessionMgr.Heartbeat(c, key, mid); err != nil {
		log.Warningf("session heartbeat(%d,%s,%s) error(%v)", mid, key, server, err)
		return
	}
	log.Infof("conn heartbeat key:%s server:%s mid:%d", key, server, mid)
	return
}

// RenewOnline renew a server online.
func (l *Logic) RenewOnline(c context.Context, server string, roomCount map[string]int32) (map[string]int32, error) {
	online := &model.Online{
		Server:    server,
		RoomCount: roomCount,
		Updated:   time.Now().Unix(),
	}
	if err := l.dao.AddServerOnline(context.Background(), server, online); err != nil {
		return nil, err
	}
	l.nodesMu.RLock()
	rc := l.roomCount
	l.nodesMu.RUnlock()
	return rc, nil
}

// Receive receive a message from Comet. Handles ACK and Sync operations.
func (l *Logic) Receive(c context.Context, mid int64, p *protocol.Proto) (err error) {
	switch p.Op {
	case protocol.OpPushMsgAck:
		// Client ACK for a push message
		ack, err := protocol.UnmarshalAckBody(p.Body)
		if err != nil {
			log.Errorf("unmarshal ack body error(%v)", err)
			return err
		}
		if err := l.router.HandleACK(c, mid, ack.MsgID); err != nil {
			log.Errorf("handle ack error(%v) mid:%d msg_id:%s", err, mid, ack.MsgID)
			return err
		}
		log.Infof("ack received mid:%d msg_id:%s seq:%d", mid, ack.MsgID, ack.Seq)

	case protocol.OpSyncReq:
		// Client sync request for offline messages
		syncReq, err := protocol.UnmarshalSyncReq(p.Body)
		if err != nil {
			log.Errorf("unmarshal sync req error(%v)", err)
			return err
		}
		reply, err := l.syncSvc.GetOfflineMessages(c, mid, syncReq.LastSeq, syncReq.Limit)
		if err != nil {
			log.Errorf("sync offline error(%v) mid:%d last_seq:%d", err, mid, syncReq.LastSeq)
			return err
		}
		// Marshal reply and set it as the proto body for Comet to write back
		replyBytes, err := protocol.MarshalSyncReply(reply)
		if err != nil {
			log.Errorf("marshal sync reply error(%v)", err)
			return err
		}
		p.Op = protocol.OpSyncReply
		p.Body = replyBytes
		log.Infof("sync request mid:%d last_seq:%d limit:%d msgs=%d", mid, syncReq.LastSeq, syncReq.Limit, len(reply.Messages))

	default:
		log.Infof("receive mid:%d message:%+v", mid, p)
	}
	return
}
