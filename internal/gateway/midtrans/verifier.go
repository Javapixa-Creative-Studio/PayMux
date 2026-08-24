package midtrans

import (
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
)

// SignatureInput is the set of notification fields Midtrans signs.
type SignatureInput struct {
	OrderID     string
	StatusCode  string
	GrossAmount string
}

// Signature computes Midtrans's notification signature.
//
// Midtrans defines it as:
//
//	SHA512(order_id + status_code + gross_amount + server_key)
//
// The gross amount must be used exactly as it appeared in the notification,
// "150000.00", not a reformatted "150000", because the digest covers the
// literal string Midtrans sent.
func Signature(in SignatureInput, serverKey crypto.Secret) string {
	var b strings.Builder
	b.Grow(len(in.OrderID) + len(in.StatusCode) + len(in.GrossAmount) + len(serverKey))
	b.WriteString(in.OrderID)
	b.WriteString(in.StatusCode)
	b.WriteString(in.GrossAmount)
	b.WriteString(serverKey.Reveal())

	sum := sha512.Sum512([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// VerifySignature reports whether the signature on a notification is valid.
//
// The comparison is constant time so a caller cannot recover a valid signature
// byte by byte from response timing.
func VerifySignature(in SignatureInput, provided string, serverKey crypto.Secret) bool {
	if provided == "" {
		return false
	}
	expected := Signature(in, serverKey)
	return subtle.ConstantTimeCompare(
		[]byte(strings.ToLower(strings.TrimSpace(provided))),
		[]byte(expected),
	) == 1
}
