package simulator

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Terry-Mao/goim/internal/notify/conf"
	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/service"
)

// Engine manages load generation simulations.
type Engine struct {
	mu      sync.Mutex
	running atomic.Bool
	mode    string
	qps     float64
	users   int
	startAt time.Time

	orderSvc     *service.OrderNotifyService
	flashSaleSvc *service.FlashSaleService
	cfg          *conf.Config

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewEngine creates a new simulation engine.
func NewEngine(orderSvc *service.OrderNotifyService, flashSaleSvc *service.FlashSaleService, cfg *conf.Config) *Engine {
	return &Engine{
		orderSvc:     orderSvc,
		flashSaleSvc: flashSaleSvc,
		cfg:          cfg,
	}
}

// Start begins a simulation with the given mode.
func (e *Engine) Start(mode string, qps, users int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running.Load() {
		return fmt.Errorf("simulation already running")
	}

	e.mode = mode
	e.qps = float64(qps)
	e.users = users
	e.startAt = time.Now()
	e.stopCh = make(chan struct{})
	e.running.Store(true)

	switch mode {
	case "lifecycle":
		go e.runOrderLifecycle()
	case "normal":
		go e.runNormalTraffic(qps, users)
	case "peak":
		go e.runPeakTraffic(qps, users)
	case "flash_sale":
		go e.runFlashSaleBurst(qps, users)
	default:
		e.running.Store(false)
		return fmt.Errorf("unknown simulation mode: %s", mode)
	}

	return nil
}

// Stop halts the running simulation.
func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.running.Load() {
		e.mu.Unlock()
		return
	}
	close(e.stopCh)
	e.mu.Unlock()
	e.wg.Wait()
	e.running.Store(false)
}

// stopInternal closes the stop channel without waiting on the WaitGroup.
// Use this from within a goroutine that is tracked by e.wg (e.g. order lifecycle)
// to avoid a self-deadlock where Stop() calls wg.Wait() on the calling goroutine.
func (e *Engine) stopInternal() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running.Load() {
		close(e.stopCh)
		e.running.Store(false)
	}
}

// Status returns the current simulation state.
func (e *Engine) Status() model.SimulationState {
	uptime := int64(0)
	if e.running.Load() {
		uptime = int64(time.Since(e.startAt).Seconds())
	}
	return model.SimulationState{
		Active:        e.running.Load(),
		Mode:          e.mode,
		QPS:           e.qps,
		UptimeSeconds: uptime,
	}
}
