package simulator

import (
	"fmt"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

// runOrderLifecycle runs a single purchase order through the demo lifecycle.
func (e *Engine) runOrderLifecycle() {
	e.wg.Add(1)
	defer e.wg.Done()

	userID := fmt.Sprintf("%d", time.Now().UnixNano()%10000+1000)
	items := []model.OrderItem{
		{ProductName: "即时履约服务包", Quantity: 1, Price: 1299.00},
	}

	order, _, err := e.orderSvc.CreateOrder(userID, items, 1299.00)
	if err != nil {
		return
	}

	transitions := []struct {
		status model.OrderStatus
		delay  time.Duration
		extra  map[string]string
	}{
		{model.OrderConfirmed, 3 * time.Second, nil},
		{model.OrderShipped, 4 * time.Second, map[string]string{"location": "华东履约中心"}},
		{model.OrderDelivered, 5 * time.Second, nil},
	}

	for _, t := range transitions {
		select {
		case <-e.stopCh:
			return
		case <-time.After(t.delay):
		}

		if _, _, err := e.orderSvc.ChangeOrderStatus(order.OrderID, t.status, t.extra); err != nil {
			return
		}
	}

	select {
	case <-e.stopCh:
		return
	case <-time.After(5 * time.Second):
	}
	e.stopInternal()
}
