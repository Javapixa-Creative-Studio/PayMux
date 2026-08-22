package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// KeySize is the required length in bytes of the at-rest encryption key.
const KeySize = 32

// decodeKey accepts a hex or base64 encoded key and verifies its length.
func decodeKey(raw string) ([]byte, error) {
	if b, err := hex.DecodeString(raw); err == nil {
		return checkKeyLen(b)
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return checkKeyLen(b)
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil {
		return checkKeyLen(b)
	}
	return nil, errors.New("must be hex or base64 encoded")
}

func checkKeyLen(b []byte) ([]byte, error) {
	if len(b) != KeySize {
		return nil, fmt.Errorf("must decode to %d bytes, got %d", KeySize, len(b))
	}
	return b, nil
}
