package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

// ListDeliveryMessages returns a merged delivery-status feed for the dashboard.
func (s *SQLStore) ListDeliveryMessages(limit int) ([]*model.DeliveryMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var rows []*model.DeliveryMessage

	notifications, err := s.listRecentNotifications(limit)
	if err != nil {
		return nil, err
	}
	for _, n := range notifications {
		outbox, _ := s.getOutboxByNotifyID(n.NotifyID)
		attempts, _ := s.ListAttemptsByNotifyID(n.NotifyID)
		dlq, _ := s.getDLQByNotifyID(n.NotifyID)
		rows = append(rows, deliveryMessageFromNotification(n, outbox, attempts, dlq))
	}

	chatRows, err := s.listRecentChatDeliveryMessages(limit)
	if err != nil {
		return nil, err
	}
	rows = append(rows, chatRows...)

	acks, err := s.listRecentACKs(limit)
	if err != nil {
		return nil, err
	}
	for _, ack := range acks {
		rows = append(rows, deliveryMessageFromACK(ack, nil))
	}

	dlqs, err := s.ListDLQFiltered(model.DLQBulkFilter{Limit: limit})
	if err != nil {
		return nil, err
	}
	for _, dlq := range dlqs {
		rows = append(rows, deliveryMessageFromDLQ(dlq))
	}

	retries, err := s.listRetryOutboxes(limit)
	if err != nil {
		return nil, err
	}
	for _, outbox := range retries {
		rows = append(rows, deliveryMessageFromOutbox(outbox))
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// GetDeliveryMessageDetail returns operator details for one flattened row id.
func (s *SQLStore) GetDeliveryMessageDetail(id string) (*model.DeliveryMessageDetail, error) {
	kind, key, ok := strings.Cut(id, ":")
	if !ok || key == "" {
		return nil, ErrNotFound
	}
	switch kind {
	case "notification":
		return s.notificationDeliveryDetail(key)
	case "chat":
		return s.chatDeliveryDetail(key)
	case "ack":
		return s.ackDeliveryDetail(key)
	case "dlq":
		return s.dlqDeliveryDetail(key)
	case "retry":
		return s.retryDeliveryDetail(key)
	default:
		return nil, ErrNotFound
	}
}

func (s *SQLStore) listRecentNotifications(limit int) ([]*model.Notification, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT notify_id, user_id, type, business_type, event_type, title, content, order_id, status,
		priority, ttl_seconds, ack_policy, expected_ack_count, acked_count, business_ack_status,
		COALESCE(target_devices_json, ''), COALESCE(primary_device_id, ''), COALESCE(scenario_run_id, ''), idempotency_key, COALESCE(trace_id, notify_id), created_at, updated_at
		FROM notifications ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notifications []*model.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, n)
	}
	return notifications, rows.Err()
}

func (s *SQLStore) listRecentChatDeliveryMessages(limit int) ([]*model.DeliveryMessage, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT m.message_id, m.conversation_id, m.order_id, m.sender_uid, m.receiver_uid,
		m.sender_role, m.body, m.status, COALESCE(m.delivery_path, ''), m.created_at, m.delivered_at, m.read_at,
		COALESCE(c.conversation_type, 'private'), c.room_id
		FROM chat_messages m JOIN chat_conversations c ON c.conversation_id = m.conversation_id
		ORDER BY m.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []*model.DeliveryMessage
	for rows.Next() {
		msg, convType, roomID, err := scanChatMessageWithConversation(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, deliveryMessageFromChat(msg, convType, roomID))
	}
	return messages, rows.Err()
}

func (s *SQLStore) listRecentACKs(limit int) ([]*model.NotificationAck, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT ack_id, notify_id, user_id, COALESCE(msg_id, ''),
		COALESCE(device_id, ''), COALESCE(session_id, ''), ack_key, latency_ms, COALESCE(policy_satisfied_at, ''), COALESCE(trace_id, notify_id), created_at
		FROM notification_acks ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var acks []*model.NotificationAck
	for rows.Next() {
		ack, err := scanAck(rows)
		if err != nil {
			return nil, err
		}
		acks = append(acks, ack)
	}
	return acks, rows.Err()
}

