package chains

/*
#cgo LDFLAGS: -Wl,--start-group -lTrustWalletCore -lwallet_core_rs -lTrezorCrypto -lprotobuf -Wl,--end-group -lstdc++ -lm -ldl -lpthread
#include <stdlib.h>
#include <TrustWalletCore/TWAnyAddress.h>
#include <TrustWalletCore/TWAnySigner.h>
#include <TrustWalletCore/TWCoinType.h>
#include <TrustWalletCore/TWData.h>
#include <TrustWalletCore/TWHDWallet.h>
#include <TrustWalletCore/TWHash.h>
#include <TrustWalletCore/TWPrivateKey.h>
#include <TrustWalletCore/TWString.h>
*/
import "C"

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"unsafe"

	"wallet-service/internal/config"
	"wallet-service/internal/utils"
)

// Build notes: header and library paths of wallet-core are passed via CGO_CFLAGS / CGO_LDFLAGS (see README). The compiler must be clang.
// --start-group/--end-group lets GNU ld resolve circular dependencies between the static wallet-core libraries regardless of their order.

const (
	// ethereumCoin is the wallet-core coin type. One type covers all EVM networks;
	// the network itself is selected by chain_id in the transaction.
	ethereumCoin = C.TWCoinTypeEthereum
	// ethereumCoinType is the SLIP-44 coin type used in the derivation path.
	ethereumCoinType = 60
	// txModeEnveloped is TW.Ethereum.Proto.TransactionMode.Enveloped: an EIP-2718 type 0x02 (EIP-1559) transaction.
	txModeEnveloped = 1
)

// Ethereum is the HD wallet of an EVM network, backed by wallet-core.
type Ethereum struct {
	// mu serializes all HDWallet calls: wallet-core does not document its objects as thread-safe.
	mu     sync.Mutex
	wallet *C.struct_TWHDWallet
}

// NewEthereum creates a wallet from the gate's mnemonic (and optional BIP-39 passphrase).
// Returns an error if wallet-core rejects the mnemonic.
func NewEthereum(g config.Gate) (Chain, error) {
	m := newTWString(g.Mnemonic)
	p := newTWString(g.Passphrase)
	defer C.TWStringDelete(m)
	defer C.TWStringDelete(p)

	w := C.TWHDWalletCreateWithMnemonic(m, p)
	if w == nil {
		return nil, errors.New("invalid mnemonic")
	}
	return &Ethereum{wallet: w}, nil
}

// Close releases the wallet. Safe to call more than once.
func (e *Ethereum) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.wallet != nil {
		C.TWHDWalletDelete(e.wallet)
		e.wallet = nil
	}
}

// DeriveAddress returns the EIP-55 address at m/44'/60'/account'/change/address_index and the derivation path.
func (e *Ethereum) DeriveAddress(p Path) (string, string, error) {
	var address string
	err := e.withKey(p, func(key *C.struct_TWPrivateKey) error {
		address = keyAddress(key)
		return nil
	})

	return address, p.BIP44(ethereumCoinType), err
}

// ValidateAddress checks the address with wallet-core and returns its EIP-55 form.
//
// Addresses written entirely in lower or upper case carry no checksum and are accepted as is.
// For a mixed-case address the EIP-55 checksum is verified as well, so a typo in a checksummed address is rejected.
func (e *Ethereum) ValidateAddress(address string) (bool, string) {
	if !utils.IsHexAddress(address) {
		return false, ""
	}

	s := newTWString(address)
	defer C.TWStringDelete(s)

	if !bool(C.TWAnyAddressIsValid(s, ethereumCoin)) {
		return false, ""
	}
	a := C.TWAnyAddressCreateWithString(s, ethereumCoin)
	if a == nil {
		return false, ""
	}
	defer C.TWAnyAddressDelete(a)

	d := unsafe.Pointer(C.TWAnyAddressDescription(a))
	defer C.TWStringDelete(d)
	checksummed := goString(d)

	body := strings.TrimPrefix(address, "0x")
	mixedCase := body != strings.ToLower(body) && body != strings.ToUpper(body)
	if mixedCase && address != checksummed {
		return false, ""
	}
	return true, checksummed
}

