// =====================================================================
// message.go — 自定义应用层消息体编解码
// =====================================================================
//
// 本文件定义了 goim 系统中所有**应用层消息体**的结构体及其二进制序列化/反序列化逻辑。
//
// 【与 Proto 的关系】
//   - Proto 是底层传输协议帧（protobuf 定义），包含 Ver/Op/Seq/Body 四个字段。
//   - Proto.Body 是一段 []byte，承载的是**应用层业务数据**。
//   - 本文件中的 MsgBody / AckBody / SyncReqBody / SyncReplyBody 就是这些业务数据的
//     结构化表示，通过自定义二进制格式（非 protobuf）编码后塞入 Proto.Body。
//
// 【为什么不用 protobuf 编码 Body 内部？】
//   1. 消息体结构简单固定，手工二进制编码比 protobuf 更紧凑、零 GC 开销；
//   2. 避免 protobuf 反射带来的额外内存分配，对高吞吐 IM 场景更友好；
//   3. msg_id 变长设计保留了灵活性，其余字段固定长度便于零拷贝解析。
//
// 【编码约定】
//   - 所有多字节整数统一使用 Big-Endian（网络字节序），方便跨平台一致解析。
//   - 变长字段（如 msg_id）采用「长度前缀 + 数据」模式，长度前缀为 2 字节 uint16。
//   - 末尾可变长度字段（如 MsgBody.Content）不设长度前缀，直接读到缓冲区末尾。
//
// 【各消息体与操作码的对应关系】
//   - MsgBody   → OpSendMsg (客户端发消息) / OpSendMsgReply (服务器回执) / OpPushMsg (推送)
//   - AckBody   → OpSendMsgAck (消息确认) / OpPushMsgAck (推送确认)
//   - SyncReqBody  → OpSyncReq (离线同步请求)
//   - SyncReplyBody → OpSyncReply (离线同步回复)
// =====================================================================

package protocol

import (
	"encoding/binary"
	"errors"
)

// =====================================================================
// MsgBody — 聊天消息体
// =====================================================================
//
// MsgBody 是 goim 系统中最核心的消息结构，承载一条完整的聊天消息。
// 它作为 Proto.Body 的有效载荷，在以下场景中使用：
//
//   - 客户端 → 服务器（OpSendMsg）：客户端发送聊天消息时，将 MsgBody 编码后放入 Proto.Body。
//   - 服务器 → 客户端（OpSendMsgReply）：服务器确认消息已接收，返回包含 msg_id 的 MsgBody。
//   - 服务器 → 客户端（OpPushMsg）：服务器主动推送消息给目标用户（单聊/群聊）。
//   - 离线同步（OpSyncReply）：客户端重连后拉取离线消息，SyncReplyBody 中包含多条 MsgBody。
//
// 二进制编码格式：
//
//	+------------------+----------+----------+-----------+----------+---------+
//	| msg_id_len (2B)  | msg_id   | from_uid | to_uid    | timestamp| seq     | content |
//	| uint16 BigEndian | []byte   | int64    | int64     | int64    | int64   | []byte  |
//	+------------------+----------+----------+-----------+----------+---------+
//	|<---- 变长 ---->|<-- 8B --><-- 8B --><--- 8B ---><--- 8B ---><-- 剩余 -->|
//
// 总长度 = 2 + len(msg_id) + 8 + 8 + 8 + 8 + len(content)
//
//	= 34 + len(msg_id) + len(content)  (最小值，当 msg_id 和 content 为空时)
type MsgBody struct {
	// MsgID 全局唯一消息 ID，由服务器在 OpSendMsgReply 中分配并返回给客户端。
	// 格式通常为 snowflake 算法生成的字符串，保证趋势递增且全局唯一。
	// 客户端在后续 ACK / 消息查询中通过此 ID 引用特定消息。
	// 最大长度 65535 字节（受 uint16 长度前缀限制）。
	MsgID string

	// FromUID 发送者用户 ID（int64）。
	// 标识消息的发送方，在消息路由、权限校验、存储索引等环节使用。
	FromUID int64

	// ToUID 接收者用户 ID（int64）。
	// 标识消息的接收方。对于单聊即对方 UID；对于群聊即群 ID（也用 int64 表示）。
	// 路由层根据此字段决定将消息投递给哪个 Comet 节点的哪些连接。
	ToUID int64

	// Timestamp 消息时间戳（Unix 毫秒）。
	// 由服务器在接收消息时生成，用于消息排序、离线同步游标、历史消息查询等。
	// 客户端发送时可填 0，服务器会覆盖为权威时间。
	Timestamp int64

	// Seq 消息序号（sequence number）。
	// 由服务器分配，同一用户维度单调递增，用于：
	//   1. 客户端检测消息丢失（seq 不连续则有遗漏）；
	//   2. 离线同步的游标（SyncReqBody.LastSeq 就是从此字段恢复）；
	//   3. ACK 回执中引用具体哪条消息（AckBody.Seq）。
	Seq int64

	// Content 消息正文（任意字节）。
	// 承载实际的聊天内容，编码格式由上层业务决定（JSON/Protobuf/纯文本等）。
	// goim 本身不关心 Content 的语义，只负责透传。
	// 作为最后一个字段，无需长度前缀，直接读到缓冲区末尾即可。
	Content []byte
}

