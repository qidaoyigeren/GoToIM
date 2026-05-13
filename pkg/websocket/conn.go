// websocket 包 - WebSocket 协议底层实现
// 基于 RFC 6455（The WebSocket Protocol）实现的轻量级 WebSocket 连接封装。
// 不依赖第三方 WebSocket 库，仅使用标准库的 io 和 encoding/binary 完成帧的读写。
//
// 核心职责：
//  1. WebSocket 帧的封包（WriteHeader + WriteBody）
//  2. WebSocket 帧的解析（ReadMessage → readFrame）
//  3. Ping/Pong 心跳的自动响应
//  4. 分片帧（continuation frame）的组装
//  5. Close 帧的识别与处理
//
// RFC 6455 帧格式（每个帧的二进制结构）：
//
//	 0                   1                   2                   3
//	 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
//	+-+-+-+-+-------+-+-------------+-------------------------------+
//	|F|R|R|R| opcode|M| Payload len |    Extended payload length    |
//	|I|S|S|S|  (4)  |A|     (7)     |             (16/64)           |
//	|N|V|V|V|       |S|             |   (if payload len==126/127)   |
//	| |1|2|3|       |K|             |                               |
//	+-+-+-+-+-------+-+-------------+ - - - - - - - - - - - - - - - +
//	|     Extended payload length continued, if payload len == 127  |
//	+ - - - - - - - - - - - - - - - +-------------------------------+
//	|                               |Masking-key, if MASK set to 1  |
//	+-------------------------------+-------------------------------+
//	| Masking-key (continued)       |          Payload Data         |
//	+-------------------------------- - - - - - - - - - - - - - - - +
//	:                     Payload Data continued ...                :
//	+ - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - +
//	|                     Payload Data continued ...                |
//	+---------------------------------------------------------------+
//
// 字节0: FIN(1bit) + RSV1(1bit) + RSV2(1bit) + RSV3(1bit) + OpCode(4bit)
// 字节1: MASK(1bit) + PayloadLength(7bit)
// 扩展长度: PayloadLength==126 → 2字节; PayloadLength==127 → 8字节
// 掩码密钥: MASK==1 → 4字节
// 负载数据: 剩余字节
package websocket

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/Terry-Mao/goim/pkg/bufio"
)

// ==========================================================================
// 帧首部字节 0 的位掩码定义（RFC 6455 Section 5.2）
// ==========================================================================
const (
	finBit  = 1 << 7 // FIN 位：指示这是消息的最后一个分片（0=还有后续帧, 1=最后一帧）
	rsv1Bit = 1 << 6 // RSV1 保留位：扩展协商时使用（如压缩扩展），未协商时必须为 0
	rsv2Bit = 1 << 5 // RSV2 保留位：同上
	rsv3Bit = 1 << 4 // RSV3 保留位：同上
	opBit   = 0x0f   // OpCode 掩码：取低 4 位作为操作码

	// 帧首部字节 1 的位掩码定义
	maskBit = 1 << 7 // MASK 位：指示负载数据是否经过掩码处理（客户端→服务端必须为 1）
	lenBit  = 0x7f   // Payload Length 掩码：取低 7 位作为基础长度

	// 分片帧相关常量
	continuationFrame        = 0   // 延续帧操作码：表示这是前一帧的延续数据
	continuationFrameMaxRead = 100 // 最大允许的连续分片帧数量（防止无限分片攻击）
)

// ==========================================================================
// WebSocket 消息类型定义（RFC 6455 Section 11.8）
// ==========================================================================
const (
	// TextMessage 文本消息（opcode=1），负载为 UTF-8 编码的文本
	TextMessage = 1

	// BinaryMessage 二进制消息（opcode=2），负载为任意二进制数据
	BinaryMessage = 2

	// CloseMessage 关闭连接控制帧（opcode=8），可选负载包含状态码和原因文本
	CloseMessage = 8

	// PingMessage Ping 心跳帧（opcode=9），接收方必须回复 Pong
	PingMessage = 9

	// PongMessage Pong 响应帧（opcode=10），作为 Ping 的响应
	PongMessage = 10
)