func (s *SQLStore) listRetryOutboxes(limit int) ([]*model.NotificationOutbox, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT outbox_id, notify_id, user_id, COALESCE(order_id, ''),
		business_type, event_type, payload_json, priority, ttl_seconds, status, retry_count,
		COALESCE(next_retry_at, ''), COALESCE(locked_by, ''), COALESCE(locked_until, ''),
		COALESCE(last_error, ''), COALESCE(scenario_run_id, ''), COALESCE(trace_id, notify_id), COALESCE(compensation_strategy, 'retry_then_dlq'), created_at, updated_at
		FROM notification_outbox
		WHERE retry_count > 0 OR status = 'failed' OR COALESCE(last_error, '') <> ''
		ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var outboxes []*model.NotificationOutbox
	for rows.Next() {
		outbox, err := scanOutbox(rows)
		if err != nil {
			return nil, err
		}
		outboxes = append(outboxes, outbox)
	}
	return outboxes, rows.Err()
}

func (s *SQLStore) notificationDeliveryDetail(notifyID string) (*model.DeliveryMessageDetail, error) {
	trace, err := s.GetNotificationTrace(notifyID)
	if err != nil {
		return nil, err
	}
	dlq, _ := s.getDLQByNotifyID(notifyID)
	message := deliveryMessageFromNotification(trace.Notification, trace.Outbox, trace.Attempts, dlq)
	order, _ := s.GetOrder(trace.Notification.OrderID)
	timeline, _ := s.GetOrderTimeline(trace.Notification.OrderID)
	targetNode, latency := latestAttemptTarget(trace.Attempts)
	lastError := lastDeliveryError(trace.Outbox, trace.Attempts, dlq)
	return &model.DeliveryMessageDetail{
		Message:           message,
		RawPayload:        decodePayload(traceOutboxPayload(trace)),
		BusinessType:      trace.Notification.BusinessType,
		EventType:         trace.Notification.EventType,
		TraceID:           trace.TraceID,
		DeliveryPath:      message.DeliveryMethod,
		Attempts:          trace.Attempts,
		ACKs:              trace.ACKs,
		RetryCount:        trace.RetryCount,
		DLQ:               dlq,
		FailureReason:     failureReason(lastError),
		LastError:         lastError,
		TargetNode:        targetNode,
		LatencyMs:         latency,
		Order:             order,
		NotificationTrace: trace,
		OrderTimeline:     timeline,
	}, nil
}

func (s *SQLStore) chatDeliveryDetail(messageID string) (*model.DeliveryMessageDetail, error) {
	msg, conv, err := s.getChatMessageWithConversation(messageID)
	if err != nil {
		return nil, err
	}
	row := deliveryMessageFromChat(msg, conv.Type, conv.RoomID)
	var order *model.Order
	if msg.OrderID != "" && !strings.HasPrefix(msg.OrderID, "group:") {
		order, _ = s.GetOrder(msg.OrderID)
	}
	lastError := ""
	if msg.Status == model.ChatStatusFailed {
		lastError = msg.DeliveryPath
	}
	return &model.DeliveryMessageDetail{
		Message:       row,
		RawPayload:    chatPayload(msg, conv),
		BusinessType:  "chat",
		EventType:     chatEventType(conv.Type),
		TraceID:       row.TraceID,
		DeliveryPath:  row.DeliveryMethod,
		RetryCount:    0,
		FailureReason: failureReason(lastError),
		LastError:     lastError,
		Order:         order,
		ChatMessage:   msg,
		Conversation:  conv,
	}, nil
}

func (s *SQLStore) ackDeliveryDetail(ackID string) (*model.DeliveryMessageDetail, error) {
	ack, err := s.getACKByID(ackID)
	if err != nil {
		return nil, err
	}
	trace, _ := s.GetNotificationTrace(ack.NotifyID)
	row := deliveryMessageFromACK(ack, trace)
	var order *model.Order
	var timeline *model.OrderTimeline
	if trace != nil && trace.Notification != nil {
		order, _ = s.GetOrder(trace.Notification.OrderID)
		timeline, _ = s.GetOrderTimeline(trace.Notification.OrderID)
	}
	return &model.DeliveryMessageDetail{
		Message:           row,
		RawPayload:        ack,
		BusinessType:      row.BusinessType,
		EventType:         "client_ack",
		TraceID:           row.TraceID,
		DeliveryPath:      "ACK",
		ACKs:              []*model.NotificationAck{ack},
		FailureReason:     "暂无失败原因",
		Order:             order,
		NotificationTrace: trace,
		OrderTimeline:     timeline,
	}, nil
}

