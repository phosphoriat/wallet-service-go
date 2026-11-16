package utils

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"regexp"
	"strings"
)

// RequestError is an error that is safe to show to the API client.
// The HTTP error handler responds with Status and {"error": Message}.
type RequestError struct {
	Status  int
	Message string
}

// Error implements the error interface.
func (e *RequestError) Error() string { return e.Message }

// BadRequest returns a RequestError with HTTP status 400.
func BadRequest(format string, args ...any) error {
	return &RequestError{Status: 400, Message: fmt.Sprintf(format, args...)}
}

// NotFound returns a RequestError with HTTP status 404.
func NotFound(format string, args ...any) error {
	return &RequestError{Status: 404, Message: fmt.Sprintf(format, args...)}
}

var (
	// hexAddressRe matches "0x" followed by exactly 40 hex characters (20 bytes).
	hexAddressRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	// decimalRe matches a non-negative decimal integer: no sign, no fraction, no exponent.
	decimalRe = regexp.MustCompile(`^[0-9]+$`)
	// maxUint256 is 2^256 - 1, the upper bound for on-chain amounts and fees.
	maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
)

// IsHexAddress reports whether s has the format of an EVM address
// ("0x" + 40 hex characters). The EIP-55 checksum is not verified.
func IsHexAddress(s string) bool { return hexAddressRe.MatchString(s) }

// ParseUint256 parses a decimal string as an unsigned 256-bit integer.
// Floats, signs and exponents are rejected. If allowZero is false, zero is rejected too.
// field is the parameter name used in error messages. All errors are BadRequest.
func ParseUint256(field, s string, allowZero bool) (*big.Int, error) {
	if s == "" {
		return nil, BadRequest("%s is required", field)
	}
	if !decimalRe.MatchString(s) {
		return nil, BadRequest("%s must be a non-negative decimal integer string", field)
	}

	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Cmp(maxUint256) > 0 {
		return nil, BadRequest("%s is out of uint256 range", field)
	}
	if !allowZero && v.Sign() == 0 {
		return nil, BadRequest("%s must be greater than zero", field)
	}

	return v, nil
}

// ParseHexData decodes 0x-prefixed hex data (e.g. transaction calldata).
// An empty string and "0x" both mean empty data and return nil.
// field is the parameter name used in error messages. All errors are BadRequest.
func ParseHexData(field, s string) ([]byte, error) {
	if s == "" || s == "0x" {
		return nil, nil
	}
	if !strings.HasPrefix(s, "0x") {
		return nil, BadRequest("%s must be 0x-prefixed hex", field)
	}

	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, BadRequest("%s is not valid hex", field)
	}
	return b, nil
}

// BigEndianBytes returns the minimal big-endian representation of x, as expected by wallet-core for uint256 protobuf fields.
// Zero is encoded as a single 0x00 byte rather than an empty slice.
func BigEndianBytes(x *big.Int) []byte {
	if x.Sign() == 0 {
		return []byte{0}
	}
	return x.Bytes()
}

// Wipe overwrites b with zeros. Used for private keys and serialized SigningInput messages that contain a private key.
func Wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// DecodeStrictJSON decodes data into dst.
// The body must contain exactly one JSON value, and unknown fields are rejected. All errors are BadRequest.
func DecodeStrictJSON(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return BadRequest("malformed JSON: %v", err)
	}
	// Anything after the first value (e.g. a second object) is an error.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("request body must contain a single JSON object")
	}

	return nil
}

// Protobuf wire types (https://protobuf.dev/programming-guides/encoding/).
// A minimal codec is enough here: wallet-core takes and returns serialized TW.<Chain>.Proto.* messages, so no code generation from .proto files is needed.
const (
	WireVarint  = 0 // int32, int64, uint32, uint64, bool, enum
	WireFixed64 = 1 // fixed64, sfixed64, double
	WireBytes   = 2 // string, bytes, embedded messages
	WireFixed32 = 5 // fixed32, sfixed32, float
)

// AppendVarint appends v to b as a base-128 varint
func AppendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// AppendVarintField appends a varint field (field key + value), e.g. an enum
func AppendVarintField(b []byte, field int, v uint64) []byte {
	b = AppendVarint(b, uint64(field)<<3|WireVarint)
	return AppendVarint(b, v)
}

// AppendBytesField appends a length-delimited field (field key + length + data): bytes, string or an embedded message
func AppendBytesField(b []byte, field int, v []byte) []byte {
	b = AppendVarint(b, uint64(field)<<3|WireBytes)
	b = AppendVarint(b, uint64(len(v)))
	return append(b, v...)
}

// WalkProto iterates over the top-level fields of a serialized protobuf message.
//
// fn is called for every varint field (value is set, data is nil) and every length-delimited field (data is set, value is 0).
// Fixed32 and fixed64 fields are skipped, data points into b and is only valid until b is modified.
// Returns an error if the message is truncated or malformed.
func WalkProto(b []byte, fn func(field uint64, value uint64, data []byte)) error {
	for len(b) > 0 {
		key, n := readVarint(b)
		if n <= 0 {
			return errors.New("protobuf: bad field key")
		}
		b = b[n:]
		// Field key = (field_number << 3) | wire_type.
		field, wire := key>>3, key&7

		switch wire {
		case WireVarint:
			v, n := readVarint(b)
			if n <= 0 {
				return errors.New("protobuf: bad varint")
			}
			b = b[n:]
			fn(field, v, nil)
		case WireBytes:
			l, n := readVarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return errors.New("protobuf: bad length")
			}
			fn(field, 0, b[n:n+int(l)])
			b = b[n+int(l):]
		case WireFixed64:
			if len(b) < 8 {
				return errors.New("protobuf: truncated fixed64")
			}
			b = b[8:]
		case WireFixed32:
			if len(b) < 4 {
				return errors.New("protobuf: truncated fixed32")
			}
			b = b[4:]
		default:
			return fmt.Errorf("protobuf: unsupported wire type %d", wire)
		}
	}
	return nil
}

// readVarint decodes a base-128 varint from the start of b.
// Returns the value and the number of bytes read, or -1 if the varint is truncated or longer than 10 bytes (the maximum for uint64).
func readVarint(b []byte) (uint64, int) {
	var v uint64

	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1
		}
	}

	return 0, -1
}

// LoadDotEnv reads KEY=VALUE lines from the file at path and sets the environment variables that are not set yet:
// the process environment takes priority over the .env file.
//
// Supported syntax: empty lines, "#" comments, an optional "export " prefix, and values wrapped in single or double quotes.
// Returns the os.Open error as is, so callers can ignore a missing file with errors.Is(err, fs.ErrNotExist).
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}

		s = strings.TrimPrefix(s, "export ")
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, line)
		}

		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		// Strip matching surrounding quotes: "value" or 'value'.
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// Env returns the value of the environment variable key, or def if the variable is not set or empty.
func Env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