var (
	// ErrMessageClose 收到 Close 控制帧时返回，通知上层该连接应被关闭
	ErrMessageClose = errors.New("close control message")
	// ErrMessageMaxRead 连续分片超过最大限制时返回，防止恶意客户端发送无限分片耗尽内存
	ErrMessageMaxRead = errors.New("continuation frame max read")
)

// Conn 代表一个 WebSocket 连接。
//
// 字段说明：
//   - rwc:    底层 I/O 连接（通常是 net.Conn），提供最原始的读写关闭能力
//   - r:      带缓冲的 Reader，避免每次读取一个字节都触发系统调用
//   - w:      带缓冲的 Writer，积攒数据后批量写入，减少系统调用
//   - maskKey: 解码掩码密钥缓存。从客户端收到的帧需要用掩码密钥解码。
//     此处复用 []byte 避免每次读取帧时重新分配内存
type Conn struct {
	rwc     io.ReadWriteCloser // 底层原始连接
	r       *bufio.Reader      // 带缓冲的读取器
	w       *bufio.Writer      // 带缓冲的写入器
	maskKey []byte             // 掩码密钥缓冲区（4字节），复用减少 GC 压力
}

// newConn 创建新的 WebSocket 连接对象（内部使用）。
//
// 参数全部由外部传入，体现"依赖注入"的设计思想：
//   - rwc: 已建立的底层连接
//   - r:   已初始化的缓冲 Reader
//   - w:   已初始化的缓冲 Writer
//
// maskKey 预分配 4 字节，避免每次解掩码时重新分配。
func newConn(rwc io.ReadWriteCloser, r *bufio.Reader, w *bufio.Writer) *Conn {
	return &Conn{rwc: rwc, r: r, w: w, maskKey: make([]byte, 4)}
}

// WriteMessage 写入一条完整的 WebSocket 消息。
//
// 这是一个便捷方法，等价于先调用 WriteHeader 再调用 WriteBody。
// 服务端→客户端发送的帧不需要掩码（MASK=0），所以这里直接分两步写入即可。
//
// 参数说明：
//   - msgType: 消息类型（TextMessage=1, BinaryMessage=2）
//   - msg:     消息体的字节内容
func (c *Conn) WriteMessage(msgType int, msg []byte) (err error) {
	// 步骤1：写入帧首部（FIN=1, OpCode=msgType, MASK=0, PayloadLen=len(msg)）
	if err = c.WriteHeader(msgType, len(msg)); err != nil {
		return
	}
	// 步骤2：写入消息体
	err = c.WriteBody(msg)
	return
}

// WriteHeader 写入 WebSocket 帧首部（2~10 字节）。
//
// 帧首部构成：
//
//	字节0: FIN(1) | RSV(3) | OpCode(4)  → 固定 0x80 | msgType（服务端不设 RSV）
//	字节1: MASK(0) | PayloadLen(7)       → 长度编码
//	扩展字节: 0/2/8 字节（依据 PayloadLen 的取值）
//
// 长度编码规则：
//
//	length ≤ 125          → 直接放入字节1低7位
//	length 126~65535      → 字节1低7位填 126，后面用 2 字节存储真实长度（BigEndian）
//	length ≥ 65536        → 字节1低7位填 127，后面用 8 字节存储真实长度（BigEndian）
//
// 使用 bufio.Writer.Peek 预取空间再直接写入，避免多次 Write 调用。
func (c *Conn) WriteHeader(msgType int, length int) (err error) {
	var h []byte
	// Peek(2) 预取前 2 字节的缓冲区（不移动写入指针，但预留空间）
	if h, err = c.w.Peek(2); err != nil {
		return
	}
	// 字节0: FIN=1（finBit）| OpCode = msgType
	h[0] = 0
	h[0] |= finBit | byte(msgType)

	// 字节1: MASK=0（服务端→客户端不需要掩码）
	h[1] = 0

	// 根据长度选择编码方式
	switch {
	case length <= 125:
		// 长度 ≤125：直接写入低7位，无扩展长度字节
		h[1] |= byte(length)

	case length < 65536:
		// 长度 126~65535：标志位=126，再 Peek(2) 获取 2 字节扩展长度空间
		h[1] |= 126
		if h, err = c.w.Peek(2); err != nil {
			return
		}
		binary.BigEndian.PutUint16(h, uint16(length)) // 大端序写入 2 字节长度

	default:
		// 长度 ≥65536：标志位=127，再 Peek(8) 获取 8 字节扩展长度空间
		h[1] |= 127
		if h, err = c.w.Peek(8); err != nil {
			return
		}
		binary.BigEndian.PutUint64(h, uint64(length)) // 大端序写入 8 字节长度
	}
	return
}

