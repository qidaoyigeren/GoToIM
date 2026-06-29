package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/store"
)

// PurchaseOrderItemInput is the compact request item for one-click ordering.
type PurchaseOrderItemInput struct {
	ProductID string  `json:"product_id"`
	SKUID     string  `json:"sku_id,omitempty"`
	Quantity  int     `json:"quantity"`
	Price     float64 `json:"price,omitempty"`
}

// PurchaseOrderInput captures the demo purchase order request.
type PurchaseOrderInput struct {
	UserID          string                   `json:"user_id"`
	MerchantID      string                   `json:"merchant_id"`
	MerchantUID     int64                    `json:"merchant_uid,omitempty"`
	OrderType       model.OrderType          `json:"order_type,omitempty"`
	Importance      model.OrderImportance    `json:"importance,omitempty"`
	BuyerNote       string                   `json:"buyer_note,omitempty"`
	FulfillmentMode string                   `json:"fulfillment_mode,omitempty"`
	Items           []PurchaseOrderItemInput `json:"items"`
}

// PurchaseOrderResult is returned by the purchase-order API.
type PurchaseOrderResult struct {
	Order                *model.Order            `json:"order"`
	Notifications        []*model.Notification   `json:"notifications"`
	BuyerNotification    *model.Notification     `json:"buyer_notification,omitempty"`
	MerchantNotification *model.Notification     `json:"merchant_notification,omitempty"`
	PrivateConversation  *model.ChatConversation `json:"private_conversation,omitempty"`
	SupportRoom          *model.MerchantGroup    `json:"support_room,omitempty"`
	DeliveryPolicy       purchaseDeliveryProfile `json:"delivery_policy"`
}

type purchaseDeliveryProfile struct {
	Priority         string `json:"priority"`
	TTLSeconds       int64  `json:"ttl_seconds"`
	AckPolicy        string `json:"ack_policy"`
	ExpectedAckCount int64  `json:"expected_ack_count"`
}

// ListMerchants returns demo merchants.
func (s *OrderNotifyService) ListMerchants() ([]*model.Merchant, error) {
	return s.store.ListMerchants()
}

// ListProducts returns demo products for a merchant.
func (s *OrderNotifyService) ListProducts(merchantID string) ([]*model.Product, error) {
	return s.store.ListProducts(merchantID)
}

// ListMerchantGroups returns merchant-created demo groups.
func (s *OrderNotifyService) ListMerchantGroups(merchantID string) ([]*model.MerchantGroup, error) {
	return s.store.ListMerchantGroups(merchantID)
}