// SignTx signs an EIP-1559 transaction with the key at path p.
//
// The private key exists in Go memory only for the duration of the call:
// the key copy and the serialized SigningInput (which contains the key) are wiped before returning.
func (e *Ethereum) SignTx(p Path, params TxParams) (*SignedTx, error) {
	tx, err := params.parse()
	if err != nil {
		return nil, err
	}
	if ok, _ := e.ValidateAddress(tx.To); !ok {
		return nil, utils.BadRequest("tx_params.to is not a valid address")
	}

	var signed *SignedTx
	err = e.withKey(p, func(key *C.struct_TWPrivateKey) error {
		pk := keyBytes(key)
		input := signingInput(tx, pk)
		defer utils.Wipe(pk)
		defer utils.Wipe(input)

		encoded, err := parseSigningOutput(anySign(input))
		if err != nil {
			return err
		}
		signed = &SignedTx{
			From: keyAddress(key),
			// The transaction hash is keccak256 of the signed raw transaction.
			TxHash:   "0x" + hex.EncodeToString(keccak256(encoded)),
			SignedTx: "0x" + hex.EncodeToString(encoded),
		}

		return nil
	})
	return signed, err
}

// withKey derives the private key at path p, passes it to fn and frees it afterwards.
// The whole operation runs under the wallet mutex.
func (e *Ethereum) withKey(p Path, fn func(*C.struct_TWPrivateKey) error) error {
	if err := p.Validate(); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.wallet == nil {
		return errors.New("wallet is closed")
	}

	path := newTWString(p.BIP44(ethereumCoinType))
	defer C.TWStringDelete(path)

	key := C.TWHDWalletGetKey(e.wallet, ethereumCoin, path)
	if key == nil {
		return errors.New("key derivation failed")
	}
	defer C.TWPrivateKeyDelete(key)

	return fn(key)
}

// signingInput serializes TW.Ethereum.Proto.SigningInput (src/proto/Ethereum.proto):
//
//	1 chain_id, 2 nonce, 3 tx_mode, 5 gas_limit, 6 max_inclusion_fee_per_gas,
//	7 max_fee_per_gas, 8 to_address, 9 private_key, 10 transaction{1 transfer{1 amount, 2 data}}
//
// Transfer{amount, data} covers both ETH (empty data) and ERC-20 (to = token contract, amount = 0, data = transfer calldata).
// Field 4 (gas_price) is legacy-only and is omitted. The result contains the private key: the caller must wipe it.
func signingInput(tx *evmTx, privateKey []byte) []byte {
	var transfer []byte
	transfer = utils.AppendBytesField(transfer, 1, utils.BigEndianBytes(tx.Value))

	if len(tx.Data) > 0 {
		transfer = utils.AppendBytesField(transfer, 2, tx.Data)
	}
	transaction := utils.AppendBytesField(nil, 1, transfer)

	b := make([]byte, 0, 256+len(tx.Data))
	b = utils.AppendBytesField(b, 1, utils.BigEndianBytes(new(big.Int).SetUint64(tx.ChainID)))
	b = utils.AppendBytesField(b, 2, utils.BigEndianBytes(new(big.Int).SetUint64(tx.Nonce)))
	b = utils.AppendVarintField(b, 3, txModeEnveloped)
	b = utils.AppendBytesField(b, 5, utils.BigEndianBytes(new(big.Int).SetUint64(tx.GasLimit)))
	b = utils.AppendBytesField(b, 6, utils.BigEndianBytes(tx.MaxPriority))
	b = utils.AppendBytesField(b, 7, utils.BigEndianBytes(tx.MaxFee))
	b = utils.AppendBytesField(b, 8, []byte(tx.To))
	b = utils.AppendBytesField(b, 9, privateKey)
	b = utils.AppendBytesField(b, 10, transaction)

	return b
}