func (s *SQLStore) dlqDeliveryDetail(dlqID string) (*model.DeliveryMessageDetail, error) {
	dlq, err := s.GetDLQ(dlqID)
	if err != nil {
		return nil, err
	}
	trace, _ := s.GetNotificationTrace(dlq.NotifyID)
	row := deliveryMessageFromDLQ(dlq)
	var order *model.Order
	if dlq.OrderID != "" {
		order, _ = s.GetOrder(dlq.OrderID)
	}
	return &model.DeliveryMessageDetail{
		Message:           row,
		RawPayload:        decodePayload(dlq.PayloadJSON),
		BusinessType:      dlq.BusinessType,
		EventType:         row.EventType,
		TraceID:           row.TraceID,
		DeliveryPath:      row.DeliveryMethod,
		Attempts:          traceAttempts(trace),
		ACKs:              traceACKs(trace),
		RetryCount:        dlq.RetryCount,
		DLQ:               dlq,
		FailureReason:     failureReason(dlq.LastError),
		LastError:         dlq.LastError,
		Order:             order,
		NotificationTrace: trace,
	}, nil
}

func (s *SQLStore) retryDeliveryDetail(outboxID string) (*model.DeliveryMessageDetail, error) {
	outbox, err := s.GetOutbox(outboxID)
	if err != nil {
		return nil, err
	}
	trace, _ := s.GetNotificationTrace(outbox.NotifyID)
	row := deliveryMessageFromOutbox(outbox)
	var order *model.Order
	if outbox.OrderID != "" {
		order, _ = s.GetOrder(outbox.OrderID)
	}
	return &model.DeliveryMessageDetail{
		Message:           row,
		RawPayload:        decodePayload(outbox.PayloadJSON),
		BusinessType:      outbox.BusinessType,
		EventType:         outbox.EventType,
		TraceID:           row.TraceID,
		DeliveryPath:      row.DeliveryMethod,
		Attempts:          traceAttempts(trace),
		ACKs:              traceACKs(trace),
		RetryCount:        outbox.RetryCount,
		FailureReason:     failureReason(outbox.LastError),
		LastError:         outbox.LastError,
		Order:             order,
		NotificationTrace: trace,
	}, nil
}

func (s *SQLStore) getChatMessageWithConversation(messageID string) (*model.ChatMessage, *model.ChatConversation, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT m.message_id, m.conversation_id, m.order_id, m.sender_uid, m.receiver_uid,
		m.sender_role, m.body, m.status, COALESCE(m.delivery_path, ''), m.created_at, m.delivered_at, m.read_at
		FROM chat_messages m WHERE m.message_id = ?`, messageID)
	msg, err := scanChatMessage(row)
	if err != nil {
		return nil, nil, err
	}
	conv, err := s.GetChatConversation(msg.ConversationID)
	if err != nil {
		return nil, nil, err
	}
	return msg, conv, nil
}

func (s *SQLStore) getACKByID(ackID string) (*model.NotificationAck, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT ack_id, notify_id, user_id, COALESCE(msg_id, ''),
		COALESCE(device_id, ''), COALESCE(session_id, ''), ack_key, latency_ms, COALESCE(policy_satisfied_at, ''), COALESCE(trace_id, notify_id), created_at
		FROM notification_acks WHERE ack_id = ?`, ackID)
	return scanAck(row)
}

func (s *SQLStore) getDLQByNotifyID(notifyID string) (*model.NotificationDLQ, error) {
	row := s.db.QueryRowContext(context.Background(), `SELECT dlq_id, notify_id, outbox_id, user_id, COALESCE(order_id, ''), reason, last_error, payload_json,
		retry_count, COALESCE(business_type, ''), COALESCE(compensation_strategy, 'retry_then_dlq'), COALESCE(trace_id, notify_id),
		created_at, COALESCE(resolved_at, ''), COALESCE(resolved_by, ''), COALESCE(resolution, '')
		FROM notification_dlq WHERE notify_id = ? LIMIT 1`, notifyID)
	dlq, err := scanDLQ(row)
	if errorsIsNoRows(err) {
		return nil, nil
	}
	return dlq, err
}

