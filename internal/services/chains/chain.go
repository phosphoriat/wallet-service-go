package chains

import (
	"fmt"
	"math/big"

	"wallet-service/internal/utils"
)

// Chain is the HD wallet of a single network.
type Chain interface {
	// DeriveAddress returns the address at the BIP-44 path p and the path itself.
	DeriveAddress(p Path) (address string, path string, err error)
	// ValidateAddress checks the address and returns its canonical form.
	ValidateAddress(address string) (valid bool, normalized string)
	// SignTx signs a transaction offline with the key at the path p.
	SignTx(p Path, params TxParams) (*SignedTx, error)
	// Close releases wallet-core resources.
	Close()
}

// MaxPathIndex is the maximum BIP-32 node index (2^31 - 1).
// Higher values are reserved for hardened derivation, which is applied to the account level by the path format itself.
const MaxPathIndex = 0x7FFFFFFF

// Path is the variable part of the BIP-44 path
// m / 44' / coin_type' / account' / change / address_index.
type Path struct {
	Account      uint32
	Change       uint32
	AddressIndex uint32
}

// Validate checks that every index is within [0, MaxPathIndex].
// Returns a BadRequest error otherwise.
func (p Path) Validate() error {
	if p.Account > MaxPathIndex || p.Change > MaxPathIndex || p.AddressIndex > MaxPathIndex {
		return utils.BadRequest("account, change and address_index must be in [0, %d]", MaxPathIndex)
	}
	return nil
}

// BIP44 returns the full derivation path for the given SLIP-44 coin type, e.g. "m/44'/60'/0'/0/15" for Ethereum (coin type 60).
func (p Path) BIP44(coinType uint32) string {
	return fmt.Sprintf("m/44'/%d'/%d'/%d/%d", coinType, p.Account, p.Change, p.AddressIndex)
}

// TxParams are the tx_params of POST /api/v1/tx: an EVM EIP-1559 transaction.
// Amounts and fees are decimal strings (uint256); nonce, chain_id and gas_limit are integers.
// Nonce is a pointer so that a missing value can be told apart from 0.
type TxParams struct {
	To                      string  `json:"to"`
	ValueWei                string  `json:"value_wei"`
	Data                    string  `json:"data"`
	Nonce                   *uint64 `json:"nonce"`
	ChainID                 uint64  `json:"chain_id"`
	GasLimit                uint64  `json:"gas_limit"`
	MaxFeePerGasWei         string  `json:"max_fee_per_gas_wei"`
	MaxPriorityFeePerGasWei string  `json:"max_priority_fee_per_gas_wei"`
}

// SignedTx is the result of signing, returned by POST /api/v1/tx
type SignedTx struct {
	// From is the address of the signing key.
	From string `json:"from"`
	// TxHash is keccak256 of the signed transaction, 0x-prefixed hex.
	TxHash string `json:"tx_hash"`
	// SignedTx is the signed raw transaction, 0x-prefixed hex, ready for eth_sendRawTransaction.
	SignedTx string `json:"signed_tx"`
}

// evmTx holds TxParams after parsing and validation.
type evmTx struct {
	To          string
	Value       *big.Int
	Data        []byte
	Nonce       uint64
	ChainID     uint64
	GasLimit    uint64
	MaxFee      *big.Int
	MaxPriority *big.Int
}

// Allowed gas limit range: 21,000 is the cost of a plain ETH transfer;
// 30,000,000 is a sanity cap at the level of a block gas limit.
const (
	minGasLimit = 21_000
	maxGasLimit = 30_000_000
)

// Validate checks the format of the parameters without calling wallet-core.
// Returns a BadRequest error describing the first invalid field.
func (p TxParams) Validate() error {
	_, err := p.parse()
	return err
}

// parse validates the parameters and converts them to an evmTx.
// Checks: "to" is a 20-byte hex address, amounts and fees are uint256 decimal strings (max fee must be > 0),
// data is valid 0x-hex, nonce and chain_id are set, gas_limit is within range, and the priority fee does not exceed the max fee.
func (p TxParams) parse() (*evmTx, error) {
	if !utils.IsHexAddress(p.To) {
		return nil, utils.BadRequest("tx_params.to must be a 0x-prefixed 20-byte hex address")
	}

	value, err := utils.ParseUint256("tx_params.value_wei", p.ValueWei, true)
	if err != nil {
		return nil, err
	}

	data, err := utils.ParseHexData("tx_params.data", p.Data)
	if err != nil {
		return nil, err
	}

	if p.Nonce == nil {
		return nil, utils.BadRequest("tx_params.nonce is required")
	}
	if p.ChainID == 0 {
		return nil, utils.BadRequest("tx_params.chain_id is required")
	}
	if p.GasLimit < minGasLimit || p.GasLimit > maxGasLimit {
		return nil, utils.BadRequest("tx_params.gas_limit must be in [%d, %d]", minGasLimit, maxGasLimit)
	}

	maxFee, err := utils.ParseUint256("tx_params.max_fee_per_gas_wei", p.MaxFeePerGasWei, false)
	if err != nil {
		return nil, err
	}

	priority, err := utils.ParseUint256("tx_params.max_priority_fee_per_gas_wei", p.MaxPriorityFeePerGasWei, true)
	if err != nil {
		return nil, err
	}
	// EIP-1559 invariant: the tip is part of the max fee, so it cannot exceed it.
	if priority.Cmp(maxFee) > 0 {
		return nil, utils.BadRequest("max_priority_fee_per_gas_wei must not exceed max_fee_per_gas_wei")
	}

	return &evmTx{
		To: p.To, Value: value, Data: data, Nonce: *p.Nonce, ChainID: p.ChainID,
		GasLimit: p.GasLimit, MaxFee: maxFee, MaxPriority: priority,
	}, nil
}
