package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	"github.com/Terry-Mao/goim/internal/logic/conf"
	"github.com/Terry-Mao/goim/internal/logic/dao"
	"github.com/Terry-Mao/goim/internal/logic/service"
	"github.com/Terry-Mao/goim/pkg/snowflake"
	xtime "github.com/Terry-Mao/goim/pkg/time"
)

// ============================================================
// Helpers
// ============================================================

func newTestDao(t *testing.T) *dao.Dao {
	if t != nil {
		t.Helper()
	}
	return dao.New(&conf.Config{
		Kafka: &conf.Kafka{
			Topic:     "goim-push-topic",
			Brokers:   []string{"127.0.0.1:9092"},
			PushTopic: "goim-push-topic",
			RoomTopic: "goim-room-topic",
			AllTopic:  "goim-all-topic",
			ACKTopic:  "goim-ack-topic",
		},
		Redis: &conf.Redis{
			Network:      "tcp",
			Addr:         "127.0.0.1:6379",
			Active:       10,
			Idle:         5,
			DialTimeout:  xtime.Duration(200 * time.Millisecond),
			ReadTimeout:  xtime.Duration(500 * time.Millisecond),
			WriteTimeout: xtime.Duration(500 * time.Millisecond),
			IdleTimeout:  xtime.Duration(120 * time.Second),
			Expire:       xtime.Duration(30 * time.Minute),
		},
	})
}

type mockCometPusher struct {
	calls   []pushCall
	pushErr error
}

type pushCall struct {
	server string
	keys   []string
	op     int32
	body   []byte
}

func (m *mockCometPusher) PushMsg(ctx context.Context, server string, keys []string, op int32, body []byte) error {
	m.calls = append(m.calls, pushCall{server, keys, op, body})
	return m.pushErr
}

func (m *mockCometPusher) SetError(err error) {
	m.pushErr = err
}

// ============================================================
// 1. Redis DAO Tests
// ============================================================

func TestRedisConnection(t *testing.T) {
	d := newTestDao(t)
	defer d.Close()

	ctx := context.Background()
	if err := d.Ping(ctx); err != nil {
		t.Fatalf("Redis ping failed: %v", err)
	}
	t.Log("Redis connection OK")
}

func TestRedisMappingOps(t *testing.T) {
	d := newTestDao(t)
	defer d.Close()
	ctx := context.Background()

	mid := int64(9001)
	key := "test-key-chain-1"
	server := "test-comet-1"

	// Add mapping
	if err := d.AddMapping(ctx, mid, key, server); err != nil {
		t.Fatalf("AddMapping failed: %v", err)
	}
	t.Log("AddMapping OK")

	// KeysByMids - returns (res map, olMids []int64, err)
	keyMap, olMids, err := d.KeysByMids(ctx, []int64{mid})
	if err != nil {
		t.Fatalf("KeysByMids failed: %v", err)
	}
	t.Logf("KeysByMids: keys=%v olMids=%v", keyMap, olMids)

	// ExpireMapping
	has, err := d.ExpireMapping(ctx, mid, key)
	if err != nil {
		t.Errorf("ExpireMapping failed: %v", err)
	}
	t.Logf("ExpireMapping: has=%v", has)

	// DelMapping
	has, err = d.DelMapping(ctx, mid, key, server)
	if err != nil {
		t.Errorf("DelMapping failed: %v", err)
	}
	t.Logf("DelMapping: has=%v", has)

	_ = d.DelServerOnline(ctx, server)
}