// WriteBody 写入消息体。
//
// 直接调用 Wrtier.Write，不额外封装。如果长度为 0 则跳过。
// 注意：bufio.Writer.Write 只将数据写入内存缓冲区，需调用 Flush 才会真正发送。
func (c *Conn) WriteBody(b []byte) (err error) {
	if len(b) > 0 {
		_, err = c.w.Write(b)
	}
	return
}

// Peek 预取 n 字节的写入缓冲区（不提交，不发送）。
//
// 返回的切片直接指向 Writer 的内部缓冲区，修改它会直接影响后续写入内容。
// 典型使用场景：需要在写入前先计算某些值再填充（如协议头中的长度字段）。
func (c *Conn) Peek(n int) ([]byte, error) {
	return c.w.Peek(n)
}

// Flush 将缓冲区中的所有待写入数据刷新到底层连接。
//
// 每次写完一条完整消息后应调用此方法，确保数据真正发送到网络中。
func (c *Conn) Flush() error {
	return c.w.Flush()
}

// ReadMessage 读取一条完整的 WebSocket 消息。
//
// 这是 WebSocket 消息读取的核心方法。它循环调用 readFrame 读取帧，直到读到 FIN=1 的帧。
// 期间自动处理：
//   - 分片帧（continuation frame）：将多个帧的 payload 拼接成完整消息
//   - Ping 帧：自动回复 Pong
//   - Pong 帧：忽略（Pong 是 Ping 的响应，上层不需要关心）
//   - Close 帧：返回 ErrMessageClose 通知上层关闭连接
//
// 返回值说明：
//   - op:      消息类型（TextMessage=1 或 BinaryMessage=2）
//   - payload: 完整的消息体
//   - err:     io 错误或协议错误（ErrMessageClose / ErrMessageMaxRead）
//
// 为什么需要循环读取？
// WebSocket 消息可能被分割成多个帧发送（分片），每个帧的 FIN=0 表示还有后续帧，
// 直到读到 FIN=1 的帧才表示一条完整消息传输完毕。continuationFrameMaxRead 限制了
// 分片数量上限，防止恶意客户端发送无限 FIN=0 帧耗尽服务端内存。
func (c *Conn) ReadMessage() (op int, payload []byte, err error) {
	var (
		fin         bool   // 当前帧是否为最后一帧
		finOp, n    int    // finOp: 首帧的操作码，n: 已读取的分片帧计数
		partPayload []byte // 当前帧的负载数据
	)
	for {
		// 步骤1：读取一个原始帧
		if fin, op, partPayload, err = c.readFrame(); err != nil {
			return
		}

		switch op {
		case BinaryMessage, TextMessage, continuationFrame:
			// --- 数据帧处理 ---
			// 如果整个消息只有一个帧（fin=true 且之前没有累积数据），直接返回
			if fin && len(payload) == 0 {
				return op, partPayload, nil
			}
			// 多帧消息：将当前帧的负载追加到累积缓冲区
			payload = append(payload, partPayload...)
			// 记录首帧的操作码（后续 continuationFrame 的操作码是 0）
			if op != continuationFrame {
				finOp = op
			}
			// 读到最后一帧：用首帧的操作码作为消息类型返回
			if fin {
				op = finOp
				return
			}

		case PingMessage:
			// --- Ping 帧处理 ---
			// 收到 Ping 必须回复 Pong，负载原样返回（RFC 6455 要求）
			if err = c.WriteMessage(PongMessage, partPayload); err != nil {
				return
			}

		case PongMessage:
			// --- Pong 帧处理 ---
			// Pong 是 Ping 的响应，收到后无需任何操作（心跳由上层管理）

		case CloseMessage:
			// --- Close 帧处理 ---
			// 收到对端关闭请求，返回 ErrMessageClose 以通知上层执行优雅关闭
			err = ErrMessageClose
			return

		default:
			// 未知帧类型，协议错误
			err = fmt.Errorf("unknown control message, fin=%t, op=%d", fin, op)
			return
		}

		// 分片数量安全检查：超过上限则返回错误，防止无限分片导致的 OOM
		if n > continuationFrameMaxRead {
			err = ErrMessageMaxRead
			return
		}
		n++
	}
}

