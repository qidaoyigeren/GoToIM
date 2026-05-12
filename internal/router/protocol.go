package router

// protocol.go 是 goim 的**协议格式转换器**。
//
// 【整体作用】
// 在 HTTP API 的 JSON 格式与内部 protobuf 二进制格式之间做双向转换。
//
// 【为什么需要这个转换？】
// goim 的消息传递链路：
//   业务服务 --(HTTP JSON)--> Router --(内部 protobuf)--> Logic --> Worker --> Job --> Comet --> 客户端
//   外部业务系统发来的消息是 JSON 格式（友好、易调试），
//   但系统内部为了高效传输和存储，使用 protobuf 编码。
//   ProtocolConverter 就是这个"翻译官"，在边界处做格式转换。
//
// 【外部 JSON 格式】
//   {"from": 1001, "to": 1002, "text": "hello", "ts": 1699000000}
//
// 【内部 protobuf 格式（MsgBody）】
//   字段：FromUID, ToUID, Timestamp, Content

import (
	"encoding/json"
	"fmt"

	"github.com/Terry-Mao/goim/api/protocol"
)

// externalMessage 表示 HTTP API 客户端发送的 JSON 消息格式。
// 业务系统通过 HTTP 接口推送消息时，使用这个结构体的 JSON 形式。
type externalMessage struct {
	From int64  `json:"from"` // 发送者用户ID
	To   int64  `json:"to"`   // 接收者用户ID
	Text string `json:"text"` // 消息文本内容
	Ts   int64  `json:"ts"`   // 消息时间戳（Unix 秒）
}

// ProtocolConverter 协议格式转换器，负责在外部 JSON 和内部 protobuf 之间双向转换。
type ProtocolConverter struct{}

// NewProtocolConverter 创建一个新的协议转换器实例。
func NewProtocolConverter() *ProtocolConverter {
	return &ProtocolConverter{}
}

// ExternalToInternal 将外部 JSON 消息转换为内部 protobuf 编码的 MsgBody。
//
// 转换流程：
//  1. JSON 字节 -> externalMessage 结构体（反序列化）
//  2. externalMessage -> protocol.MsgBody（字段映射）
//  3. MsgBody -> protobuf 字节（序列化）
//
// 输入示例：{"from":1001,"to":1002,"text":"hello","ts":1699000000}
// 输出：protobuf 编码的 MsgBody 字节数组
func (c *ProtocolConverter) ExternalToInternal(externalMsg []byte) ([]byte, error) {
	var ext externalMessage
	// 第一步：JSON 反序列化
	if err := json.Unmarshal(externalMsg, &ext); err != nil {
		return nil, fmt.Errorf("unmarshal external message: %w", err)
	}
	// 校验必填字段：发送者和接收者不能为空
	if ext.From == 0 || ext.To == 0 {
		return nil, fmt.Errorf("external message missing from/to fields")
	}
	// 第二步：映射到内部 MsgBody 结构
	msgBody := &protocol.MsgBody{
		FromUID:   ext.From,         // 发送者ID
		ToUID:     ext.To,           // 接收者ID
		Timestamp: ext.Ts,           // 时间戳
		Content:   []byte(ext.Text), // 文本转字节切片
	}
	// 第三步：protobuf 序列化
	return protocol.MarshalMsgBody(msgBody)
}

// InternalToExternal 将内部 protobuf 编码的 MsgBody 转换为外部 JSON 消息。
//
// 转换流程：
//  1. protobuf 字节 -> MsgBody 结构体（反序列化）
//  2. MsgBody -> externalMessage（字段映射）
//  3. externalMessage -> JSON 字节（序列化）
//
// 输入：protobuf 编码的 MsgBody 字节数组
// 输出示例：{"from":1001,"to":1002,"text":"hello","ts":1699000000}
func (c *ProtocolConverter) InternalToExternal(internal []byte) ([]byte, error) {
	// 第一步：protobuf 反序列化
	msgBody, err := protocol.UnmarshalMsgBody(internal)
	if err != nil {
		return nil, fmt.Errorf("unmarshal internal message: %w", err)
	}
	// 第二步：映射到外部 JSON 结构
	ext := externalMessage{
		From: msgBody.FromUID,
		To:   msgBody.ToUID,
		Text: string(msgBody.Content), // 字节切片转字符串
		Ts:   msgBody.Timestamp,
	}
	// 第三步：JSON 序列化
	return json.Marshal(ext)
}
