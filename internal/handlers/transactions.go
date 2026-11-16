package handlers

import (
	"strings"

	"wallet-service/internal/middleware"
	"wallet-service/internal/services"
	"wallet-service/internal/services/chains"
	"wallet-service/internal/utils"

	"github.com/gofiber/fiber/v3"
)

type TransactionHandler struct {
	svc *services.TransactionService
}

func NewTransactionHandler(svc *services.TransactionService) *TransactionHandler {
	return &TransactionHandler{svc: svc}
}

// SignTxRequest POST /api/v1/tx
type SignTxRequest struct {
	Gate         string           `json:"gate"`
	Account      uint32           `json:"account"`
	Change       uint32           `json:"change"`
	AddressIndex uint32           `json:"address_index"`
	TxParams     *chains.TxParams `json:"tx_params"`
}

func (r *SignTxRequest) Validate() error {
	if strings.TrimSpace(r.Gate) == "" {
		return utils.BadRequest("gate is required")
	}
	if err := r.path().Validate(); err != nil {
		return err
	}
	if r.TxParams == nil {
		return utils.BadRequest("tx_params is required")
	}
	return r.TxParams.Validate()
}

func (r *SignTxRequest) path() chains.Path {
	return chains.Path{Account: r.Account, Change: r.Change, AddressIndex: r.AddressIndex}
}

// Sign POST /api/v1/tx (offline only)
func (h *TransactionHandler) Sign(c fiber.Ctx) error {
	req := middleware.Body[SignTxRequest](c)

	signed, err := h.svc.Sign(req.Gate, req.path(), *req.TxParams)
	if err != nil {
		return err
	}
	return c.JSON(signed)
}
