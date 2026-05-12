package protocol

// protocol.go 是 goim 即时通讯系统的**二进制协议编解码层**。
//
// 【整体作用】
// 定义了客户端与服务器之间的二进制通信协议格式：
//   - 一个"协议包" = 协议头(16字节) + 消息体(Body)
//   - 协议头固定16字节，包含：包总长度(4B) + 头长度(2B) + 版本号(2B) + 操作码(4B) + 序列号(4B)
//   - 消息体是变长的，由具体业务决定
//
// 【协议头二进制布局（大端序）】
//   偏移量  字节数  字段名
//   0      4      packLen   — 整个包的长度（头+体）
//   4      2      headerLen — 协议头长度（固定16）
//   6      2      ver       — 协议版本号
//   8      4      op        — 操作码（握手/心跳/发消息/认证等）
//   12     4      seq       — 序列号，用于请求-响应配对
//   16     变长    body      — 消息体（protobuf 或原始字节）
//
// 【使用场景】
// TCP 连接和 WebSocket 连接都使用这套协议格式进行通信。
// 服务器内部模块间通信也复用 Proto 结构体。

import (
	"errors"

	"github.com/Terry-Mao/goim/pkg/bufio"
	"github.com/Terry-Mao/goim/pkg/bytes"
	"github.com/Terry-Mao/goim/pkg/encoding/binary"
	"github.com/Terry-Mao/goim/pkg/websocket"
)

const (
	// MaxBodySize 单个协议包消息体的最大长度：4KB（1左移12位 = 4096）
	MaxBodySize = int32(1 << 12)
)

const (
	// ====== 协议头各字段的字节大小 ======
	_packSize      = 4                                                       // packLen 字段：4字节（int32）
	_headerSize    = 2                                                       // headerLen 字段：2字节（int16）
	_verSize       = 2                                                       // 版本号字段：2字节（int16）
	_opSize        = 4                                                       // 操作码字段：4字节（int32）
	_seqSize       = 4                                                       // 序列号字段：4字节（int32）
	_heartSize     = 4                                                       // 心跳包体大小：4字节（携带在线人数）
	_rawHeaderSize = _packSize + _headerSize + _verSize + _opSize + _seqSize // 协议头总大小 = 16字节
	_maxPackSize   = MaxBodySize + int32(_rawHeaderSize)                     // 单包最大长度 = 4KB + 16字节

	// ====== 协议头各字段在字节缓冲区中的起始偏移量 ======
	_packOffset   = 0                           // packLen 从第0字节开始
	_headerOffset = _packOffset + _packSize     // headerLen 从第4字节开始
	_verOffset    = _headerOffset + _headerSize // 版本号从第6字节开始
	_opOffset     = _verOffset + _verSize       // 操作码从第8字节开始
	_seqOffset    = _opOffset + _opSize         // 序列号从第12字节开始
	_heartOffset  = _seqOffset + _seqSize       // 心跳体从第16字节开始（紧跟协议头之后）
)

var (
	// ErrProtoPackLen 收到的包长度超过最大限制时返回此错误
	ErrProtoPackLen = errors.New("default server codec pack length error")
	// ErrProtoHeaderLen 收到的协议头长度不等于预期的16字节时返回此错误
	ErrProtoHeaderLen = errors.New("default server codec header length error")
)

var (
	// ProtoReady 一个特殊的 Proto 实例，表示连接已就绪（内部状态机用）
	ProtoReady = &Proto{Op: OpProtoReady}
	// ProtoFinish 一个特殊的 Proto 实例，表示连接即将关闭（内部状态机用）
	ProtoFinish = &Proto{Op: OpProtoFinish}
)

// WriteTo 将 Proto 序列化为二进制写入 bytes.Writer。
// 用于**服务器内部模块间**的消息传递（如 Worker -> Job -> Comet）。
// 写入格式：协议头(16字节) + Body(变长)
func (p *Proto) WriteTo(b *bytes.Writer) {
	var (
		packLen = _rawHeaderSize + int32(len(p.Body)) // 包总长 = 头 + 体
		buf     = b.Peek(_rawHeaderSize)              // 预留16字节给协议头
	)
	// 按大端序依次写入协议头的5个字段
	binary.BigEndian.PutInt32(buf[_packOffset:], packLen)
	binary.BigEndian.PutInt16(buf[_headerOffset:], int16(_rawHeaderSize))
	binary.BigEndian.PutInt16(buf[_verOffset:], int16(p.Ver))
	binary.BigEndian.PutInt32(buf[_opOffset:], p.Op)
	binary.BigEndian.PutInt32(buf[_seqOffset:], p.Seq)
	if p.Body != nil {
		b.Write(p.Body)
	}
}

