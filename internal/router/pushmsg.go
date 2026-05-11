package router

import (
	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/golang/protobuf/proto"
)

// pushMsgBytes serializes a pb.PushMsg to bytes.
// Kept as a helper so the serialization format matches what the Job consumer expects.
func pushMsgBytes(typ int32, op int32, server string, keys []string, room string, msg []byte, speed int32) []byte {
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_Type(typ),
		Operation: op,
		Server:    server,
		Keys:      keys,
		Room:      room,
		Msg:       msg,
		Speed:     speed,
	}
	b, _ := proto.Marshal(pushMsg)
	return b
}