// parseSigningOutput extracts the encoded transaction (field 1) and the error (6 error, 7 error_message) from TW.Ethereum.Proto.SigningOutput.
//
// A signing error reported by wallet-core is returned as BadRequest, since it is caused by the request parameters;
// a malformed or empty output is an internal error.
func parseSigningOutput(b []byte) ([]byte, error) {
	var (
		encoded []byte
		code    uint64
		message string
	)

	err := utils.WalkProto(b, func(field, value uint64, data []byte) {
		switch field {
		case 1:
			// Copy: data points into the output buffer.
			encoded = append([]byte(nil), data...)
		case 6:
			code = value
		case 7:
			message = string(data)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("decode signing output: %w", err)
	}

	if code != 0 || message != "" {
		return nil, utils.BadRequest("wallet-core signing error %d: %s", code, message)
	}

	if len(encoded) == 0 {
		return nil, errors.New("wallet-core returned empty transaction")
	}
	return encoded, nil
}

/* Wallet Core C API */
// newTWString creates a TWString from a Go string.
// The caller must free it with C.TWStringDelete.
func newTWString(s string) unsafe.Pointer {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	return unsafe.Pointer(C.TWStringCreateWithUTF8Bytes(cs))
}

// goString copies a TWString into a Go string (the TWString is not freed)
func goString(s unsafe.Pointer) string { return C.GoString(C.TWStringUTF8Bytes(s)) }

// newTWData creates a TWData with a copy of b.
// The caller must free it with C.TWDataDelete (or deleteSecret for sensitive data).
func newTWData(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return unsafe.Pointer(C.TWDataCreateWithSize(0))
	}
	return unsafe.Pointer(C.TWDataCreateWithBytes((*C.uchar)(unsafe.Pointer(&b[0])), C.size_t(len(b))))
}

// goBytes copies the contents of a TWData into a Go slice (the TWData is not freed)
func goBytes(d unsafe.Pointer) []byte {
	return C.GoBytes(unsafe.Pointer(C.TWDataBytes(d)), C.int(C.TWDataSize(d)))
}

// deleteSecret zeroes a TWData before freeing it. Used for private keys and for SigningInput messages that contain a private key.
func deleteSecret(d unsafe.Pointer) {
	C.TWDataReset(d)
	C.TWDataDelete(d)
}

// keyAddress returns the EIP-55 address of a private key
func keyAddress(key *C.struct_TWPrivateKey) string {
	s := unsafe.Pointer(C.TWCoinTypeDeriveAddress(ethereumCoin, key))
	defer C.TWStringDelete(s)
	return goString(s)
}

// keyBytes returns a Go copy of the 32-byte private key. The C-side copy is wiped; the caller must wipe the returned slice.
func keyBytes(key *C.struct_TWPrivateKey) []byte {
	d := unsafe.Pointer(C.TWPrivateKeyData(key))
	defer deleteSecret(d)
	return goBytes(d)
}

// anySign calls TWAnySignerSign with a serialized SigningInput and returns the serialized SigningOutput. The C copy of the input is wiped after use.
func anySign(input []byte) []byte {
	in := newTWData(input)
	defer deleteSecret(in)
	out := unsafe.Pointer(C.TWAnySignerSign(in, ethereumCoin))
	defer C.TWDataDelete(out)
	return goBytes(out)
}

// keccak256 returns the Keccak-256 hash of b (as used by Ethereum, not SHA3-256)
func keccak256(b []byte) []byte {
	in := newTWData(b)
	defer C.TWDataDelete(in)
	out := unsafe.Pointer(C.TWHashKeccak256(in))
	defer C.TWDataDelete(out)
	return goBytes(out)
}
