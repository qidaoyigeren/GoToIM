package simulator

import (
	"fmt"
	"math/rand"
	"time"
)

// runFlashSaleBurst sends a burst of flash sale notifications to many users.
func (e *Engine) runFlashSaleBurst(qps, userCount int) {
	e.wg.Add(1)
	defer e.wg.Done()

	// Generate target user IDs
	users := make([]string, userCount)
	for i := 0; i < userCount; i++ {
		users[i] = fmt.Sprintf("%d", 1000+i)
	}

	titles := []string{
		"iPhone 15 限时抢购",
		"双11 数码专场",
		"品牌日 全场5折",
		"新品首发 限量100台",
	}
	descs := []string{
		"全场低至5折，数量有限，先到先得！",
		"限时2小时，错过再等一年！",
		"首发特惠，前100名下单送赠品！",
	}

	// Burst: send all within a short window
	// Batch users into chunks for rate limiting
	batchSize := max(qps, 100)
	for i := 0; i < userCount; i += batchSize {
		select {
		case <-e.stopCh:
			return
		default:
		}

		end := i + batchSize
		if end > userCount {
			end = userCount
		}
		batch := users[i:end]

		title := titles[rand.Intn(len(titles))]
		desc := descs[rand.Intn(len(descs))]

		_, err := e.flashSaleSvc.CreateFlashSale(title, desc, batch)
		if err != nil {
			continue
		}

		// Brief pause between batches to simulate realistic burst pattern
		time.Sleep(time.Duration(1000/qps) * time.Millisecond)
	}

	e.stopInternal()
}