// readFrame 从连接中读取并解析一个原始的 WebSocket 帧。
//
// 这是 WebSocket 协议解析的最底层函数，严格按照 RFC 6455 帧格式解析。
//
// 解析流程：
//  1. 读取字节0 → 解析 FIN、RSV、OpCode
//  2. 读取字节1 → 解析 MASK、PayloadLength
//  3. 根据 PayloadLength 读取扩展长度字段（0/2/8 字节）
//  4. 如果 MASK=1 → 读取 4 字节掩码密钥
//  5. 根据 PayloadLength 读取负载数据
//  6. 如果 MASK=1 → 用掩码密钥对负载做 XOR 解码
//
// 返回值：
//   - fin:     是否为最后一帧
//   - op:      操作码（Text/Binary/Ping/Pong/Close）
//   - payload: 解码后的负载数据
//   - err:     读取或解析错误
func (c *Conn) readFrame() (fin bool, op int, payload []byte, err error) {
	var (
		b          byte   // 单字节读取缓冲区
		p          []byte // 多字节读取缓冲区（用于扩展长度和掩码）
		mask       bool   // 是否携带掩码（客户端→服务端必须为 true）
		maskKey    []byte // 4 字节掩码密钥
		payloadLen int64  // 负载数据长度
	)

	// ===== 步骤1: 读取字节0：FIN + RSV + OpCode =====
	//
	//   7   6   5   4   3   2   1   0
	//  +---+---+---+---+---+---+---+---+
	//  |FIN|RSV1|RSV2|RSV3|  OpCode   |
	//  +---+---+---+---+---+---+---+---+
	b, err = c.r.ReadByte()
	if err != nil {
		return
	}
	// 提取 FIN 位：b & finBit (0x80) != 0 表示这是消息的最后一帧
	fin = (b & finBit) != 0

	// 检查 RSV 保留位：根据 RFC 6455，未协商扩展时 RSV1/2/3 必须全为 0
	if rsv := b & (rsv1Bit | rsv2Bit | rsv3Bit); rsv != 0 {
		return false, 0, nil,
			fmt.Errorf("unexpected reserved bits rsv1=%d, rsv2=%d, rsv3=%d", b&rsv1Bit, b&rsv2Bit, b&rsv3Bit)
	}
	// 提取 OpCode：b & opBit (0x0f) 取低4位
	op = int(b & opBit)

	// ===== 步骤2: 读取字节1：MASK + PayloadLength =====
	//
	//   7   6   5   4   3   2   1   0
	//  +---+---+---+---+---+---+---+---+
	//  |MASK|     Payload Length       |
	//  +---+---+---+---+---+---+---+---+
	b, err = c.r.ReadByte()
	if err != nil {
		return
	}
	// 提取 MASK 位：b & maskBit (0x80) != 0 表示负载数据经过掩码处理
	mask = (b & maskBit) != 0

	// ===== 步骤3: 解析负载长度 =====
	//
	// PayloadLength 的编码：
	//   0~125   → 直接表示长度（7位足够）
	//   126     → 后面 2 字节以大端序存储真实长度（16位，最大 65535）
	//   127     → 后面 8 字节以大端序存储真实长度（64位，理论最大 2^63-1）
	switch b & lenBit {
	case 126:
		// 16 位扩展长度
		if p, err = c.r.Pop(2); err != nil {
			return
		}
		payloadLen = int64(binary.BigEndian.Uint16(p))
	case 127:
		// 64 位扩展长度
		if p, err = c.r.Pop(8); err != nil {
			return
		}
		payloadLen = int64(binary.BigEndian.Uint64(p))
	default:
		// 7 位长度（直接使用）
		payloadLen = int64(b & lenBit)
	}

	// ===== 步骤4: 读取掩码密钥 =====
	//
	// 根据 RFC 6455 Section 5.3：客户端→服务端的帧必须携带掩码（MASK=1）
	// 掩码密钥为 4 字节随机数，用于对负载做 XOR 混淆。
	// 注意：这里复用 c.maskKey 缓冲区而非每次分配，减少 GC 压力。
	if mask {
		maskKey, err = c.r.Pop(4)
		if err != nil {
			return
		}
		if c.maskKey == nil {
			c.maskKey = make([]byte, 4)
		}
		copy(c.maskKey, maskKey)
	}

	// ===== 步骤5: 读取负载数据 =====
	if payloadLen > 0 {
		if payload, err = c.r.Pop(int(payloadLen)); err != nil {
			return
		}
		// ===== 步骤6: 掩码解码 =====
		// 如果帧带有掩码，对负载执行 XOR 解码。
		// 掩码算法：payload[i] ^= maskKey[i % 4]
		// 核心性质：XOR 两次等于原值，所以编码和解码使用完全相同的算法。
		if mask {
			maskBytes(c.maskKey, 0, payload)
		}
	}
	return
}

