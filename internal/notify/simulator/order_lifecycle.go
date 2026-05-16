package simulator

import (
	"fmt"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/model"
)

// runOrderLifecycle runs a single order through its full lifecycle for demo purposes.
func (e *Engine) runOrderLifecycle() {
	e.wg.Add(1)
	defer e.wg.Done()

	userID := fmt.Sprintf("%d", time.Now().UnixNano()%10000+1000)
	items := []model.OrderItem{
		{ProductName: "iPhone 15 Pro Max", Quantity: 1, Price: 9999.00},
	}

	order, _, err := e.orderSvc.CreateOrder(userID, items, 9999.00)
	if err != nil {
		return
	}

	transitions := []struct {
		status model.OrderStatus
		delay  time.Duration
		extra  map[string]string
	}{
		{model.OrderPaid, 2 * time.Second, nil},
		{model.OrderConfirmed, 3 * time.Second, nil},
		{model.OrderShipped, 4 * time.Second, map[string]string{"location": "杭州仓"}},
		{model.OrderDelivered, 5 * time.Second, nil},
	}

	for _, t := range transitions {
		select {
		case <-e.stopCh:
			return
		case <-time.After(t.delay):
		}

		_, _, err := e.orderSvc.ChangeOrderStatus(order.OrderID, t.status, t.extra)
		if err != nil {
			return
		}
	}

	// Keep running indicator for a bit
	select {
	case <-e.stopCh:
		return
	case <-time.After(5 * time.Second):
	}
	e.stopInternal()
}
