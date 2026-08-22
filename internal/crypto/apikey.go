package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeyMode distinguishes credentials issued against the sandbox gateway from
// those issued against production.
type KeyMode string

const (
	KeyModeLive KeyMode = "live"
	KeyModeTest KeyMode = "test"
)

// Valid reports whether m is a recognised key mode.
func (m KeyMode) Valid() bool { return m == KeyModeLive || m == KeyModeTest }

const (
	apiKeyPrefix = "pmx"
	// apiKeySecretBytes yields 256 bits of entropy in the random portion.
	apiKeySecretBytes = 32
	// APIKeyDisplayPrefixLen is how much of a key is retained in cleartext so
	// operators can recognise it in the dashboard.
	APIKeyDisplayPrefixLen = 16
)

// keyEncoding renders the random portion of a key. Base32 keeps keys
// alphanumeric, so "_" stays unambiguous as the key's field separator and the
// value survives being pasted into URLs, headers and shell commands.
var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ErrInvalidAPIKey reports a syntactically invalid API key.
var ErrInvalidAPIKey = errors.New("crypto: invalid API key")

// GeneratedAPIKey is a freshly minted credential. The plaintext is returned
// exactly once, at creation; only the hash and display prefix are persisted.
type GeneratedAPIKey struct {
	Plaintext     Secret
	Hash          string
	DisplayPrefix string
	Mode          KeyMode
}

// GenerateAPIKey mints a key of the form "pmx_live_<base64url secret>".
func GenerateAPIKey(mode KeyMode) (GeneratedAPIKey, error) {
	if !mode.Valid() {
		return GeneratedAPIKey{}, fmt.Errorf("crypto: unknown key mode %q", mode)
	}
	buf := make([]byte, apiKeySecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return GeneratedAPIKey{}, fmt.Errorf("crypto: entropy source failed: %w", err)
	}
	plaintext := fmt.Sprintf("%s_%s_%s", apiKeyPrefix, mode, keyEncoding.EncodeToString(buf))
	return GeneratedAPIKey{
		Plaintext:     Secret(plaintext),
		Hash:          HashAPIKey(plaintext),
		DisplayPrefix: plaintext[:APIKeyDisplayPrefixLen],
		Mode:          mode,
	}, nil
}

// HashAPIKey derives the stored lookup hash for an API key.
//
// The key already carries 256 bits of entropy, so an unsalted SHA-256 is
// sufficient here and — unlike a salted hash — allows constant-time lookup by
// a unique index.
func HashAPIKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ParseAPIKeyMode extracts the mode from a key without trusting its contents
// for authorisation. It exists so a malformed key can be rejected before any
// database work happens.
func ParseAPIKeyMode(plaintext string) (KeyMode, error) {
	parts := strings.Split(plaintext, "_")
	if len(parts) != 3 || parts[0] != apiKeyPrefix {
		return "", ErrInvalidAPIKey
	}
	mode := KeyMode(parts[1])
	if !mode.Valid() {
		return "", ErrInvalidAPIKey
	}
	if len(parts[2]) < 32 {
		return "", ErrInvalidAPIKey
	}
	return mode, nil
}