// ReadTCP 从 TCP 连接的缓冲读取器中解析一个完整的协议包。
// 流程：先读16字节协议头 -> 解析出包长度和头长度 -> 校验 -> 再读消息体
func (p *Proto) ReadTCP(rr *bufio.Reader) (err error) {
	var (
		bodyLen   int
		headerLen int16
		packLen   int32
		buf       []byte
	)
	// 第一步：精确读取16字节协议头
	if buf, err = rr.Pop(_rawHeaderSize); err != nil {
		return
	}
	// 第二步：从协议头字节中解析出各字段
	packLen = binary.BigEndian.Int32(buf[_packOffset:_headerOffset])  // 总包长
	headerLen = binary.BigEndian.Int16(buf[_headerOffset:_verOffset]) // 头长度
	p.Ver = int32(binary.BigEndian.Int16(buf[_verOffset:_opOffset]))  // 版本号
	p.Op = binary.BigEndian.Int32(buf[_opOffset:_seqOffset])          // 操作码
	p.Seq = binary.BigEndian.Int32(buf[_seqOffset:])                  // 序列号
	// 第三步：安全校验
	if packLen > _maxPackSize {
		return ErrProtoPackLen // 包太大，拒绝
	}
	if headerLen != _rawHeaderSize {
		return ErrProtoHeaderLen // 头长度不对，拒绝
	}
	// 第四步：计算消息体长度并读取
	if bodyLen = int(packLen - int32(headerLen)); bodyLen > 0 {
		p.Body, err = rr.Pop(bodyLen)
	} else {
		p.Body = nil // 没有消息体（如纯心跳包走不到这里，心跳用专用方法）
	}
	return
}

// WriteTCP 将 Proto 通过 TCP 连接的缓冲写入器发送出去。
// 如果操作码是 OpRaw（原始消息），直接透写 Body，不做协议头封装——
// 因为 Job 层已经把多个协议包拼接好了，直接整块发出效率更高。
func (p *Proto) WriteTCP(wr *bufio.Writer) (err error) {
	var (
		buf     []byte
		packLen int32
	)
	if p.Op == OpRaw {
		// 原始消息：Body 已经是完整的协议包（含协议头），直接写入，不做任何处理
		_, err = wr.WriteRaw(p.Body)
		return
	}
	// 普通消息：组装协议头 + 写入消息体
	packLen = _rawHeaderSize + int32(len(p.Body))
	if buf, err = wr.Peek(_rawHeaderSize); err != nil {
		return
	}
	binary.BigEndian.PutInt32(buf[_packOffset:], packLen)
	binary.BigEndian.PutInt16(buf[_headerOffset:], int16(_rawHeaderSize))
	binary.BigEndian.PutInt16(buf[_verOffset:], int16(p.Ver))
	binary.BigEndian.PutInt32(buf[_opOffset:], p.Op)
	binary.BigEndian.PutInt32(buf[_seqOffset:], p.Seq)
	if p.Body != nil {
		_, err = wr.Write(p.Body)
	}
	return
}

// WriteTCPHeart 发送 TCP 心跳响应包。
// 心跳包的消息体是4字节的在线人数（online），告知客户端当前房间有多少人在线。
// 格式：协议头(16字节) + 在线人数(4字节) = 共20字节
func (p *Proto) WriteTCPHeart(wr *bufio.Writer, online int32) (err error) {
	var (
		buf     []byte
		packLen int
	)
	packLen = _rawHeaderSize + _heartSize // 16 + 4 = 20
	if buf, err = wr.Peek(packLen); err != nil {
		return
	}
	// 写协议头
	binary.BigEndian.PutInt32(buf[_packOffset:], int32(packLen))
	binary.BigEndian.PutInt16(buf[_headerOffset:], int16(_rawHeaderSize))
	binary.BigEndian.PutInt16(buf[_verOffset:], int16(p.Ver))
	binary.BigEndian.PutInt32(buf[_opOffset:], p.Op)
	binary.BigEndian.PutInt32(buf[_seqOffset:], p.Seq)
	// 写消息体：在线人数
	binary.BigEndian.PutInt32(buf[_heartOffset:], online)
	return
}

// ReadWebsocket 从 WebSocket 连接中读取一个完整的消息帧并解析为 Proto。
// 与 ReadTCP 不同：WebSocket 是消息驱动的，一次 ReadMessage 就能拿到完整的一帧数据，
// 不需要像 TCP 那样分步读取头和体。
func (p *Proto) ReadWebsocket(ws *websocket.Conn) (err error) {
	var (
		bodyLen   int
		headerLen int16
		packLen   int32
		buf       []byte
	)
	// 一次性读取整个 WebSocket 消息帧
	if _, buf, err = ws.ReadMessage(); err != nil {
		return
	}
	if len(buf) < _rawHeaderSize {
		return ErrProtoPackLen // 数据太短，连协议头都不够
	}
	// 解析协议头各字段
	packLen = binary.BigEndian.Int32(buf[_packOffset:_headerOffset])
	headerLen = binary.BigEndian.Int16(buf[_headerOffset:_verOffset])
	p.Ver = int32(binary.BigEndian.Int16(buf[_verOffset:_opOffset]))
	p.Op = binary.BigEndian.Int32(buf[_opOffset:_seqOffset])
	p.Seq = binary.BigEndian.Int32(buf[_seqOffset:])
	// 安全校验
	if packLen < 0 || packLen > _maxPackSize {
		return ErrProtoPackLen
	}
	if headerLen != _rawHeaderSize {
		return ErrProtoHeaderLen
	}
	// 提取消息体（直接从已读取的 buf 中切片，无需额外读取）
	if bodyLen = int(packLen - int32(headerLen)); bodyLen > 0 {
		p.Body = buf[headerLen:packLen]
	} else {
		p.Body = nil
	}
	return
}