func TestRedisMessageOps(t *testing.T) {
	d := newTestDao(t)
	defer d.Close()
	ctx := context.Background()

	uid := int64(9002)
	msgID := "test-msg-chain-001"

	// Add to offline queue
	if err := d.AddToOfflineQueue(ctx, uid, msgID, 42.0); err != nil {
		t.Fatalf("AddToOfflineQueue failed: %v", err)
	}
	t.Log("AddToOfflineQueue OK")

	// Get offline queue size
	size, err := d.GetOfflineQueueSize(ctx, uid)
	if err != nil {
		t.Fatalf("GetOfflineQueueSize failed: %v", err)
	}
	if size < 1 {
		t.Errorf("offline queue size=%d, want >=1", size)
	}
	t.Logf("GetOfflineQueueSize = %d", size)

	// Set message status
	if err := d.SetMessageStatus(ctx, msgID, map[string]interface{}{"status": "delivered"}); err != nil {
		t.Fatalf("SetMessageStatus failed: %v", err)
	}
	t.Log("SetMessageStatus OK")

	// Get message status
	status, err := d.GetMessageStatus(ctx, msgID)
	if err != nil {
		t.Fatalf("GetMessageStatus failed: %v", err)
	}
	if status["status"] != "delivered" {
		t.Errorf("status=%v, want delivered", status)
	}
	t.Log("GetMessageStatus OK")

	// Cleanup
	_ = d.RemoveFromOfflineQueue(ctx, uid, msgID)
}

// ============================================================
// 2. Kafka Producer Test
// ============================================================

