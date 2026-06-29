package simulator

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

// runNormalTraffic generates a steady stream of mixed order status changes.
func (e *Engine) runNormalTraffic(qps, userPool int) {
	e.wg.Add(1)
	defer e.wg.Done()

	interval := time.Second / time.Duration(qps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.generateOneEvent(userPool)
		}
	}
}

// runPeakTraffic generates high-volume traffic with occasional bursts.
func (e *Engine) runPeakTraffic(qps, userPool int) {
	e.wg.Add(1)
	defer e.wg.Done()

	interval := time.Second / time.Duration(qps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	burstTick := time.NewTicker(10 * time.Second)
	defer burstTick.Stop()

	for {
		select {
		case <-e.stopCh:
			return
		case <-burstTick.C:
			for i := 0; i < 50; i++ {
				e.generateOneEvent(userPool)
			}
		case <-ticker.C:
			e.generateOneEvent(userPool)
		}
	}
}

func (e *Engine) generateOneEvent(userPool int) {
	userID := fmt.Sprintf("%d", 1000+rand.Intn(userPool))
	eventType := rand.Intn(10)

	switch {
	case eventType < 5:
		items := randomItems()
		total := 0.0
		for _, item := range items {
			total += item.Price * float64(item.Quantity)
		}
		e.orderSvc.CreateOrder(userID, items, total)

	case eventType < 8:
		orders := e.orderSvc.GetUserOrders(userID)
		if len(orders) == 0 {
			return
		}
		order := orders[rand.Intn(len(orders))]
		if order.Status == model.OrderDelivered || order.Status == model.OrderCancelled {
			return
		}
		nextStatus := randomNextStatus(order.Status)
		if nextStatus == "" {
			return
		}
		e.orderSvc.ChangeOrderStatus(order.OrderID, nextStatus, nil)

	default:
		users := make([]string, rand.Intn(20)+5)
		for i := range users {
			users[i] = fmt.Sprintf("%d", 1000+rand.Intn(userPool))
		}
		e.flashSaleSvc.CreateFlashSale(
			"会员服务提醒",
			"重点用户可查看新的履约权益与服务消息",
			users,
		)
	}
}

func randomItems() []model.OrderItem {
	products := []struct {
		name  string
		price float64
	}{
		{"透明防摔保护壳", 129.00},
		{"无线办公键盘", 329.00},
		{"27 英寸办公显示器", 1599.00},
		{"降噪通话耳机", 699.00},
		{"企业采购服务包", 2499.00},
		{"远程售后服务单", 99.00},
		{"数字会员兑换码", 199.00},
		{"加急履约服务", 299.00},
	}
	n := rand.Intn(3) + 1
	items := make([]model.OrderItem, n)
	for i := 0; i < n; i++ {
		p := products[rand.Intn(len(products))]
		items[i] = model.OrderItem{
			ProductName: p.name,
			Quantity:    rand.Intn(2) + 1,
			Price:       p.price,
		}
	}
	return items
}

func randomNextStatus(current model.OrderStatus) model.OrderStatus {
	transitions := map[model.OrderStatus][]model.OrderStatus{
		model.OrderCreated:   {model.OrderConfirmed, model.OrderCancelled},
		model.OrderPaid:      {model.OrderConfirmed, model.OrderCancelled},
		model.OrderConfirmed: {model.OrderShipped, model.OrderCancelled},
		model.OrderShipped:   {model.OrderDelivered, model.OrderDeliveryFailed},
	}
	valid, ok := transitions[current]
	if !ok || len(valid) == 0 {
		return ""
	}
	return valid[rand.Intn(len(valid))]
}
