package handler

import (
	"github.com/Terry-Mao/goim/internal/notify/model"
	"github.com/Terry-Mao/goim/internal/notify/service"
)

// Handler holds references to all business services used by HTTP handlers.
type Handler struct {
	orderSvc     *service.OrderNotifyService
	flashSaleSvc *service.FlashSaleService
	simulator    Simulator
}

// Simulator is the interface for the load generator engine.
type Simulator interface {
	Start(mode string, qps, users int) error
	Stop()
	Status() model.SimulationState
}

// New creates a new Handler with the given services.
func New(orderSvc *service.OrderNotifyService, flashSaleSvc *service.FlashSaleService) *Handler {
	return &Handler{
		orderSvc:     orderSvc,
		flashSaleSvc: flashSaleSvc,
	}
}

// SetSimulator attaches a simulation engine to the handler.
func (h *Handler) SetSimulator(sim Simulator) {
	h.simulator = sim
}