func scanChatMessageWithConversation(row scanner) (*model.ChatMessage, string, string, error) {
	var msg model.ChatMessage
	var createdAt, convType, roomID string
	var deliveredAt, readAt sql.NullString
	if err := row.Scan(&msg.MessageID, &msg.ConversationID, &msg.OrderID, &msg.SenderUID, &msg.ReceiverUID,
		&msg.SenderRole, &msg.Body, &msg.Status, &msg.DeliveryPath, &createdAt, &deliveredAt, &readAt,
		&convType, &roomID); err != nil {
		if errorsIsNoRows(err) {
			return nil, "", "", ErrNotFound
		}
		return nil, "", "", err
	}
	var err error
	msg.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return nil, "", "", err
	}
	if deliveredAt.Valid && deliveredAt.String != "" {
		t, err := parseTime(deliveredAt.String)
		if err != nil {
			return nil, "", "", err
		}
		msg.DeliveredAt = &t
	}
	if readAt.Valid && readAt.String != "" {
		t, err := parseTime(readAt.String)
		if err != nil {
			return nil, "", "", err
		}
		msg.ReadAt = &t
	}
	return &msg, convType, roomID, nil
}

func deliveryMessageFromNotification(n *model.Notification, outbox *model.NotificationOutbox, attempts []*model.NotificationAttempt, dlq *model.NotificationDLQ) *model.DeliveryMessage {
	targetNode, latency := latestAttemptTarget(attempts)
	createdAt := n.CreatedAt
	updatedAt := n.UpdatedAt
	status := n.Status
	if dlq != nil {
		status = "dlq"
		updatedAt = dlq.CreatedAt
	} else if outbox != nil && status == "pending" {
		status = normalizeOutboxStatus(outbox.Status)
		updatedAt = outbox.UpdatedAt
	}
	return &model.DeliveryMessage{
		ID:             "notification:" + n.NotifyID,
		MessageID:      n.NotifyID,
		RecordKind:     "notification",
		Type:           notificationDeliveryType(n),
		OrderID:        n.OrderID,
		Sender:         "notify-server",
		Target:         displayTarget(n.UserID),
		DeliveryMethod: normalizeDeliveryPath(firstDeliveryPath(attempts, outbox)),
		Status:         status,
		Priority:       n.Priority,
		TTLSeconds:     n.TTLSeconds,
		AckPolicy:      n.AckPolicy,
		BusinessType:   n.BusinessType,
		EventType:      n.EventType,
		TraceID:        n.TraceID,
		RetryCount:     retryCount(outbox, dlq),
		LastError:      lastDeliveryError(outbox, attempts, dlq),
		TargetNode:     targetNode,
		LatencyMs:      latency,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
}

func deliveryMessageFromChat(msg *model.ChatMessage, convType, roomID string) *model.DeliveryMessage {
	updatedAt := msg.CreatedAt
	if msg.DeliveredAt != nil {
		updatedAt = *msg.DeliveredAt
	}
	if msg.ReadAt != nil {
		updatedAt = *msg.ReadAt
	}
	deliveryMethod := "direct push"
	target := fmt.Sprintf("UID %d", msg.ReceiverUID)
	if convType == model.ChatTypeGroup {
		deliveryMethod = "room push"
		target = roomID
	}
	return &model.DeliveryMessage{
		ID:             "chat:" + msg.MessageID,
		MessageID:      msg.MessageID,
		RecordKind:     "chat",
		Type:           mapBool(convType == model.ChatTypeGroup, "群聊", "私聊"),
		OrderID:        msg.OrderID,
		Sender:         fmt.Sprintf("UID %d", msg.SenderUID),
		Target:         target,
		DeliveryMethod: deliveryMethod,
		Status:         msg.Status,
		Priority:       "normal",
		AckPolicy:      mapBool(convType == model.ChatTypeGroup, "room fanout", "peer read"),
		BusinessType:   "chat",
		EventType:      chatEventType(convType),
		TraceID:        msg.MessageID,
		ConversationID: msg.ConversationID,
		RoomID:         roomID,
		SenderUID:      msg.SenderUID,
		ReceiverUID:    msg.ReceiverUID,
		SenderRole:     msg.SenderRole,
		LastError:      mapBool(msg.Status == model.ChatStatusFailed, msg.DeliveryPath, ""),
		CreatedAt:      msg.CreatedAt,
		UpdatedAt:      updatedAt,
	}
}

func deliveryMessageFromACK(ack *model.NotificationAck, trace *model.NotificationTrace) *model.DeliveryMessage {
	orderID, businessType, priority, ackPolicy := "", "", "", ""
	if trace != nil && trace.Notification != nil {
		orderID = trace.Notification.OrderID
		businessType = trace.Notification.BusinessType
		priority = trace.Notification.Priority
		ackPolicy = trace.Notification.AckPolicy
	}
	messageID := ack.MsgID
	if messageID == "" {
		messageID = ack.AckID
	}
	return &model.DeliveryMessage{
		ID:             "ack:" + ack.AckID,
		MessageID:      messageID,
		RecordKind:     "ack",
		Type:           "ACK",
		OrderID:        orderID,
		Sender:         displayTarget(ack.UserID),
		Target:         "notify-server",
		DeliveryMethod: "ACK",
		Status:         "acked",
		Priority:       priority,
		AckPolicy:      ackPolicy,
		BusinessType:   businessType,
		EventType:      "client_ack",
		TraceID:        ack.TraceID,
		LatencyMs:      ack.LatencyMs,
		CreatedAt:      ack.CreatedAt,
		UpdatedAt:      ack.CreatedAt,
	}
}

func deliveryMessageFromDLQ(dlq *model.NotificationDLQ) *model.DeliveryMessage {
	return &model.DeliveryMessage{
		ID:             "dlq:" + dlq.DLQID,
		MessageID:      dlq.NotifyID,
		RecordKind:     "dlq",
		Type:           "DLQ",
		OrderID:        dlq.OrderID,
		Sender:         "notify outbox",
		Target:         displayTarget(dlq.UserID),
		DeliveryMethod: "kafka fallback",
		Status:         "dlq",
		BusinessType:   dlq.BusinessType,
		EventType:      "dlq",
		TraceID:        dlq.TraceID,
		RetryCount:     dlq.RetryCount,
		LastError:      dlq.LastError,
		CreatedAt:      dlq.CreatedAt,
		UpdatedAt:      nonNilTime(dlq.ResolvedAt, dlq.CreatedAt),
	}
}

func deliveryMessageFromOutbox(outbox *model.NotificationOutbox) *model.DeliveryMessage {
	return &model.DeliveryMessage{
		ID:             "retry:" + outbox.OutboxID,
		MessageID:      outbox.NotifyID,
		RecordKind:     "retry",
		Type:           "重试",
		OrderID:        outbox.OrderID,
		Sender:         "notify outbox",
		Target:         displayTarget(outbox.UserID),
		DeliveryMethod: "kafka fallback",
		Status:         normalizeOutboxStatus(outbox.Status),
		Priority:       outbox.Priority,
		TTLSeconds:     outbox.TTLSeconds,
		AckPolicy:      "outbox_retry",
		BusinessType:   outbox.BusinessType,
		EventType:      outbox.EventType,
		TraceID:        outbox.TraceID,
		RetryCount:     outbox.RetryCount,
		LastError:      outbox.LastError,
		CreatedAt:      outbox.CreatedAt,
		UpdatedAt:      outbox.UpdatedAt,
	}
}

func notificationDeliveryType(n *model.Notification) string {
	if n.Type == model.NotifyPurchaseOrder && n.EventType == "created_merchant" {
		return "商家新订单通知"
	}
	if n.Type == model.NotifyPurchaseOrder {
		return "购买订单通知"
	}
	switch n.Type {
	case model.NotifyOrderStatus, model.NotifyLogistics:
		return "订单通知"
	case model.NotifySystem:
		return "系统"
	default:
		return string(n.Type)
	}
}

func firstDeliveryPath(attempts []*model.NotificationAttempt, outbox *model.NotificationOutbox) string {
	for _, attempt := range attempts {
		if attempt.Path != "" && attempt.Path != "unknown" {
			return attempt.Path
		}
		if attempt.Channel != "" {
			return attempt.Channel
		}
	}
	if outbox != nil && outbox.Status != "" {
		return outbox.Status
	}
	return "pending"
}

func normalizeDeliveryPath(path string) string {
	switch path {
	case "grpc_direct", "direct_sent", "logic_push", "success":
		return "direct push"
	case "room_push":
		return "room push"
	case "offline_stored":
		return "offline stored"
	case "published", "processing", "delivering", "failed", "pending":
		return "kafka fallback"
	default:
		if path == "" || path == "unknown" {
			return "pending"
		}
		return path
	}
}

func normalizeOutboxStatus(status string) string {
	switch status {
	case "published", "processing", "delivering":
		return "sent"
	case "dead", "dlq":
		return "dlq"
	case "":
		return "pending"
	default:
		return status
	}
}

func latestAttemptTarget(attempts []*model.NotificationAttempt) (string, float64) {
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i] == nil {
			continue
		}
		return attempts[i].TargetNode, attempts[i].LatencyMs
	}
	return "", 0
}

