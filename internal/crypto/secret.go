// Package crypto provides PayMux's at-rest encryption, password hashing,
// API-key hashing and HMAC signing primitives.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrDecrypt is returned when a ciphertext cannot be authenticated. It is
// deliberately opaque: callers must not learn why decryption failed.
var ErrDecrypt = errors.New("crypto: could not decrypt value")

// Sealer encrypts and decrypts gateway secrets with AES-256-GCM.
//
// Sealed values carry a version prefix so the key can be rotated later
// without ambiguity about how an existing ciphertext was produced.
type Sealer struct {
	aead cipher.AEAD
}

const sealVersion = "v1"

// NewSealer builds a Sealer from a 32-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext, binding it to context so a ciphertext cannot be
// moved between fields or rows. The result is "v1.<base64 nonce||ciphertext>".
func (s *Sealer) Seal(plaintext []byte, context string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("crypto: entropy source failed: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, plaintext, []byte(context))
	return sealVersion + "." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Open reverses Seal. The context must match the one used to seal.
func (s *Sealer) Open(sealed string, context string) ([]byte, error) {
	if len(sealed) < len(sealVersion)+1 || sealed[:len(sealVersion)+1] != sealVersion+"." {
		return nil, ErrDecrypt
	}
	raw, err := base64.RawURLEncoding.DecodeString(sealed[len(sealVersion)+1:])
	if err != nil {
		return nil, ErrDecrypt
	}
	n := s.aead.NonceSize()
	if len(raw) < n {
		return nil, ErrDecrypt
	}
	out, err := s.aead.Open(nil, raw[:n], raw[n:], []byte(context))
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}

// SealString is Seal for string plaintexts.
func (s *Sealer) SealString(plaintext, context string) (string, error) {
	return s.Seal([]byte(plaintext), context)
}

// OpenString is Open returning a string.
func (s *Sealer) OpenString(sealed, context string) (string, error) {
	b, err := s.Open(sealed, context)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Secret wraps a sensitive string so it cannot be leaked by accidental
// formatting, logging or JSON serialisation.
type Secret string

// String implements fmt.Stringer with a redacted value.
func (s Secret) String() string { return "[REDACTED]" }

// GoString implements fmt.GoStringer with a redacted value.
func (s Secret) GoString() string { return "[REDACTED]" }

// MarshalJSON always emits null so a Secret never escapes through an API.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// LogValue implements slog.LogValuer with a redacted value.
func (s Secret) LogValue() any { return "[REDACTED]" }

// Reveal returns the underlying value. Every call site should be auditable.
func (s Secret) Reveal() string { return string(s) }
