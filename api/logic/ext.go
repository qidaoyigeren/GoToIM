package logic

import (
	context "context"

	"github.com/Terry-Mao/goim/api/protocol"
	grpc "google.golang.org/grpc"
)

// AckReq is the request for acknowledging a message.
type AckReq struct {
	Mid   int64
	MsgID string
	Seq   int64
}

// AckReply is the response for acknowledging a message.
type AckReply struct{}

// SyncOfflineReq is the request for syncing offline messages.
type SyncOfflineReq struct {
	Mid     int64
	LastSeq int64
	Limit   int32
}

// SyncOfflineReply is the response for syncing offline messages.
type SyncOfflineReply struct {
	CurrentSeq int64
	HasMore    bool
}

// LogicClientExt extends LogicClient with new RPCs for ACK and Sync.
type LogicClientExt interface {
	LogicClient
	AckMessage(ctx context.Context, in *AckReq, opts ...grpc.CallOption) (*AckReply, error)
	SyncOffline(ctx context.Context, in *SyncOfflineReq, opts ...grpc.CallOption) (*SyncOfflineReply, error)
}

type logicClientExt struct {
	LogicClient
	cc *grpc.ClientConn
}

// NewLogicClientExt creates an extended Logic client.
func NewLogicClientExt(cc *grpc.ClientConn) LogicClientExt {
	return &logicClientExt{
		LogicClient: NewLogicClient(cc),
		cc:          cc,
	}
}

// AckMessage sends an ACK for a message via the Receive RPC.
func (c *logicClientExt) AckMessage(ctx context.Context, in *AckReq, opts ...grpc.CallOption) (*AckReply, error) {
	ackBody := &protocol.AckBody{
		MsgID: in.MsgID,
		Seq:   in.Seq,
	}
	body, err := protocol.MarshalAckBody(ackBody)
	if err != nil {
		return nil, err
	}
	_, err = c.Receive(ctx, &ReceiveReq{
		Mid: in.Mid,
		Proto: &protocol.Proto{
			Ver:  1,
			Op:   protocol.OpPushMsgAck,
			Body: body,
		},
	}, opts...)
	if err != nil {
		return nil, err
	}
	return &AckReply{}, nil
}

// SyncOffline requests offline message sync via the Receive RPC.
func (c *logicClientExt) SyncOffline(ctx context.Context, in *SyncOfflineReq, opts ...grpc.CallOption) (*SyncOfflineReply, error) {
	syncBody := &protocol.SyncReqBody{
		LastSeq: in.LastSeq,
		Limit:   in.Limit,
	}
	body := protocol.MarshalSyncReq(syncBody)
	reply, err := c.Receive(ctx, &ReceiveReq{
		Mid: in.Mid,
		Proto: &protocol.Proto{
			Ver:  1,
			Op:   protocol.OpSyncReq,
			Body: body,
		},
	}, opts...)
	if err != nil {
		return nil, err
	}
	result := &SyncOfflineReply{}
	if reply != nil && reply.Proto != nil && len(reply.Proto.Body) > 0 {
		syncReply, err := protocol.UnmarshalSyncReply(reply.Proto.Body)
		if err == nil {
			result.CurrentSeq = syncReply.CurrentSeq
			result.HasMore = syncReply.HasMore
		}
	}
	return result, nil
}

// LogicServerExt extends the Logic server interface with new RPCs.
type LogicServerExt interface {
	LogicServer
	AckMessage(ctx context.Context, req *AckReq) (*AckReply, error)
	SyncOffline(ctx context.Context, req *SyncOfflineReq) (*SyncOfflineReply, error)
}