func lastDeliveryError(outbox *model.NotificationOutbox, attempts []*model.NotificationAttempt, dlq *model.NotificationDLQ) string {
	if dlq != nil && dlq.LastError != "" {
		return dlq.LastError
	}
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i] != nil && attempts[i].ErrorMessage != "" {
			return attempts[i].ErrorMessage
		}
	}
	if outbox != nil {
		return outbox.LastError
	}
	return ""
}

func retryCount(outbox *model.NotificationOutbox, dlq *model.NotificationDLQ) int64 {
	if dlq != nil {
		return dlq.RetryCount
	}
	if outbox != nil {
		return outbox.RetryCount
	}
	return 0
}

func decodePayload(raw string) interface{} {
	if raw == "" {
		return map[string]interface{}{}
	}
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}

func displayTarget(userID string) string {
	if strings.HasPrefix(userID, "room:") {
		return strings.TrimPrefix(userID, "room:")
	}
	if userID == "" {
		return "-"
	}
	return "UID " + userID
}

func chatPayload(msg *model.ChatMessage, conv *model.ChatConversation) map[string]interface{} {
	return map[string]interface{}{
		"type":            chatEventType(conv.Type),
		"message_id":      msg.MessageID,
		"conversation_id": msg.ConversationID,
		"order_id":        msg.OrderID,
		"room_id":         conv.RoomID,
		"sender_uid":      msg.SenderUID,
		"receiver_uid":    msg.ReceiverUID,
		"sender_role":     msg.SenderRole,
		"body":            msg.Body,
		"status":          msg.Status,
		"delivery_path":   msg.DeliveryPath,
		"created_at":      msg.CreatedAt,
		"delivered_at":    msg.DeliveredAt,
		"read_at":         msg.ReadAt,
	}
}

func chatEventType(convType string) string {
	if convType == model.ChatTypeGroup {
		return "group_chat_message"
	}
	return "chat_message"
}

func failureReason(lastError string) string {
	if lastError == "" {
		return "暂无失败原因"
	}
	return lastError
}

func traceAttempts(trace *model.NotificationTrace) []*model.NotificationAttempt {
	if trace == nil {
		return nil
	}
	return trace.Attempts
}

func traceACKs(trace *model.NotificationTrace) []*model.NotificationAck {
	if trace == nil {
		return nil
	}
	return trace.ACKs
}

func nonNilTime(t *time.Time, fallback time.Time) time.Time {
	if t == nil {
		return fallback
	}
	return *t
}

func mapBool(ok bool, a, b string) string {
	if ok {
		return a
	}
	return b
}

func traceOutboxPayload(trace *model.NotificationTrace) string {
	if trace == nil || trace.Outbox == nil {
		return ""
	}
	return trace.Outbox.PayloadJSON
}

func errorsIsNoRows(err error) bool {
	return err == sql.ErrNoRows || err == ErrNotFound
}
