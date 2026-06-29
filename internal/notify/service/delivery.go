package service

import "github.com/Terry-Mao/goim/internal/notify/model"

// ListDeliveryMessages returns a merged delivery-status feed.
func (s *OrderNotifyService) ListDeliveryMessages(limit int) ([]*model.DeliveryMessage, error) {
	return s.store.ListDeliveryMessages(limit)
}

// GetDeliveryMessageDetail returns one expanded delivery record.
func (s *OrderNotifyService) GetDeliveryMessageDetail(id string) (*model.DeliveryMessageDetail, error) {
	return s.store.GetDeliveryMessageDetail(id)
}