// CreatePurchaseOrderIdempotent creates a purchase order and its IM delivery messages.
func (s *OrderNotifyService) CreatePurchaseOrderIdempotent(input PurchaseOrderInput, idempotencyKey string) (*PurchaseOrderResult, error) {
	var replay PurchaseOrderResult
	if ok, err := s.loadIdempotency("purchase_order_create", idempotencyKey, &replay); err != nil {
		return nil, err
	} else if ok {
		return &replay, nil
	}

	if strings.TrimSpace(input.UserID) == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if strings.TrimSpace(input.MerchantID) == "" {
		return nil, fmt.Errorf("merchant_id is required")
	}
	if len(input.Items) == 0 {
		return nil, fmt.Errorf("items must not be empty")
	}

	merchant, err := s.store.GetMerchant(input.MerchantID)
	if err != nil {
		if err == store.ErrNotFound {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	if input.MerchantUID > 0 {
		merchant.MerchantUID = input.MerchantUID
	}
	groups, _ := s.store.ListMerchantGroups(merchant.MerchantID)
	var supportRoom *model.MerchantGroup
	if len(groups) > 0 {
		supportRoom = groups[0]
	}

	orderItems := make([]model.OrderItem, 0, len(input.Items))
	total := 0.0
	fulfillmentMode := input.FulfillmentMode
	for _, item := range input.Items {
		if item.Quantity <= 0 {
			item.Quantity = 1
		}
		product, err := s.store.GetProduct(item.ProductID)
		if err != nil {
			return nil, err
		}
		if product.MerchantID != merchant.MerchantID {
			return nil, fmt.Errorf("product %s does not belong to merchant %s", item.ProductID, merchant.MerchantID)
		}
		price := product.Price
		if item.Price > 0 {
			price = item.Price
		}
		if fulfillmentMode == "" {
			fulfillmentMode = product.FulfillmentMode
		} else if product.FulfillmentMode != "" && fulfillmentMode != product.FulfillmentMode {
			fulfillmentMode = "mixed"
		}
		orderItems = append(orderItems, model.OrderItem{
			ProductID:   product.ProductID,
			SKUID:       nonEmptyString(item.SKUID, product.SKUID),
			ProductName: product.Name,
			Quantity:    item.Quantity,
			Price:       price,
			ImageURL:    product.ImageURL,
		})
		total += price * float64(item.Quantity)
	}
	if fulfillmentMode == "" {
		fulfillmentMode = "physical"
	}

	now := time.Now()
	importance := normalizePurchaseImportance(input.Importance)
	orderType := normalizePurchaseOrderType(input.OrderType, fulfillmentMode)
	profile := purchaseProfileForImportance(importance)
	privateConversationID := generateChatConversationID()
	supportRoomID := ""
	if supportRoom != nil {
		supportRoomID = supportRoom.RoomID
	}
	order := &model.Order{
		OrderID:               generateOrderID(),
		UserID:                input.UserID,
		MerchantID:            merchant.MerchantID,
		MerchantUID:           merchant.MerchantUID,
		MerchantName:          merchant.Name,
		Status:                model.OrderCreated,
		OrderType:             orderType,
		Importance:            importance,
		BuyerNote:             input.BuyerNote,
		FulfillmentMode:       fulfillmentMode,
		SupportRoomID:         supportRoomID,
		PrivateConversationID: privateConversationID,
		Items:                 orderItems,
		Total:                 total,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	customerUID, _ := strconv.ParseInt(order.UserID, 10, 64)
	privateConv := &model.ChatConversation{
		ConversationID: privateConversationID,
		Type:           model.ChatTypePrivate,
		OrderID:        order.OrderID,
		MerchantID:     merchant.MerchantID,
		Title:          "订单私聊 " + order.OrderID,
		CustomerUID:    customerUID,
		MerchantUID:    merchant.MerchantUID,
		RoomID:         "order_chat:" + order.OrderID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	event := &model.OrderStatusEvent{
		EventID:        generateEventID(),
		OrderID:        order.OrderID,
		FromStatus:     "",
		ToStatus:       model.OrderCreated,
		Extra:          purchaseEventExtra(order, profile),
		IdempotencyKey: idempotencyKey,
		CreatedAt:      now,
	}

	buyerNotif := s.buildPurchaseNotification(order.UserID, order, "created_buyer", "下单成功", fmt.Sprintf("订单 %s 已提交，%s 已收到订单", order.OrderID, merchant.Name), profile, idempotencyKey, now)
	merchantNotif := s.buildPurchaseNotification(strconv.FormatInt(merchant.MerchantUID, 10), order, "created_merchant", "新订单待处理", fmt.Sprintf("用户 %s 提交了 %s 订单 %s，请及时处理", order.UserID, orderTypeLabel(order.OrderType), order.OrderID), profile, idempotencyKey, now)
	notifications := []*model.Notification{buyerNotif, merchantNotif}

	outboxes := make([]*model.NotificationOutbox, 0, len(notifications))
	for _, n := range notifications {
		outbox, err := s.createOutboxWithExtras(n, map[string]interface{}{
			"merchant_id":     order.MerchantID,
			"merchant_uid":    order.MerchantUID,
			"merchant_name":   order.MerchantName,
			"order_type":      order.OrderType,
			"importance":      order.Importance,
			"support_room_id": order.SupportRoomID,
		})
		if err != nil {
			return nil, err
		}
		outboxes = append(outboxes, outbox)
	}

	result := &PurchaseOrderResult{
		Order:                order,
		Notifications:        notifications,
		BuyerNotification:    buyerNotif,
		MerchantNotification: merchantNotif,
		PrivateConversation:  privateConv,
		SupportRoom:          supportRoom,
		DeliveryPolicy:       profile,
	}
	snapshot, err := marshalReplay(result)
	if err != nil {
		return nil, err
	}
	if err := s.store.CreatePurchaseOrderNotificationOutbox(order, event, privateConv, notifications, outboxes, "purchase_order_create", idempotencyKey, "order", order.OrderID, snapshot); err != nil {
		if store.IsDuplicateKey(err) {
			if ok, replayErr := s.loadIdempotencyAfterWriteConflict("purchase_order_create", idempotencyKey, &replay); replayErr != nil {
				return nil, replayErr
			} else if ok {
				return &replay, nil
			}
		}
		return nil, err
	}
	s.incrementScenarioRun(buyerNotif.ScenarioRunID, 1, int64(len(notifications)), 0, 0, 0, 0)
	return result, nil
}

func (s *OrderNotifyService) buildPurchaseNotification(userID string, order *model.Order, eventType, title, content string, profile purchaseDeliveryProfile, idempotencyKey string, now time.Time) *model.Notification {
	notif := &model.Notification{
		NotifyID:          generateNotifyID(),
		UserID:            userID,
		Type:              model.NotifyPurchaseOrder,
		BusinessType:      "purchase_order",
		EventType:         eventType,
		Title:             title,
		Content:           content,
		OrderID:           order.OrderID,
		Status:            "pending",
		Priority:          profile.Priority,
		TTLSeconds:        profile.TTLSeconds,
		AckPolicy:         profile.AckPolicy,
		ExpectedAckCount:  profile.ExpectedAckCount,
		BusinessAckStatus: "pending",
		IdempotencyKey:    idempotencyKey,
		ScenarioRunID:     s.currentScenarioRunID(),
		TraceID:           generateTraceID(),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	s.applyDeviceTargets(notif)
	return notif
}

func normalizePurchaseOrderType(v model.OrderType, fulfillmentMode string) model.OrderType {
	switch strings.ToLower(strings.TrimSpace(string(v))) {
	case "presale", "pre_sale", "preorder":
		return model.OrderTypePresale
	case "urgent", "express":
		return model.OrderTypeUrgent
	case "enterprise", "b2b", "corporate":
		return model.OrderTypeEnterprise
	case "after_sale", "aftersale", "service":
		return model.OrderTypeAfterSale
	case "virtual", "digital":
		return model.OrderTypeVirtual
	case "normal", "":
		if fulfillmentMode == "virtual" {
			return model.OrderTypeVirtual
		}
		return model.OrderTypeNormal
	default:
		return model.OrderTypeNormal
	}
}

func normalizePurchaseImportance(v model.OrderImportance) model.OrderImportance {
	switch strings.ToLower(strings.TrimSpace(string(v))) {
	case "high", "important":
		return model.OrderImportanceHigh
	case "urgent":
		return model.OrderImportanceUrgent
	case "critical":
		return model.OrderImportanceCritical
	default:
		return model.OrderImportanceNormal
	}
}

func purchaseProfileForImportance(importance model.OrderImportance) purchaseDeliveryProfile {
	switch importance {
	case model.OrderImportanceHigh:
		return purchaseDeliveryProfile{Priority: "high", TTLSeconds: 1800, AckPolicy: "any_device", ExpectedAckCount: 1}
	case model.OrderImportanceUrgent:
		return purchaseDeliveryProfile{Priority: "critical", TTLSeconds: 600, AckPolicy: "any_device", ExpectedAckCount: 1}
	case model.OrderImportanceCritical:
		return purchaseDeliveryProfile{Priority: "critical", TTLSeconds: 300, AckPolicy: "primary_device", ExpectedAckCount: 1}
	default:
		return purchaseDeliveryProfile{Priority: "normal", TTLSeconds: 3600, AckPolicy: "best_effort", ExpectedAckCount: 0}
	}
}

func purchaseEventExtra(order *model.Order, profile purchaseDeliveryProfile) map[string]string {
	return map[string]string{
		"merchant_id":      order.MerchantID,
		"merchant_name":    order.MerchantName,
		"order_type":       string(order.OrderType),
		"importance":       string(order.Importance),
		"priority":         profile.Priority,
		"ttl_seconds":      strconv.FormatInt(profile.TTLSeconds, 10),
		"ack_policy":       profile.AckPolicy,
		"support_room_id":  order.SupportRoomID,
		"private_chat_id":  order.PrivateConversationID,
		"fulfillment_mode": order.FulfillmentMode,
	}
}

func orderTypeLabel(t model.OrderType) string {
	switch t {
	case model.OrderTypePresale:
		return "预售"
	case model.OrderTypeUrgent:
		return "加急"
	case model.OrderTypeEnterprise:
		return "企业采购"
	case model.OrderTypeAfterSale:
		return "售后服务"
	case model.OrderTypeVirtual:
		return "虚拟商品"
	default:
		return "普通商品"
	}
}
