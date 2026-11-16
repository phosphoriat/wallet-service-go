package services

import (
	"log/slog"

	"wallet-service/internal/services/chains"
)

type TransactionService struct {
	chains *chains.Registry
	log    *slog.Logger
}

func NewTransactionService(r *chains.Registry, log *slog.Logger) *TransactionService {
	return &TransactionService{chains: r, log: log}
}

// Sign signs the transaction with the gate key on path p
func (s *TransactionService) Sign(gate string, p chains.Path, params chains.TxParams) (*chains.SignedTx, error) {
	chain, err := s.chains.Get(gate)
	if err != nil {
		return nil, err
	}

	signed, err := chain.SignTx(p, params)
	if err != nil {
		return nil, err
	}

	s.log.Info("tx signed",
		"gate", gate,
		"from", signed.From,
		"to", params.To,
		"nonce", *params.Nonce,
		"chain_id", params.ChainID,
		"tx_hash", signed.TxHash,
	)
	return signed, nil
}
