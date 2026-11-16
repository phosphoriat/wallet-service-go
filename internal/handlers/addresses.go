package handlers

import (
	"strings"

	"wallet-service/internal/middleware"
	"wallet-service/internal/services"
	"wallet-service/internal/services/chains"
	"wallet-service/internal/utils"

	"github.com/gofiber/fiber/v3"
)

type AddressHandler struct {
	svc *services.AddressService
}

func NewAddressHandler(svc *services.AddressService) *AddressHandler {
	return &AddressHandler{svc: svc}
}

// CreateAddressRequest POST /api/v1/createaddress
type CreateAddressRequest struct {
	Gate         string `json:"gate"`
	Account      uint32 `json:"account"`
	Change       uint32 `json:"change"`
	AddressIndex uint32 `json:"address_index"`
}

func (r *CreateAddressRequest) Validate() error {
	if strings.TrimSpace(r.Gate) == "" {
		return utils.BadRequest("gate is required")
	}
	return r.path().Validate()
}

func (r *CreateAddressRequest) path() chains.Path {
	return chains.Path{Account: r.Account, Change: r.Change, AddressIndex: r.AddressIndex}
}

// Create POST /api/v1/createaddress
func (h *AddressHandler) Create(c fiber.Ctx) error {
	req := middleware.Body[CreateAddressRequest](c)

	address, err := h.svc.Create(req.Gate, req.path())
	if err != nil {
		return err
	}
	return c.JSON(address)
}

// ValidateAddressRequest POST /api/v1/validateaddress
type ValidateAddressRequest struct {
	Gate    string `json:"gate"`
	Address string `json:"address"`
}

func (r *ValidateAddressRequest) Validate() error {
	if strings.TrimSpace(r.Gate) == "" {
		return utils.BadRequest("gate is required")
	}
	r.Address = strings.TrimSpace(r.Address)
	if r.Address == "" {
		return utils.BadRequest("address is required")
	}
	return nil
}

func (h *AddressHandler) Validate(c fiber.Ctx) error {
	req := middleware.Body[ValidateAddressRequest](c)

	result, err := h.svc.Validate(req.Gate, req.Address)
	if err != nil {
		return err
	}
	return c.JSON(result)
}
