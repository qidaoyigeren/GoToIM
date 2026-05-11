package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestMarshalUnmarshalMsgBody(t *testing.T) {
	mb := &MsgBody{
		MsgID:     "msg-001",
		FromUID:   1001,
		ToUID:     1002,
		Timestamp: 1700000000000,
		Seq:       42,
		Content:   []byte("hello world"),
	}
	data, err := MarshalMsgBody(mb)
	if err != nil {
		t.Fatalf("MarshalMsgBody: %v", err)
	}

	got, err := UnmarshalMsgBody(data)
	if err != nil {
		t.Fatalf("UnmarshalMsgBody: %v", err)
	}

	if got.MsgID != mb.MsgID {
		t.Errorf("MsgID = %q, want %q", got.MsgID, mb.MsgID)
	}
	if got.FromUID != mb.FromUID {
		t.Errorf("FromUID = %d, want %d", got.FromUID, mb.FromUID)
	}
	if got.ToUID != mb.ToUID {
		t.Errorf("ToUID = %d, want %d", got.ToUID, mb.ToUID)
	}
	if got.Timestamp != mb.Timestamp {
		t.Errorf("Timestamp = %d, want %d", got.Timestamp, mb.Timestamp)
	}
	if got.Seq != mb.Seq {
		t.Errorf("Seq = %d, want %d", got.Seq, mb.Seq)
	}
	if !bytes.Equal(got.Content, mb.Content) {
		t.Errorf("Content = %v, want %v", got.Content, mb.Content)
	}
}

func TestMarshalUnmarshalMsgBody_EmptyContent(t *testing.T) {
	mb := &MsgBody{
		MsgID:     "msg-002",
		FromUID:   1,
		ToUID:     2,
		Timestamp: 1000,
		Seq:       1,
	}
	data, err := MarshalMsgBody(mb)
	if err != nil {
		t.Fatalf("MarshalMsgBody: %v", err)
	}

	got, err := UnmarshalMsgBody(data)
	if err != nil {
		t.Fatalf("UnmarshalMsgBody: %v", err)
	}

	if got.MsgID != mb.MsgID {
		t.Errorf("MsgID = %q, want %q", got.MsgID, mb.MsgID)
	}
	if len(got.Content) != 0 {
		t.Errorf("Content should be empty, got %d bytes", len(got.Content))
	}
}

func TestMarshalUnmarshalMsgBody_LongMsgID(t *testing.T) {
	mb := &MsgBody{
		MsgID:     strings.Repeat("a", 65535),
		FromUID:   1,
		ToUID:     2,
		Timestamp: 1000,
		Seq:       1,
		Content:   []byte("data"),
	}
	data, err := MarshalMsgBody(mb)
	if err != nil {
		t.Fatalf("MarshalMsgBody: %v", err)
	}

	got, err := UnmarshalMsgBody(data)
	if err != nil {
		t.Fatalf("UnmarshalMsgBody: %v", err)
	}

	if got.MsgID != mb.MsgID {
		t.Errorf("MsgID length = %d, want 65535", len(got.MsgID))
	}
}

func TestMarshalMsgBody_MsgIDTooLong(t *testing.T) {
	mb := &MsgBody{
		MsgID: strings.Repeat("a", 65536),
	}
	_, err := MarshalMsgBody(mb)
	if err == nil {
		t.Error("expected error for msg_id too long, got nil")
	}
}

func TestMarshalUnmarshalAckBody(t *testing.T) {
	ab := &AckBody{
		MsgID: "ack-001",
		Seq:   99,
	}
	data, err := MarshalAckBody(ab)
	if err != nil {
		t.Fatalf("MarshalAckBody: %v", err)
	}

	got, err := UnmarshalAckBody(data)
	if err != nil {
		t.Fatalf("UnmarshalAckBody: %v", err)
	}

	if got.MsgID != ab.MsgID {
		t.Errorf("MsgID = %q, want %q", got.MsgID, ab.MsgID)
	}
	if got.Seq != ab.Seq {
		t.Errorf("Seq = %d, want %d", got.Seq, ab.Seq)
	}
}

func TestUnmarshalAckBody_TooShort(t *testing.T) {
	_, err := UnmarshalAckBody([]byte{0x00})
	if err == nil {
		t.Error("expected error for too short data, got nil")
	}
}