// Close 关闭底层连接。
//
// 这是优雅关闭的一部分。上层应先发送 Close 帧（WriteMessage(CloseMessage, ...)），
// 再调用此方法关闭 TCP 连接。
func (c *Conn) Close() error {
	return c.rwc.Close()
}

// maskBytes 对 WebSocket 帧负载执行 XOR 掩码/解码操作。
//
// 掩码算法（RFC 6455 Section 5.3）：
//
//	for i = 0 to len(b)-1:
//	  b[i] = b[i] ^ key[(pos+i) % 4]
//
// 这是对称操作：编码和解码使用完全相同的算法（XOR 的性质）。
//
// 参数说明：
//   - key: 4 字节掩码密钥
//   - pos: 密钥的起始偏移位置（用于跨帧连续掩码场景）
//   - b:   需要掩码/解码的数据
//
// 返回值：下一个 pos 值（pos + len(b) 再对 4 取模），用于下次调用连续处理。
//
// 为什么客户端→服务端必须加掩码（Client-to-Server Masking）？
// WebSocket 设计的一个重要安全特性：防止"缓存投毒攻击"（Cache Poisoning）。
// 恶意攻击者可能通过在网络中放置代理，让浏览器将 WebSocket 请求误认为是 HTTP 请求，
// 从而缓存攻击者构造的内容。掩码确保客户端发出的内容是随机的，
// 即使被代理误解析为 HTTP 也不会命中已知缓存模式。
func maskBytes(key []byte, pos int, b []byte) int {
	for i := range b {
		b[i] ^= key[pos&3] // pos&3 等价于 pos % 4（位运算更快）
		pos++
	}
	return pos & 3
}
