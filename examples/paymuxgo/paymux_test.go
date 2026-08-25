package paymuxgo

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
)

// The example's verification has to agree with PayMux's signer exactly. This
// test signs with PayMux's own implementation and verifies with the example's,
// so the two cannot drift apart, which is the failure mode that would leave
// every integrator's webhooks silently rejected.
func TestExampleVerificationAgreesWithPayMux(t *testing.T) {
	const secret = "whsec_example_secret_value"
	body := []byte(`{"id":"evt_1","type":"payment.paid","status":"PAID","amount":150000}`)
	deliveryID := "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	timestamp := time.Now().Unix()

	signed := crypto.SignWebhook(crypto.Secret(secret), timestamp, deliveryID, body)

	if got := Sign(secret, timestamp, deliveryID, body); got != signed {
		t.Fatalf("the example signs differently from PayMux:\n example: %s\n paymux:  %s", got, signed)
	}

	event, err := VerifyWebhook(secret, headers(timestamp, deliveryID, signed), body)
	if err != nil {
		t.Fatalf("VerifyWebhook rejected a genuine delivery: %v", err)
	}
	if event.Type != "payment.paid" || event.Amount != 150000 {
		t.Fatalf("decoded event = %+v", event)
	}
}

func TestVerifyRejectsTamperingAndReplay(t *testing.T) {
	const secret = "whsec_example_secret_value"
	body := []byte(`{"id":"evt_1","type":"payment.paid","amount":150000}`)
	deliveryID := "dlv_1"
	now := time.Now().Unix()
	signature := Sign(secret, now, deliveryID, body)

	t.Run("altered body", func(t *testing.T) {
		altered := []byte(`{"id":"evt_1","type":"payment.paid","amount":15000000}`)
		if _, err := VerifyWebhook(secret, headers(now, deliveryID, signature), altered); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("an altered amount was accepted: %v", err)
		}
	})

	t.Run("wrong secret", func(t *testing.T) {
		if _, err := VerifyWebhook("whsec_someone_elses", headers(now, deliveryID, signature), body); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("another application's secret verified: %v", err)
		}
	})

	t.Run("replayed later", func(t *testing.T) {
		// The signature stays valid forever, so the timestamp is what bounds
		// a capture-and-replay.
		old := time.Now().Add(-2 * Tolerance).Unix()
		stale := Sign(secret, old, deliveryID, body)
		if _, err := VerifyWebhook(secret, headers(old, deliveryID, stale), body); !errors.Is(err, ErrStale) {
			t.Fatalf("a replayed delivery was accepted: %v", err)
		}
	})

	t.Run("different delivery", func(t *testing.T) {
		if _, err := VerifyWebhook(secret, headers(now, "dlv_other", signature), body); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("the body verified under a different delivery: %v", err)
		}
	})
}

func headers(timestamp int64, deliveryID, signature string) http.Header {
	h := http.Header{}
	h.Set(HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	h.Set(HeaderDeliveryID, deliveryID)
	h.Set(HeaderSignature, signature)
	h.Set(HeaderEventType, "payment.paid")
	return h
}