// WriteWebsocket 将 Proto 通过 WebSocket 连接发送出去。
// WebSocket 以"消息帧"为单位发送，需要先声明帧长度，再写入数据。
//
// 特殊处理 OpRaw：
// 当操作码为 OpRaw 时，Body 已经是一个完整的序列化后的 Proto（含协议头），
// 由 Worker/Job/CometPusher 的 WriteTo 预先编码。
// 此时需要剥掉内层协议头，只发送数据部分，并重新组装外层协议头。
// 这样做的目的是：批量推送消息时，先用 WriteTo 把多个消息拼到一起，
// 到 WebSocket 发送时再统一处理协议头，避免重复封装。
func (p *Proto) WriteWebsocket(ws *websocket.Conn) (err error) {
	var (
		buf     []byte
		packLen int
	)
	if p.Op == OpRaw {
		// Body 已经是完整的协议包（头+体），剥掉内层头，只取数据部分
		if len(p.Body) > _rawHeaderSize {
			data := p.Body[_rawHeaderSize:] // 跳过内层16字节协议头，取出纯数据
			packLen = _rawHeaderSize + len(data)
			// 先写 WebSocket 帧头（声明本次消息的总字节数）
			if err = ws.WriteHeader(websocket.BinaryMessage, packLen); err != nil {
				return
			}
			// 再写外层协议头（16字节）
			if buf, err = ws.Peek(_rawHeaderSize); err != nil {
				return
			}
			binary.BigEndian.PutInt32(buf[_packOffset:], int32(packLen))
			binary.BigEndian.PutInt16(buf[_headerOffset:], int16(_rawHeaderSize))
			binary.BigEndian.PutInt16(buf[_verOffset:], int16(p.Ver))
			binary.BigEndian.PutInt32(buf[_opOffset:], p.Op)
			binary.BigEndian.PutInt32(buf[_seqOffset:], p.Seq)
			// 最后写数据部分
			err = ws.WriteBody(data)
			return
		}
		// 兜底：Body 没有预编码，走普通发送逻辑
	}
	// 普通消息的发送流程：WebSocket帧头 + 协议头 + 消息体
	packLen = _rawHeaderSize + len(p.Body)
	if err = ws.WriteHeader(websocket.BinaryMessage, packLen); err != nil {
		return
	}
	if buf, err = ws.Peek(_rawHeaderSize); err != nil {
		return
	}
	binary.BigEndian.PutInt32(buf[_packOffset:], int32(packLen))
	binary.BigEndian.PutInt16(buf[_headerOffset:], int16(_rawHeaderSize))
	binary.BigEndian.PutInt16(buf[_verOffset:], int16(p.Ver))
	binary.BigEndian.PutInt32(buf[_opOffset:], p.Op)
	binary.BigEndian.PutInt32(buf[_seqOffset:], p.Seq)
	if p.Body != nil {
		err = ws.WriteBody(p.Body)
	}
	return
}

// WriteWebsocketHeart 发送 WebSocket 心跳响应包。
// 和 TCP 心跳一样，消息体是4字节的在线人数。
// 流程：WebSocket帧头 -> 协议头(16字节) -> 在线人数(4字节)
func (p *Proto) WriteWebsocketHeart(wr *websocket.Conn, online int32) (err error) {
	var (
		buf     []byte
		packLen int
	)
	packLen = _rawHeaderSize + _heartSize // 16 + 4 = 20
	// 写 WebSocket 帧头
	if err = wr.WriteHeader(websocket.BinaryMessage, packLen); err != nil {
		return
	}
	if buf, err = wr.Peek(packLen); err != nil {
		return
	}
	// 写协议头
	binary.BigEndian.PutInt32(buf[_packOffset:], int32(packLen))
	binary.BigEndian.PutInt16(buf[_headerOffset:], int16(_rawHeaderSize))
	binary.BigEndian.PutInt16(buf[_verOffset:], int16(p.Ver))
	binary.BigEndian.PutInt32(buf[_opOffset:], p.Op)
	binary.BigEndian.PutInt32(buf[_seqOffset:], p.Seq)
	// 写消息体：在线人数
	binary.BigEndian.PutInt32(buf[_heartOffset:], online)
	return
}
