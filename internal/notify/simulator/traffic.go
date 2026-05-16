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
			// Burst: create 50 orders at once
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
		// Create new order
		items := randomItems()
		total := 0.0
		for _, item := range items {
			total += item.Price * float64(item.Quantity)
		}
		e.orderSvc.CreateOrder(userID, items, total)

	case eventType < 8:
		// Status change on existing order
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
		// Occasional flash sale to random subset
		users := make([]string, rand.Intn(20)+5)
		for i := range users {
			users[i] = fmt.Sprintf("%d", 1000+rand.Intn(userPool))
		}
		e.flashSaleSvc.CreateFlashSale(
			"限时特惠",
			"精选商品限时折扣，立即抢购！",
			users,
		)
	}
}

func randomItems() []model.OrderItem {
	products := []struct {
		name  string
		price float64
	}{
		{"iPhone 15 Pro Max", 9999.00},
		{"MacBook Pro 14", 14999.00},
		{"AirPods Pro", 1999.00},
		{"iPad Air", 4799.00},
		{"Apple Watch Ultra", 6299.00},
		{"机械键盘 Cherry MX", 899.00},
		{"4K显示器 27寸", 2999.00},
		{"无线鼠标", 299.00},
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
		model.OrderCreated:   {model.OrderPaid, model.OrderCancelled},
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