// MarshalMsgBody 将 MsgBody 序列化为二进制字节切片。
//
// 编码格式：[msg_id_len:2][msg_id:N][from_uid:8][to_uid:8][timestamp:8][seq:8][content...]
//
// 返回值：
//   - []byte: 编码后的字节数据，可直接赋值给 Proto.Body
//   - error:  当 msg_id 长度超过 65535 字节时返回错误
//
// 注意：此函数会分配一块连续的 []byte 并一次性拷贝所有字段，适合单次编码场景。
// 如果需要高频编码且想减少 GC 压力，调用方可预分配 buf 并自行组装。
func MarshalMsgBody(mb *MsgBody) ([]byte, error) {
	msgIDBytes := []byte(mb.MsgID)
	// msg_id 长度用 uint16 存储，最大 65535 字节，超出则拒绝编码
	if len(msgIDBytes) > 65535 {
		return nil, errors.New("msg_id too long")
	}
	// 预计算总长度：2(长度前缀) + N(msg_id) + 8*4(四个 int64 固定字段) + len(content)
	size := 2 + len(msgIDBytes) + 8 + 8 + 8 + 8 + len(mb.Content)
	buf := make([]byte, size)

	// 写入 msg_id 长度前缀（2 字节，Big-Endian）
	binary.BigEndian.PutUint16(buf[0:2], uint16(len(msgIDBytes)))
	// 写入 msg_id 原始字节
	copy(buf[2:2+len(msgIDBytes)], msgIDBytes)

	// 依次写入四个固定长度的 int64 字段（转为 uint64 保持位模式不变）
	offset := 2 + len(msgIDBytes)
	binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(mb.FromUID))
	offset += 8
	binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(mb.ToUID))
	offset += 8
	binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(mb.Timestamp))
	offset += 8
	binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(mb.Seq))
	offset += 8

	// Content 是最后一个字段，直接拷贝到缓冲区末尾，无需长度前缀
	copy(buf[offset:], mb.Content)

	return buf, nil
}

// UnmarshalMsgBody 从二进制字节切片反序列化出 MsgBody。
//
// 解码过程与 MarshalMsgBody 严格对称：
//  1. 读取前 2 字节获取 msg_id 长度；
//  2. 读取 msg_id；
//  3. 依次读取 from_uid、to_uid、timestamp、seq（各 8 字节）；
//  4. 剩余字节全部作为 Content。
//
// 返回值：
//   - *MsgBody: 解码后的消息体指针
//   - error:    数据长度不足或格式错误时返回错误
//
// 注意：返回的 MsgBody.Content 与输入 data 共享底层内存（零拷贝）。
// 如果调用方需要长期持有 Content 且 data 会被复用，应自行 copy。
func UnmarshalMsgBody(data []byte) (*MsgBody, error) {
	// 至少需要 2 字节来读取 msg_id 长度前缀
	if len(data) < 2 {
		return nil, errors.New("data too short for msg_id length")
	}
	msgIDLen := int(binary.BigEndian.Uint16(data[0:2]))
	// 总共至少需要 2(长度前缀) + msgIDLen + 32(四个 int64 固定字段) 字节
	if len(data) < 2+msgIDLen+32 {
		return nil, errors.New("data too short for MsgBody header")
	}

	mb := &MsgBody{}
	mb.MsgID = string(data[2 : 2+msgIDLen])

	// 按序读取四个 int64 字段（从 uint64 位模式转回 int64）
	offset := 2 + msgIDLen
	mb.FromUID = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
	offset += 8
	mb.ToUID = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
	offset += 8
	mb.Timestamp = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
	offset += 8
	mb.Seq = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
	offset += 8

	// 剩余字节全部作为 Content（可能为空，例如 ACK 回执场景）
	if offset < len(data) {
		mb.Content = data[offset:]
	}

	return mb, nil
}

