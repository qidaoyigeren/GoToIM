package comet

import (
	"context"
	"sync"
	"testing"

	logic "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/api/protocol"

	"google.golang.org/grpc"
)

// ============ mock LogicClient for Comet tests ============

type mockLogicClient struct {
	mu           sync.Mutex
	connectFn    func(ctx context.Context, in *logic.ConnectReq, opts ...grpc.CallOption) (*logic.ConnectReply, error)
	disconnectFn func(ctx context.Context, in *logic.DisconnectReq, opts ...grpc.CallOption) (*logic.DisconnectReply, error)
	heartbeatFn  func(ctx context.Context, in *logic.HeartbeatReq, opts ...grpc.CallOption) (*logic.HeartbeatReply, error)
	receiveFn    func(ctx context.Context, in *logic.ReceiveReq, opts ...grpc.CallOption) (*logic.ReceiveReply, error)
	receiveCalls []*logic.ReceiveReq
}

func (m *mockLogicClient) Connect(ctx context.Context, in *logic.ConnectReq, opts ...grpc.CallOption) (*logic.ConnectReply, error) {
	if m.connectFn != nil {
		return m.connectFn(ctx, in, opts...)
	}
	return &logic.ConnectReply{}, nil
}

func (m *mockLogicClient) Disconnect(ctx context.Context, in *logic.DisconnectReq, opts ...grpc.CallOption) (*logic.DisconnectReply, error) {
	if m.disconnectFn != nil {
		return m.disconnectFn(ctx, in, opts...)
	}
	return &logic.DisconnectReply{}, nil
}

func (m *mockLogicClient) Heartbeat(ctx context.Context, in *logic.HeartbeatReq, opts ...grpc.CallOption) (*logic.HeartbeatReply, error) {
	if m.heartbeatFn != nil {
		return m.heartbeatFn(ctx, in, opts...)
	}
	return &logic.HeartbeatReply{}, nil
}

func (m *mockLogicClient) RenewOnline(ctx context.Context, in *logic.OnlineReq, opts ...grpc.CallOption) (*logic.OnlineReply, error) {
	return &logic.OnlineReply{}, nil
}

func (m *mockLogicClient) Receive(ctx context.Context, in *logic.ReceiveReq, opts ...grpc.CallOption) (*logic.ReceiveReply, error) {
	m.mu.Lock()
	m.receiveCalls = append(m.receiveCalls, in)
	m.mu.Unlock()
	if m.receiveFn != nil {
		return m.receiveFn(ctx, in, opts...)
	}
	return &logic.ReceiveReply{}, nil
}

func (m *mockLogicClient) Nodes(ctx context.Context, in *logic.NodesReq, opts ...grpc.CallOption) (*logic.NodesReply, error) {
	return &logic.NodesReply{}, nil
}

func (m *mockLogicClient) AckMessage(ctx context.Context, in *logic.AckReq, opts ...grpc.CallOption) (*logic.AckReply, error) {
	return &logic.AckReply{}, nil
}

func (m *mockLogicClient) SyncOffline(ctx context.Context, in *logic.SyncOfflineReq, opts ...grpc.CallOption) (*logic.SyncOfflineReply, error) {
	return &logic.SyncOfflineReply{}, nil
}

func (m *mockLogicClient) getReceiveCalls() []*logic.ReceiveReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*logic.ReceiveReq, len(m.receiveCalls))
	copy(cp, m.receiveCalls)
	return cp
}

// newTestServer creates a Server with a mock rpcClient for testing.
func newTestServer(mockClient *mockLogicClient) *Server {
	return &Server{
		serverID:  "test-comet-1",
		rpcClient: mockClient,
	}
}

// ============ Receive tests ============

func TestServer_Receive_CopiesReplyProto(t *testing.T) {
	mock := &mockLogicClient{
		receiveFn: func(ctx context.Context, in *logic.ReceiveReq, opts ...grpc.CallOption) (*logic.ReceiveReply, error) {
			// Simulate Logic modifying the proto (e.g., SyncReply)
			return &logic.ReceiveReply{
				Proto: &protocol.Proto{
					Op:   protocol.OpSyncReply,
					Body: []byte("sync-reply-data"),
				},
			}, nil
		},
	}
	srv := newTestServer(mock)

	p := &protocol.Proto{
		Op:   protocol.OpSyncReq,
		Body: []byte("sync-request"),
	}

	err := srv.Receive(context.Background(), 1001, p)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	// Verify the proto was modified with the reply data
	if p.Op != protocol.OpSyncReply {
		t.Errorf("p.Op = %d, want %d", p.Op, protocol.OpSyncReply)
	}
	if string(p.Body) != "sync-reply-data" {
		t.Errorf("p.Body = %q, want %q", p.Body, "sync-reply-data")
	}
}

