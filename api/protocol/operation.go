package protocol

// operation.go 定义了 goim 二进制协议中的所有**操作码（Op Code）**。
//
// 【操作码是什么？】
// 操作码是协议头中 Op 字段的值，用来告诉接收方"这个包是干什么的"。
// 比如客户端发来 OpHeartbeat，服务器就知道这是一个心跳包，应该回复 OpHeartbeatReply。
//
// 【通信流程示例】
//   客户端                    服务器
//     |--- OpHandshake -------->|   （握手：建立连接）
//     |<-- OpHandshakeReply ----|   （握手回复）
//     |--- OpAuth ------------->|   （认证：携带 token）
//     |<-- OpAuthReply ---------|   （认证回复）
//     |--- OpHeartbeat -------->|   （心跳：保活）
//     |<-- OpHeartbeatReply ----|   （心跳回复，携带在线人数）
//     |--- OpSendMsg ---------->|   （发消息）
//     |<-- OpSendMsgReply ------|   （发消息回复）
//     |<-- OpSendMsgAck --------|   （服务器确认消息已收到，携带 msg_id）
//     |<-- OpPushMsgAck --------|   （客户端确认推送消息已收到）

const (
	// OpHandshake 握手请求：客户端连接后发送的第一个包，用于协议版本协商
	OpHandshake = int32(0)
	// OpHandshakeReply 握手回复：服务器确认握手成功
	OpHandshakeReply = int32(1)

	// OpHeartbeat 心跳请求：客户端定期发送，用于保持连接活跃并告知服务器自己还在线
	OpHeartbeat = int32(2)
	// OpHeartbeatReply 心跳回复：服务器回复，附带当前房间的在线人数
	OpHeartbeatReply = int32(3)

	// OpSendMsg 发送消息：客户端向服务器发送聊天消息
	OpSendMsg = int32(4)
	// OpSendMsgReply 发送消息回复：服务器确认收到客户端的消息
	OpSendMsgReply = int32(5)

	// OpDisconnectReply 断开连接回复：服务器通知客户端连接即将断开
	OpDisconnectReply = int32(6)

	// OpAuth 认证请求：客户端在握手后发送，携带用户 token 用于身份验证
	OpAuth = int32(7)
	// OpAuthReply 认证回复：服务器返回认证结果（成功/失败）
	OpAuthReply = int32(8)

	// OpRaw 原始消息：一种特殊操作码，表示 Body 已经是预编码好的完整协议包，
	// 不需要再封装协议头。用于批量消息推送时的性能优化——
	// Job 层先把多个消息拼到一起，到 Comet 层直接整块发送。
	OpRaw = int32(9)

	// OpProtoReady 协议就绪：内部状态标记，表示 Proto 结构体已准备好被处理
	OpProtoReady = int32(10)
	// OpProtoFinish 协议完成：内部状态标记，表示 Proto 处理完毕，连接可以关闭
	OpProtoFinish = int32(11)

	// OpChangeRoom 切换房间：客户端请求从当前聊天房间切换到另一个房间
	OpChangeRoom = int32(12)
	// OpChangeRoomReply 切换房间回复：服务器确认房间切换成功
	OpChangeRoomReply = int32(13)

	// OpSub 订阅：客户端请求订阅某个主题（如某个聊天频道）
	OpSub = int32(14)
	// OpSubReply 订阅回复：服务器确认订阅成功
	OpSubReply = int32(15)

	// OpUnsub 取消订阅：客户端请求取消订阅某个主题
	OpUnsub = int32(16)
	// OpUnsubReply 取消订阅回复：服务器确认取消订阅成功
	OpUnsubReply = int32(17)

	// OpSendMsgAck 消息确认：服务器 -> 客户端，确认消息已被服务器接收并分配了 msg_id。
	// 客户端收到后就知道消息已经成功送达服务器，可以更新本地消息状态为"已发送"。
	OpSendMsgAck = int32(18)
	// OpPushMsgAck 推送确认：客户端 -> 服务器，确认推送消息已收到。
	// 服务器收到后可以将该消息从离线队列中移除，避免重复推送。
	OpPushMsgAck = int32(19)
	// OpSyncReq 同步请求：客户端 -> 服务器，请求拉取离线期间未收到的消息。
	// 通常在客户端重连后发送，用于补发断线期间错过的消息。
	OpSyncReq = int32(20)
	// OpSyncReply 同步回复：服务器 -> 客户端，返回离线消息列表。
	OpSyncReply = int32(21)

	// OpKickConnection 踢连接：服务器 -> Comet，通知 Comet 断开某个客户端连接。
	// 用于多设备登录互踢、账号被封禁等场景。
	OpKickConnection = int32(22)
)