// =====================================================================
// AckBody — 消息确认（ACK）体
// =====================================================================
//
// AckBody 用于客户端向服务器确认「已收到某条消息」。
// 在 goim 的消息投递流程中，服务器通过 OpPushMsg 将消息推送给客户端后，
// 客户端需要回复 OpPushMsgAck，其中 Proto.Body 携带编码后的 AckBody，
// 告知服务器「我已收到 msg_id=X、seq=Y 的消息，可以从离线队列中移除了」。
//
// 服务器收到 ACK 后会：
//  1. 将消息状态标记为已投递（UpdateMessageStatus）；
//  2. 从该用户的离线队列中移除该消息（RemoveFromOfflineQueue）；
//  3. 可选：向发送者发送「已送达」回执。
//
// 二进制编码格式：
//
//	+------------------+----------+---------+
//	| msg_id_len (2B)  | msg_id   | seq     |
//	| uint16 BigEndian | []byte   | int64   |
//	+------------------+----------+---------+
//
// 总长度 = 2 + len(msg_id) + 8
type AckBody struct {
	// MsgID 被确认消息的全局唯一 ID。
	// 与 MsgBody.MsgID 对应，告诉服务器「我在确认哪条消息」。
	MsgID string

	// Seq 被确认消息的序号。
	// 与 MsgBody.Seq 对应，提供冗余校验——服务器可同时用 msg_id 和 seq
	// 双重匹配，防止因 ID 生成异常导致的误确认。
	Seq int64
}

// MarshalAckBody 将 AckBody 序列化为二进制字节切片。
//
// 编码格式：[msg_id_len:2][msg_id:N][seq:8]
//
// 返回值：
//   - []byte: 编码后的字节数据，赋值给 OpSendMsgAck / OpPushMsgAck 的 Proto.Body
//   - error:  当 msg_id 长度超过 65535 字节时返回错误
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

// UnmarshalAckBody 从二进制字节切片反序列化出 AckBody。
//
// 返回值：
//   - *AckBody: 解码后的 ACK 体指针
//   - error:    数据长度不足时返回错误
func UnmarshalAckBody(data []byte) (*AckBody, error) {
	// 至少需要 2 字节来读取 msg_id 长度前缀
	if len(data) < 2 {
		return nil, errors.New("data too short for ack")
	}
	msgIDLen := int(binary.BigEndian.Uint16(data[0:2]))
	// 总共至少需要 2(长度前缀) + msgIDLen + 8(seq) 字节
	if len(data) < 2+msgIDLen+8 {
		return nil, errors.New("data too short for AckBody")
	}

	ab := &AckBody{}
	ab.MsgID = string(data[2 : 2+msgIDLen])
	offset := 2 + msgIDLen
	ab.Seq = int64(binary.BigEndian.Uint64(data[offset : offset+8]))

	return ab, nil
}