func TestMarshalUnmarshalSyncReq(t *testing.T) {
	sr := &SyncReqBody{
		LastSeq: 500,
		Limit:   100,
	}
	data := MarshalSyncReq(sr)

	got, err := UnmarshalSyncReq(data)
	if err != nil {
		t.Fatalf("UnmarshalSyncReq: %v", err)
	}

	if got.LastSeq != sr.LastSeq {
		t.Errorf("LastSeq = %d, want %d", got.LastSeq, sr.LastSeq)
	}
	if got.Limit != sr.Limit {
		t.Errorf("Limit = %d, want %d", got.Limit, sr.Limit)
	}
}

func TestUnmarshalSyncReq_TooShort(t *testing.T) {
	_, err := UnmarshalSyncReq([]byte{0x00, 0x01, 0x02})
	if err == nil {
		t.Error("expected error for too short data, got nil")
	}
}

func TestMarshalUnmarshalSyncReply(t *testing.T) {
	sr := &SyncReplyBody{
		CurrentSeq: 1000,
		HasMore:    true,
		Messages: []*MsgBody{
			{
				MsgID:     "msg-1",
				FromUID:   100,
				ToUID:     200,
				Timestamp: 1700000000000,
				Seq:       10,
				Content:   []byte("first"),
			},
			{
				MsgID:     "msg-2",
				FromUID:   101,
				ToUID:     200,
				Timestamp: 1700000001000,
				Seq:       11,
				Content:   []byte("second"),
			},
		},
	}
	data, err := MarshalSyncReply(sr)
	if err != nil {
		t.Fatalf("MarshalSyncReply: %v", err)
	}

	got, err := UnmarshalSyncReply(data)
	if err != nil {
		t.Fatalf("UnmarshalSyncReply: %v", err)
	}

	if got.CurrentSeq != sr.CurrentSeq {
		t.Errorf("CurrentSeq = %d, want %d", got.CurrentSeq, sr.CurrentSeq)
	}
	if got.HasMore != sr.HasMore {
		t.Errorf("HasMore = %v, want %v", got.HasMore, sr.HasMore)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("Messages count = %d, want 2", len(got.Messages))
	}
	for i, m := range got.Messages {
		want := sr.Messages[i]
		if m.MsgID != want.MsgID {
			t.Errorf("Messages[%d].MsgID = %q, want %q", i, m.MsgID, want.MsgID)
		}
		if m.FromUID != want.FromUID {
			t.Errorf("Messages[%d].FromUID = %d, want %d", i, m.FromUID, want.FromUID)
		}
		if m.Seq != want.Seq {
			t.Errorf("Messages[%d].Seq = %d, want %d", i, m.Seq, want.Seq)
		}
		if !bytes.Equal(m.Content, want.Content) {
			t.Errorf("Messages[%d].Content = %v, want %v", i, m.Content, want.Content)
		}
	}
}

func TestMarshalUnmarshalSyncReply_Empty(t *testing.T) {
	sr := &SyncReplyBody{
		CurrentSeq: 50,
		HasMore:    false,
	}
	data, err := MarshalSyncReply(sr)
	if err != nil {
		t.Fatalf("MarshalSyncReply: %v", err)
	}

	got, err := UnmarshalSyncReply(data)
	if err != nil {
		t.Fatalf("UnmarshalSyncReply: %v", err)
	}

	if got.CurrentSeq != 50 {
		t.Errorf("CurrentSeq = %d, want 50", got.CurrentSeq)
	}
	if got.HasMore {
		t.Error("HasMore should be false")
	}
	if len(got.Messages) != 0 {
		t.Errorf("Messages should be empty, got %d", len(got.Messages))
	}
}

func TestUnmarshalSyncReply_TooShort(t *testing.T) {
	_, err := UnmarshalSyncReply([]byte{0x00, 0x01})
	if err == nil {
		t.Error("expected error for too short data, got nil")
	}
}

func TestUnmarshalSyncReply_TruncatedMessage(t *testing.T) {
	// Build a reply that claims 1 message but truncates it
	sr := &SyncReplyBody{
		CurrentSeq: 1,
		HasMore:    false,
		Messages: []*MsgBody{
			{MsgID: "x", FromUID: 1, ToUID: 2, Timestamp: 3, Seq: 4, Content: []byte("data")},
		},
	}
	full, err := MarshalSyncReply(sr)
	if err != nil {
		t.Fatalf("MarshalSyncReply: %v", err)
	}
	// Truncate: cut off last few bytes
	truncated := full[:len(full)-3]
	_, err = UnmarshalSyncReply(truncated)
	if err == nil {
		t.Error("expected error for truncated message, got nil")
	}
}