func TestServer_Receive_NilReply(t *testing.T) {
	mock := &mockLogicClient{
		receiveFn: func(ctx context.Context, in *logic.ReceiveReq, opts ...grpc.CallOption) (*logic.ReceiveReply, error) {
			return &logic.ReceiveReply{}, nil // empty reply, no Proto
		},
	}
	srv := newTestServer(mock)

	p := &protocol.Proto{
		Op:   protocol.OpPushMsgAck,
		Body: []byte("ack-data"),
	}

	err := srv.Receive(context.Background(), 1001, p)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	// Proto should be unchanged (reply had no Proto)
	if p.Op != protocol.OpPushMsgAck {
		t.Errorf("p.Op = %d, want %d (should be unchanged)", p.Op, protocol.OpPushMsgAck)
	}
	if string(p.Body) != "ack-data" {
		t.Errorf("p.Body = %q, want %q (should be unchanged)", p.Body, "ack-data")
	}
}

func TestServer_Receive_Error(t *testing.T) {
	mock := &mockLogicClient{
		receiveFn: func(ctx context.Context, in *logic.ReceiveReq, opts ...grpc.CallOption) (*logic.ReceiveReply, error) {
			return nil, grpc.Errorf(13, "internal error")
		},
	}
	srv := newTestServer(mock)

	p := &protocol.Proto{Op: protocol.OpSyncReq, Body: []byte("req")}
	err := srv.Receive(context.Background(), 1001, p)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// ============ Operate tests ============

func TestServer_Operate_PushMsgAck(t *testing.T) {
	mock := &mockLogicClient{
		receiveFn: func(ctx context.Context, in *logic.ReceiveReq, opts ...grpc.CallOption) (*logic.ReceiveReply, error) {
			return &logic.ReceiveReply{}, nil
		},
	}
	srv := newTestServer(mock)
	ch := NewChannel(5, 5)
	ch.Mid = 1001

	ackBytes, _ := protocol.MarshalAckBody(&protocol.AckBody{MsgID: "m1", Seq: 1})
	p := &protocol.Proto{
		Op:   protocol.OpPushMsgAck,
		Body: ackBytes,
	}

	err := srv.Operate(context.Background(), p, ch, nil)
	if err != nil {
		t.Fatalf("Operate: %v", err)
	}

	// Verify Receive was called (ACK forwarded to Logic)
	calls := mock.getReceiveCalls()
	if len(calls) != 1 {
		t.Fatalf("receive calls = %d, want 1", len(calls))
	}
	if calls[0].Mid != 1001 {
		t.Errorf("receive mid = %d, want 1001", calls[0].Mid)
	}
	if calls[0].Proto.Op != protocol.OpPushMsgAck {
		t.Errorf("receive proto op = %d, want %d", calls[0].Proto.Op, protocol.OpPushMsgAck)
	}

	// Body should be cleared after ACK
	if p.Body != nil {
		t.Errorf("p.Body should be nil after ACK, got %q", p.Body)
	}
}

func TestServer_Operate_SyncReq(t *testing.T) {
	mock := &mockLogicClient{
		receiveFn: func(ctx context.Context, in *logic.ReceiveReq, opts ...grpc.CallOption) (*logic.ReceiveReply, error) {
			// Simulate Logic returning sync reply
			return &logic.ReceiveReply{
				Proto: &protocol.Proto{
					Op:   protocol.OpSyncReply,
					Body: []byte("sync-data"),
				},
			}, nil
		},
	}
	srv := newTestServer(mock)
	ch := NewChannel(5, 5)
	ch.Mid = 1001

	syncBytes := protocol.MarshalSyncReq(&protocol.SyncReqBody{LastSeq: 0, Limit: 100})
	p := &protocol.Proto{
		Op:   protocol.OpSyncReq,
		Body: syncBytes,
	}

	err := srv.Operate(context.Background(), p, ch, nil)
	if err != nil {
		t.Fatalf("Operate: %v", err)
	}

	// Verify Receive was called
	calls := mock.getReceiveCalls()
	if len(calls) != 1 {
		t.Fatalf("receive calls = %d, want 1", len(calls))
	}

	// Verify proto was updated with sync reply
	if p.Op != protocol.OpSyncReply {
		t.Errorf("p.Op = %d, want %d", p.Op, protocol.OpSyncReply)
	}
	if string(p.Body) != "sync-data" {
		t.Errorf("p.Body = %q, want %q", p.Body, "sync-data")
	}
}

func TestServer_Operate_SyncReq_Error(t *testing.T) {
	mock := &mockLogicClient{
		receiveFn: func(ctx context.Context, in *logic.ReceiveReq, opts ...grpc.CallOption) (*logic.ReceiveReply, error) {
			return nil, grpc.Errorf(13, "sync failed")
		},
	}
	srv := newTestServer(mock)
	ch := NewChannel(5, 5)
	ch.Mid = 1001

	syncBytes := protocol.MarshalSyncReq(&protocol.SyncReqBody{LastSeq: 0, Limit: 100})
	p := &protocol.Proto{
		Op:   protocol.OpSyncReq,
		Body: syncBytes,
	}

	err := srv.Operate(context.Background(), p, ch, nil)
	if err != nil {
		t.Fatalf("Operate: %v", err)
	}

	// Body should be cleared on error
	if p.Body != nil {
		t.Errorf("p.Body should be nil after error, got %q", p.Body)
	}
}

func TestServer_Operate_Default(t *testing.T) {
	mock := &mockLogicClient{
		receiveFn: func(ctx context.Context, in *logic.ReceiveReq, opts ...grpc.CallOption) (*logic.ReceiveReply, error) {
			return &logic.ReceiveReply{}, nil
		},
	}
	srv := newTestServer(mock)
	ch := NewChannel(5, 5)
	ch.Mid = 1001

	p := &protocol.Proto{
		Op:   9999, // unknown op
		Body: []byte("data"),
	}

	err := srv.Operate(context.Background(), p, ch, nil)
	if err != nil {
		t.Fatalf("Operate: %v", err)
	}

	// Default case calls Receive and clears Body
	calls := mock.getReceiveCalls()
	if len(calls) != 1 {
		t.Fatalf("receive calls = %d, want 1", len(calls))
	}
	if p.Body != nil {
		t.Errorf("p.Body should be nil in default case, got %q", p.Body)
	}
}

func TestServer_Operate_Sub(t *testing.T) {
	srv := newTestServer(&mockLogicClient{})
	ch := NewChannel(5, 5)

	p := &protocol.Proto{
		Op:   protocol.OpSub,
		Body: []byte("1000,1001,1002"),
	}

	err := srv.Operate(context.Background(), p, ch, nil)
	if err != nil {
		t.Fatalf("Operate: %v", err)
	}

	if p.Op != protocol.OpSubReply {
		t.Errorf("p.Op = %d, want %d", p.Op, protocol.OpSubReply)
	}

	// Verify watch ops were set
	if !ch.NeedPush(1000) || !ch.NeedPush(1001) || !ch.NeedPush(1002) {
		t.Error("expected watch ops 1000,1001,1002 to be set")
	}
}

func TestServer_Operate_Unsub(t *testing.T) {
	srv := newTestServer(&mockLogicClient{})
	ch := NewChannel(5, 5)
	ch.Watch(1000, 1001, 1002)

	p := &protocol.Proto{
		Op:   protocol.OpUnsub,
		Body: []byte("1001"),
	}

	err := srv.Operate(context.Background(), p, ch, nil)
	if err != nil {
		t.Fatalf("Operate: %v", err)
	}

	if p.Op != protocol.OpUnsubReply {
		t.Errorf("p.Op = %d, want %d", p.Op, protocol.OpUnsubReply)
	}

	// 1001 should be unsubscribed, 1000 and 1002 should remain
	if ch.NeedPush(1001) {
		t.Error("op 1001 should be unsubscribed")
	}
	if !ch.NeedPush(1000) || !ch.NeedPush(1002) {
		t.Error("ops 1000 and 1002 should still be watched")
	}
}