// =====================================================================
// SyncReqBody — 离线消息同步请求体
// =====================================================================
//
// SyncReqBody 用于客户端向服务器请求拉取离线期间未收到的消息。
// 当客户端重连（WebSocket/TCP 建立连接后），发送 OpSyncReq 操作码的 Proto，
// 其 Proto.Body 携带编码后的 SyncReqBody，告诉服务器：
//
//	「我上次收到的消息序号是 LastSeq，请给我之后的 Limit 条消息」
//
// 服务器收到后会：
//  1. 根据用户 UID 和 LastSeq 查询离线消息队列/持久化存储；
//  2. 返回不超过 Limit 条消息，封装在 SyncReplyBody 中；
//  3. 如果还有更多消息，设置 HasMore=true，客户端可继续发起下一轮同步。
//
// 二进制编码格式（固定 12 字节，无变长字段）：
//
//	+-----------+----------+
//	| last_seq  | limit    |
//	| int64     | int32    |
//	+-----------+----------+
//	|<-- 8B --><-- 4B ---->|
type SyncReqBody struct {
	// LastSeq 客户端上次收到的最后一条消息的序号。
	// 服务器从此序号之后开始查询，确保不重复投递已收到的消息。
	// 如果客户端是首次连接或本地无缓存，可传 0 表示从头同步。
	LastSeq int64

	// Limit 本次请求最多返回的消息条数。
	// 用于分页控制，防止一次性拉取过多消息导致内存/带宽压力。
	// 典型值：50~200，由客户端根据 UI 渲染能力决定。
	Limit int32
}

// MarshalSyncReq 将 SyncReqBody 序列化为二进制字节切片。
//
// 编码格式：[last_seq:8][limit:4]（固定 12 字节）
//
// 此函数不会返回 error，因为 SyncReqBody 是纯固定长度结构，不存在编码失败的可能。
func MarshalSyncReq(sr *SyncReqBody) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint64(buf[0:8], uint64(sr.LastSeq))
	binary.BigEndian.PutUint32(buf[8:12], uint32(sr.Limit))
	return buf
}

// UnmarshalSyncReq 从二进制字节切片反序列化出 SyncReqBody。
//
// 返回值：
//   - *SyncReqBody: 解码后的同步请求体指针
//   - error:        数据长度不足 12 字节时返回错误
func UnmarshalSyncReq(data []byte) (*SyncReqBody, error) {
	if len(data) < 12 {
		return nil, errors.New("data too short for SyncReq")
	}
	return &SyncReqBody{
		LastSeq: int64(binary.BigEndian.Uint64(data[0:8])),
		Limit:   int32(binary.BigEndian.Uint32(data[8:12])),
	}, nil
}

// =====================================================================
// SyncReplyBody — 离线消息同步回复体
// =====================================================================
//
// SyncReplyBody 用于服务器向客户端返回离线消息同步结果。
// 对应操作码 OpSyncReply，服务器将 SyncReplyBody 编码后放入 Proto.Body 返回。
//
// 客户端收到后会：
//  1. 遍历 Messages 逐条渲染到聊天界面；
//  2. 记录 CurrentSeq 作为下次同步的 LastSeq；
//  3. 如果 HasMore 为 true，继续发送 OpSyncReq 拉取下一批。
//
// 二进制编码格式：
//
//	+--------------+-----------+------------+-----------------------------------+
//	| current_seq  | has_more  | msg_count  | messages (变长数组)                |
//	| int64        | bool (1B) | uint32     | [msg_len:4][msg_body] * msg_count |
//	+--------------+-----------+------------+-----------------------------------+
//	|<--- 8B ----><--- 1B ---><--- 4B ----><---------- 变长 ----------------->|
//
// 每条消息的编码方式：
//
//	[msg_len:4][MarshalMsgBody 的输出]
//	其中 msg_len 是该条消息序列化后的字节长度（uint32 Big-Endian），
//	使得解析器可以跳过不需要的消息或提前终止。
type SyncReplyBody struct {
	// CurrentSeq 服务器端当前的最新消息序号。
	// 客户端应将此值保存为下次同步的 LastSeq 起点。
	// 即使 Messages 为空（无新消息），CurrentSeq 仍有意义——
	// 它告诉客户端「服务器已经推进到这个序号了」。
	CurrentSeq int64

	// HasMore 是否还有更多未同步的消息。
	// true 表示本次返回的消息数达到了 Limit 上限，客户端应继续发起
	// OpSyncReq（以本次 CurrentSeq 作为新的 LastSeq）来拉取剩余消息。
	// false 表示所有离线消息已同步完毕。
	HasMore bool

	// Messages 本次返回的离线消息列表。
	// 按 Seq 升序排列，客户端应按顺序逐条处理。
	// 可能为空（例如用户无离线消息），此时 HasMore 必为 false。
	Messages []*MsgBody
}

