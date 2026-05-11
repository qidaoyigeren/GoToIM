package protocol

import (
	"encoding/binary"
	"errors"
)

// MsgBody is the structured message envelope carried inside Proto.Body.
// Fields are encoded as: msg_id(varint-prefixed bytes) + from_uid(8) + to_uid(8) + timestamp(8) + seq(8) + content(rest)
type MsgBody struct {
	MsgID     string
	FromUID   int64
	ToUID     int64
	Timestamp int64
	Seq       int64
	Content   []byte
}

// MarshalMsgBody encodes a MsgBody into bytes.
// Format: [msg_id_len:2][msg_id][from_uid:8][to_uid:8][timestamp:8][seq:8][content...]
func MarshalMsgBody(mb *MsgBody) ([]byte, error) {
	msgIDBytes := []byte(mb.MsgID)
	if len(msgIDBytes) > 65535 {
		return nil, errors.New("msg_id too long")
	}
	size := 2 + len(msgIDBytes) + 8 + 8 + 8 + 8 + len(mb.Content)
	buf := make([]byte, size)

	// msg_id length (2 bytes, big-endian)
	binary.BigEndian.PutUint16(buf[0:2], uint16(len(msgIDBytes)))
	// msg_id
	copy(buf[2:2+len(msgIDBytes)], msgIDBytes)

	offset := 2 + len(msgIDBytes)
	binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(mb.FromUID))
	offset += 8
	binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(mb.ToUID))
	offset += 8
	binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(mb.Timestamp))
	offset += 8
	binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(mb.Seq))
	offset += 8
	copy(buf[offset:], mb.Content)

	return buf, nil
}

// UnmarshalMsgBody decodes bytes into a MsgBody.
func UnmarshalMsgBody(data []byte) (*MsgBody, error) {
	if len(data) < 2 {
		return nil, errors.New("data too short for msg_id length")
	}
	msgIDLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+msgIDLen+32 {
		return nil, errors.New("data too short for MsgBody header")
	}

	mb := &MsgBody{}
	mb.MsgID = string(data[2 : 2+msgIDLen])

	offset := 2 + msgIDLen
	mb.FromUID = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
	offset += 8
	mb.ToUID = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
	offset += 8
	mb.Timestamp = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
	offset += 8
	mb.Seq = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
	offset += 8
	if offset < len(data) {
		mb.Content = data[offset:]
	}

	return mb, nil
}

// AckBody is the ACK message carried inside Proto.Body.
// Format: [msg_id_len:2][msg_id][seq:8]
type AckBody struct {
	MsgID string
	Seq   int64
}

// MarshalAckBody encodes an AckBody into bytes.
func MarshalAckBody(ab *AckBody) ([]byte, error) {
	msgIDBytes := []byte(ab.MsgID)
	if len(msgIDBytes) > 65535 {
		return nil, errors.New("msg_id too long")
	}
	size := 2 + len(msgIDBytes) + 8
	buf := make([]byte, size)

	binary.BigEndian.PutUint16(buf[0:2], uint16(len(msgIDBytes)))
	copy(buf[2:2+len(msgIDBytes)], msgIDBytes)
	offset := 2 + len(msgIDBytes)
	binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(ab.Seq))

	return buf, nil
}

// UnmarshalAckBody decodes bytes into an AckBody.
func UnmarshalAckBody(data []byte) (*AckBody, error) {
	if len(data) < 2 {
		return nil, errors.New("data too short for ack")
	}
	msgIDLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+msgIDLen+8 {
		return nil, errors.New("data too short for AckBody")
	}

	ab := &AckBody{}
	ab.MsgID = string(data[2 : 2+msgIDLen])
	offset := 2 + msgIDLen
	ab.Seq = int64(binary.BigEndian.Uint64(data[offset : offset+8]))

	return ab, nil
}

// SyncReqBody is the sync request carried inside Proto.Body.
// Format: [last_seq:8][limit:4]
type SyncReqBody struct {
	LastSeq int64
	Limit   int32
}

// MarshalSyncReq encodes a SyncReqBody into bytes.
func MarshalSyncReq(sr *SyncReqBody) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint64(buf[0:8], uint64(sr.LastSeq))
	binary.BigEndian.PutUint32(buf[8:12], uint32(sr.Limit))
	return buf
}

// UnmarshalSyncReq decodes bytes into a SyncReqBody.
func UnmarshalSyncReq(data []byte) (*SyncReqBody, error) {
	if len(data) < 12 {
		return nil, errors.New("data too short for SyncReq")
	}
	return &SyncReqBody{
		LastSeq: int64(binary.BigEndian.Uint64(data[0:8])),
		Limit:   int32(binary.BigEndian.Uint32(data[8:12])),
	}, nil
}

// SyncReplyBody is the sync reply carried inside Proto.Body.
// Format: [current_seq:8][has_more:1][msg_count:4][msgs...]
type SyncReplyBody struct {
	CurrentSeq int64
	HasMore    bool
	Messages   []*MsgBody
}

// MarshalSyncReply encodes a SyncReplyBody into bytes.
func MarshalSyncReply(sr *SyncReplyBody) ([]byte, error) {
	var msgBytes [][]byte
	for _, m := range sr.Messages {
		b, err := MarshalMsgBody(m)
		if err != nil {
			return nil, err
		}
		msgBytes = append(msgBytes, b)
	}

	totalSize := 8 + 1 + 4 // current_seq + has_more + msg_count
	for _, b := range msgBytes {
		totalSize += 4 + len(b) // len prefix + data
	}

	buf := make([]byte, totalSize)
	binary.BigEndian.PutUint64(buf[0:8], uint64(sr.CurrentSeq))
	if sr.HasMore {
		buf[8] = 1
	}
	binary.BigEndian.PutUint32(buf[9:13], uint32(len(msgBytes)))

	offset := 13
	for _, b := range msgBytes {
		binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(b)))
		offset += 4
		copy(buf[offset:], b)
		offset += len(b)
	}

	return buf, nil
}

// UnmarshalSyncReply decodes bytes into a SyncReplyBody.
func UnmarshalSyncReply(data []byte) (*SyncReplyBody, error) {
	if len(data) < 13 {
		return nil, errors.New("data too short for SyncReply")
	}
	sr := &SyncReplyBody{
		CurrentSeq: int64(binary.BigEndian.Uint64(data[0:8])),
		HasMore:    data[8] == 1,
	}
	msgCount := int(binary.BigEndian.Uint32(data[9:13]))
	offset := 13

	for i := 0; i < msgCount; i++ {
		if offset+4 > len(data) {
			return nil, errors.New("data truncated in SyncReply messages")
		}
		msgLen := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if offset+msgLen > len(data) {
			return nil, errors.New("data truncated in SyncReply message body")
		}
		mb, err := UnmarshalMsgBody(data[offset : offset+msgLen])
		if err != nil {
			return nil, err
		}
		sr.Messages = append(sr.Messages, mb)
		offset += msgLen
	}

	return sr, nil
}
