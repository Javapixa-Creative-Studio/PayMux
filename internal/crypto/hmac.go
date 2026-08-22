package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// WebhookSecretPrefix marks secrets used to sign outbound PayMux webhooks.
const WebhookSecretPrefix = "whsec_"

// GenerateWebhookSecret mints a signing secret for an application destination.
func GenerateWebhookSecret() (Secret, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("crypto: entropy source failed: %w", err)
	}
	return Secret(WebhookSecretPrefix + base64.RawURLEncoding.EncodeToString(buf)), nil
}

// SigningPayload builds the canonical string that PayMux signs for an
// outbound webhook:
//
//	<unix timestamp>.<delivery id>.<raw request body>
//
// Including the timestamp lets receivers reject replayed deliveries, and
// including the delivery id binds a signature to a single delivery attempt
// series so a body cannot be replayed under a different delivery.
func SigningPayload(timestamp int64, deliveryID string, body []byte) []byte {
	head := fmt.Sprintf("%d.%s.", timestamp, deliveryID)
	out := make([]byte, 0, len(head)+len(body))
	out = append(out, head...)
	out = append(out, body...)
	return out
}

// SignWebhook returns the header value for a signed outbound webhook:
// "v1=<hex hmac-sha256>". The scheme is versioned so the algorithm can change
// without breaking receivers that pin a version.
func SignWebhook(secret Secret, timestamp int64, deliveryID string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret.Reveal()))
	mac.Write(SigningPayload(timestamp, deliveryID, body))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhookSignature checks a signature header produced by SignWebhook.
// It is exported for use by receiving applications and by PayMux's own tests.
func VerifyWebhookSignature(secret Secret, timestamp int64, deliveryID string, body []byte, header string) bool {
	want := SignWebhook(secret, timestamp, deliveryID, body)
	// Compare every advertised signature so key rotation can send several.
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(want)) == 1 {
			return true
		}
	}
	return false
}
