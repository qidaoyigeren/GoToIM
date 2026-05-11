package worker

import (
	"github.com/Terry-Mao/goim/api/comet"
	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/pkg/bytes"

	log "github.com/Terry-Mao/goim/pkg/log"
)

// pushKeys pushes a message to specific connection keys on a Comet server.
func (w *DeliveryWorker) pushKeys(operation int32, serverID string, subKeys []string, body []byte) error {
	buf := bytes.NewWriterSize(len(body) + 64)
	p := &protocol.Proto{
		Ver:  1,
		Op:   operation,
		Body: body,
	}
	p.WriteTo(buf)
	p.Body = buf.Buffer()
	p.Op = protocol.OpRaw

	args := comet.PushMsgReq{
		Keys:    subKeys,
		ProtoOp: operation,
		Proto:   p,
	}
	if err := w.comets.Push(serverID, &args); err != nil {
		log.Errorf("pushKeys server:%s error(%v)", serverID, err)
		return err
	}
	log.Infof("pushKey:%s keys:%d", serverID, len(subKeys))
	return nil
}

// broadcast sends a message to all Comet servers.
func (w *DeliveryWorker) broadcast(operation int32, body []byte, speed int32) error {
	buf := bytes.NewWriterSize(len(body) + 64)
	p := &protocol.Proto{
		Ver:  1,
		Op:   operation,
		Body: body,
	}
	p.WriteTo(buf)
	p.Body = buf.Buffer()
	p.Op = protocol.OpRaw

	n := w.comets.Len()
	if n > 0 {
		speed /= int32(n)
	}
	args := comet.BroadcastReq{
		ProtoOp: operation,
		Proto:   p,
		Speed:   speed,
	}
	if err := w.comets.Broadcast(&args); err != nil {
		log.Errorf("broadcast error(%v)", err)
		return err
	}
	log.Infof("broadcast comets:%d", n)
	return nil
}

// broadcastRoomRawBytes sends room batch bytes to all Comet servers.
func (w *DeliveryWorker) broadcastRoomRawBytes(roomID string, body []byte) error {
	args := comet.BroadcastRoomReq{
		RoomID: roomID,
		Proto: &protocol.Proto{
			Ver:  1,
			Op:   protocol.OpRaw,
			Body: body,
		},
	}
	if err := w.comets.BroadcastRoom(&args); err != nil {
		log.Errorf("broadcastRoom room:%s error(%v)", roomID, err)
		return err
	}
	log.Infof("broadcastRoom room:%s", roomID)
	return nil
}
