package services

import (
	"log/slog"

	"wallet-service/internal/services/chains"
)

type AddressService struct {
	chains *chains.Registry
	log    *slog.Logger
}

func NewAddressService(r *chains.Registry, log *slog.Logger) *AddressService {
	return &AddressService{chains: r, log: log}
}

type Address struct {
	Gate         string `json:"gate"`
	Account      uint32 `json:"account"`
	Change       uint32 `json:"change"`
	AddressIndex uint32 `json:"address_index"`
	Path         string `json:"path"`
	Address      string `json:"address"`
}

type AddressValidation struct {
	Valid   bool   `json:"valid"`
	Address string `json:"address,omitempty"`
}

// Create displays the gate address in the BIP-44 path.
func (s *AddressService) Create(gate string, p chains.Path) (*Address, error) {
	chain, err := s.chains.Get(gate)
	if err != nil {
		return nil, err
	}

	address, path, err := chain.DeriveAddress(p)
	if err != nil {
		return nil, err
	}
	s.log.Info("address derived", "gate", gate, "path", path, "address", address)

	return &Address{
		Gate: gate, Account: p.Account, Change: p.Change, AddressIndex: p.AddressIndex,
		Path: path, Address: address,
	}, nil
}

// Validate validates the address. An invalid address is not an error, but valid = false.
func (s *AddressService) Validate(gate, address string) (*AddressValidation, error) {
	chain, err := s.chains.Get(gate)
	if err != nil {
		return nil, err
	}

	valid, normalized := chain.ValidateAddress(address)
	return &AddressValidation{Valid: valid, Address: normalized}, nil
}
