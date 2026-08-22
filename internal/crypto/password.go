package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrMismatchedPassword reports that a password does not match its hash.
var ErrMismatchedPassword = errors.New("crypto: password does not match")

// Argon2Params configures the Argon2id password hash.
type Argon2Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2Params follows current OWASP guidance for Argon2id.
func DefaultArgon2Params() Argon2Params {
	p := uint8(runtime.NumCPU())
	if p > 4 {
		p = 4
	}
	if p < 1 {
		p = 1
	}
	return Argon2Params{Memory: 64 * 1024, Iterations: 3, Parallelism: p, SaltLength: 16, KeyLength: 32}
}

// HashPassword returns a PHC-formatted Argon2id hash of password.
func HashPassword(password string) (string, error) {
	return HashPasswordWith(password, DefaultArgon2Params())
}

// HashPasswordWith hashes password using the supplied parameters.
func HashPasswordWith(password string, p Argon2Params) (string, error) {
	if password == "" {
		return "", errors.New("crypto: password must not be empty")
	}
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("crypto: entropy source failed: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks password against a PHC-formatted Argon2id hash.
func VerifyPassword(password, encoded string) error {
	p, salt, want, err := decodeArgon2Hash(encoded)
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatchedPassword
	}
	return nil
}

func decodeArgon2Hash(encoded string) (p Argon2Params, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, nil, nil, errors.New("crypto: unsupported password hash format")
	}
	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, errors.New("crypto: unsupported argon2 version")
	}
	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return p, nil, nil, errors.New("crypto: malformed password hash parameters")
	}
	if salt, err = base64.RawStdEncoding.Strict().DecodeString(parts[4]); err != nil {
		return p, nil, nil, errors.New("crypto: malformed password hash salt")
	}
	if key, err = base64.RawStdEncoding.Strict().DecodeString(parts[5]); err != nil {
		return p, nil, nil, errors.New("crypto: malformed password hash digest")
	}
	p.SaltLength, p.KeyLength = uint32(len(salt)), uint32(len(key))
	return p, salt, key, nil
}