// MarshalSyncReply 将 SyncReplyBody 序列化为二进制字节切片。
//
// 编码格式：
//
//	[current_seq:8][has_more:1][msg_count:4]
//	[msg0_len:4][msg0_body...]
//	[msg1_len:4][msg1_body...]
//	...
//
// 返回值：
//   - []byte: 编码后的字节数据，赋值给 OpSyncReply 的 Proto.Body
//   - error:  任何一条 MsgBody 序列化失败时返回错误
func MarshalSyncReply(sr *SyncReplyBody) ([]byte, error) {
	// 第一步：将所有 MsgBody 预先序列化，收集编码后的字节块
	var msgBytes [][]byte
	for _, m := range sr.Messages {
		b, err := MarshalMsgBody(m)
		if err != nil {
			return nil, err
		}
		msgBytes = append(msgBytes, b)
	}

	// 第二步：计算总缓冲区大小
	// 固定部分：8(current_seq) + 1(has_more) + 4(msg_count) = 13 字节
	// 变长部分：每条消息 4 字节长度前缀 + 实际序列化数据
	totalSize := 8 + 1 + 4
	for _, b := range msgBytes {
		totalSize += 4 + len(b) // 4 字节长度前缀 + 消息体
	}

	// 第三步：一次性分配并写入
	buf := make([]byte, totalSize)

	// 写入 current_seq（int64 → uint64 保持位模式）
	binary.BigEndian.PutUint64(buf[0:8], uint64(sr.CurrentSeq))
	// 写入 has_more（单字节布尔值：1=true, 0=false）
	if sr.HasMore {
		buf[8] = 1
	}
	// 写入消息条数（uint32）
	binary.BigEndian.PutUint32(buf[9:13], uint32(len(msgBytes)))

	// 第四步：逐条写入消息，每条前面加 4 字节长度前缀
	offset := 13
	for _, b := range msgBytes {
		binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(b)))
		offset += 4
		copy(buf[offset:], b)
		offset += len(b)
	}

	return buf, nil
}

// UnmarshalSyncReply 从二进制字节切片反序列化出 SyncReplyBody。
//
// 解码过程与 MarshalSyncReply 严格对称。
// 对每条消息会递归调用 UnmarshalMsgBody 进行反序列化。
//
// 返回值：
//   - *SyncReplyBody: 解码后的同步回复体指针
//   - error:          数据长度不足或消息截断时返回错误
//
// 注意：如果消息条数很多，此函数会为每条 MsgBody 分配独立的内存。
// 在极端场景下（如数千条离线消息），调用方应考虑分批同步（Limit 参数）。
func UnmarshalSyncReply(data []byte) (*SyncReplyBody, error) {
	// 至少需要 13 字节：8(current_seq) + 1(has_more) + 4(msg_count)
	if len(data) < 13 {
		return nil, errors.New("data too short for SyncReply")
	}

	sr := &SyncReplyBody{
		CurrentSeq: int64(binary.BigEndian.Uint64(data[0:8])),
		HasMore:    data[8] == 1,
	}
	msgCount := int(binary.BigEndian.Uint32(data[9:13]))
	offset := 13

	// 逐条解析消息
	for i := 0; i < msgCount; i++ {
		// 检查是否还有足够字节读取 4 字节的消息长度前缀
		if offset+4 > len(data) {
			return nil, errors.New("data truncated in SyncReply messages")
		}
		msgLen := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4

		// 检查剩余字节是否足够容纳该条消息的完整数据
		if offset+msgLen > len(data) {
			return nil, errors.New("data truncated in SyncReply message body")
		}

		// 递归反序列化单条 MsgBody（传入切片避免额外拷贝）
		mb, err := UnmarshalMsgBody(data[offset : offset+msgLen])
		if err != nil {
			return nil, err
		}
		sr.Messages = append(sr.Messages, mb)
		offset += msgLen
	}

	return sr, nil
}