func TestKafkaPushMsg(t *testing.T) {
	d := newTestDao(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := d.PushMsg(ctx, int32(protocol.OpRaw), "test-server", []string{"test-key"}, []byte("hello-kafka"))
	if err != nil {
		t.Fatalf("Kafka PushMsg failed: %v", err)
	}
	t.Log("Kafka PushMsg OK")
}

func TestKafkaBroadcastRoom(t *testing.T) {
	d := newTestDao(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := d.BroadcastRoomMsg(ctx, int32(protocol.OpRaw), "live://test-room", []byte("room-msg"))
	if err != nil {
		t.Fatalf("Kafka BroadcastRoomMsg failed: %v", err)
	}
	t.Log("Kafka BroadcastRoomMsg OK")
}

// ============================================================
// 3. Protocol Message Test
// ============================================================

func TestProtocolMessageRoundTrip(t *testing.T) {
	original := &protocol.MsgBody{
		MsgID:     "test-msg-id-001",
		FromUID:   1001,
		ToUID:     1002,
		Timestamp: time.Now().UnixMilli(),
		Seq:       1,
		Content:   []byte("Hello, this is a chain test message"),
	}

	data, err := protocol.MarshalMsgBody(original)
	if err != nil {
		t.Fatalf("MarshalMsgBody failed: %v", err)
	}
	t.Logf("MsgBody marshalled to %d bytes", len(data))

	decoded, err := protocol.UnmarshalMsgBody(data)
	if err != nil {
		t.Fatalf("UnmarshalMsgBody failed: %v", err)
	}
	if decoded.FromUID != original.FromUID || decoded.ToUID != original.ToUID {
		t.Errorf("MsgBody mismatch: from=%d/%d to=%d/%d",
			decoded.FromUID, original.FromUID, decoded.ToUID, original.ToUID)
	}
	t.Log("MsgBody round-trip OK")

	// AckBody
	ack := &protocol.AckBody{MsgID: "msg-ack-001", Seq: 99}
	ackData, _ := protocol.MarshalAckBody(ack)
	decodedAck, _ := protocol.UnmarshalAckBody(ackData)
	if decodedAck.MsgID != ack.MsgID || decodedAck.Seq != ack.Seq {
		t.Errorf("AckBody mismatch")
	}
	t.Log("AckBody round-trip OK")

	// SyncReq
	syncReq := &protocol.SyncReqBody{LastSeq: 10, Limit: 50}
	reqData := protocol.MarshalSyncReq(syncReq)
	decodedReq, _ := protocol.UnmarshalSyncReq(reqData)
	if decodedReq.LastSeq != syncReq.LastSeq || decodedReq.Limit != syncReq.Limit {
		t.Errorf("SyncReq mismatch")
	}
	t.Log("SyncReq round-trip OK")

	// SyncReply
	syncReply := &protocol.SyncReplyBody{CurrentSeq: 55, HasMore: true}
	replyData, _ := protocol.MarshalSyncReply(syncReply)
	decodedReply, _ := protocol.UnmarshalSyncReply(replyData)
	if decodedReply.CurrentSeq != syncReply.CurrentSeq || decodedReply.HasMore != syncReply.HasMore {
		t.Errorf("SyncReply mismatch")
	}
	t.Log("SyncReply round-trip OK")
}

// ============================================================
// 4. Snowflake ID Generator
// ============================================================

func TestSnowflakeID(t *testing.T) {
	sf, err := snowflake.New(1)
	if err != nil {
		t.Fatalf("snowflake.New failed: %v", err)
	}

	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := sf.GenerateString()
		if err != nil {
			t.Fatalf("GenerateString failed: %v", err)
		}
		if ids[id] {
			t.Errorf("duplicate ID: %s", id)
		}
		ids[id] = true
	}
	t.Logf("Snowflake: 100 unique IDs generated")
}

// ============================================================
// 5. Push Service Chain Test
// ============================================================

func TestPushServiceChain(t *testing.T) {
	d := newTestDao(t)
	defer d.Close()
	ctx := context.Background()

	sessMgr := service.NewSessionManager(d, 30*time.Minute)
	ackSvc := service.NewAckService(d, d)
	mockPusher := &mockCometPusher{}
	pushSvc := service.NewPushService(d, d, sessMgr, ackSvc, mockPusher)
	sf, _ := snowflake.New(1)
	pushSvc.SetIDGenerator(sf)

	ts := time.Now().UnixNano()
	uid := int64(9100 + ts%100)
	device := fmt.Sprintf("test-device-%d", ts%1000)
	key := fmt.Sprintf("test-key-chain-push-%d", ts)
	server := fmt.Sprintf("test-comet-push-%d", ts)
	sid := fmt.Sprintf("sid-chain-1-%d", ts)

	// Create session
	if err := d.AddSession(ctx, sid, uid, key, device, "test", server); err != nil {
		t.Fatalf("AddSession failed: %v", err)
	}
	t.Logf("Step 1: Session created (uid=%d)", uid)

	// Push (online - direct push)
	msgID := fmt.Sprintf("chain-test-msg-%d-001", ts)
	err := pushSvc.PushToUser(ctx, msgID, uid, int32(protocol.OpRaw), []byte("chain test message"), 1)
	if err != nil {
		t.Fatalf("PushToUser failed: %v", err)
	}
	if len(mockPusher.calls) != 1 {
		t.Errorf("expected 1 direct push, got %d", len(mockPusher.calls))
	}
	t.Log("Step 2: PushToUser OK (direct push)")

	// Verify status
	status, _ := d.GetMessageStatus(ctx, msgID)
	t.Logf("Step 3: status=%v", status)

	// Handle ACK
	if err := ackSvc.HandleAck(ctx, uid, msgID); err != nil {
		t.Fatalf("HandleAck failed: %v", err)
	}
	t.Log("Step 4: ACK handled")

	// Push fails, fallback to offline
	mockPusher.SetError(fmt.Errorf("simulated comet failure"))
	msgID2 := fmt.Sprintf("chain-test-msg-%d-002", ts)
	err = pushSvc.PushToUser(ctx, msgID2, uid, int32(protocol.OpRaw), []byte("fallback test"), 2)
	if err != nil {
		t.Fatalf("PushToUser (fallback) failed: %v", err)
	}
	t.Log("Step 5: PushToUser (fallback to Kafka) OK")

	// Verify offline
	offlineSize, _ := d.GetOfflineQueueSize(ctx, uid)
	if offlineSize < 1 {
		t.Errorf("offline queue should have messages, size=%d", offlineSize)
	}
	t.Logf("Step 6: Offline queue size = %d", offlineSize)

	// Cleanup
	d.DelSession(ctx, sid, uid, device, key)
	d.RemoveFromOfflineQueue(ctx, uid, msgID)
	d.RemoveFromOfflineQueue(ctx, uid, msgID2)
	d.DelMapping(ctx, uid, key, server)
}

// ============================================================
// 6. Complete IM Chain Test
// ============================================================

func TestCompleteIMChain(t *testing.T) {
	d := newTestDao(t)
	defer d.Close()
	ctx := context.Background()

	sessMgr := service.NewSessionManager(d, 30*time.Minute)
	ackSvc := service.NewAckService(d, d)
	mockPusher := &mockCometPusher{}
	pushSvc := service.NewPushService(d, d, sessMgr, ackSvc, mockPusher)
	sf, _ := snowflake.New(1)
	pushSvc.SetIDGenerator(sf)

	// User A and B login
	uidA := int64(9200)
	keyA := "chain-key-A"
	_ = d.AddSession(ctx, "sid-chain-A", uidA, keyA, "device-A", "android", "comet-A")
	t.Log("User A (9200) logged in")

	uidB := int64(9201)
	keyB := "chain-key-B"
	_ = d.AddSession(ctx, "sid-chain-B", uidB, keyB, "device-B", "ios", "comet-A")
	t.Log("User B (9201) logged in")

	// User A sends to User B
	chatMsg := &protocol.MsgBody{
		MsgID:     "",
		FromUID:   uidA,
		ToUID:     uidB,
		Timestamp: time.Now().UnixMilli(),
		Seq:       1,
		Content:   []byte("Hello B, chain test!"),
	}
	msgBytes, _ := protocol.MarshalMsgBody(chatMsg)
	msgID, _ := sf.GenerateString()

	// Track + Push
	_ = ackSvc.TrackMessage(ctx, msgID, 0, uidA, int32(protocol.OpRaw), msgBytes)
	t.Logf("Message tracked: msg_id=%s", msgID)

	err := pushSvc.PushToUser(ctx, msgID, uidB, int32(protocol.OpRaw), msgBytes, 1)
	if err != nil {
		t.Fatalf("PushToUser B failed: %v", err)
	}
	t.Log("Message pushed to User B")

	// User B ACKs
	_ = ackSvc.HandleAck(ctx, uidB, msgID)
	t.Log("User B sent ACK")

	// Offline user C
	uidC := int64(9202)
	msgID2, _ := sf.GenerateString()
	err = pushSvc.PushToUser(ctx, msgID2, uidC, int32(protocol.OpRaw), []byte("offline for C"), 1)
	if err != nil {
		t.Fatalf("PushToUser C (offline) failed: %v", err)
	}
	t.Log("Offline message stored for User C")

	// Sync offline messages for C
	size, _ := d.GetOfflineQueueSize(ctx, uidC)
	t.Logf("User C offline queue size = %d", size)

	offlineMsgs, _ := d.GetOfflineQueue(ctx, uidC, 0, 100)
	t.Logf("Synced %d offline messages for User C", len(offlineMsgs))

	// Final verification
	status, _ := d.GetMessageStatus(ctx, msgID)
	t.Logf("Final msg status: %v", status)

	// Cleanup
	d.DelSession(ctx, "sid-chain-A", uidA, "device-A", keyA)
	d.DelSession(ctx, "sid-chain-B", uidB, "device-B", keyB)
	for _, mid := range []int64{uidA, uidB, uidC} {
		d.RemoveFromOfflineQueue(ctx, mid, msgID)
		d.RemoveFromOfflineQueue(ctx, mid, msgID2)
	}
	d.DelMapping(ctx, uidA, keyA, "comet-A")
	d.DelMapping(ctx, uidB, keyB, "comet-A")

	t.Log("=== COMPLETE IM CHAIN TEST PASSED ===")
}

func TestMain(m *testing.M) {
	fmt.Println("=== goim Chain Integration Test ===")
	fmt.Println("Redis: 127.0.0.1:6379")
	fmt.Println("Kafka: 127.0.0.1:9092")
	fmt.Println()

	os.Exit(m.Run())
}
