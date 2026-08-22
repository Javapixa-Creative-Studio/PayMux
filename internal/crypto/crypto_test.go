package crypto

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func testSealer(t *testing.T) *Sealer {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	s, err := NewSealer(key)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

func TestSealOpenRoundTrip(t *testing.T) {
	s := testSealer(t)
	sealed, err := s.SealString("SB-Mid-server-abc123", "gateway_account:gwa_1:server_key")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(sealed, "server-abc123") {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := s.OpenString(sealed, "gateway_account:gwa_1:server_key")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != "SB-Mid-server-abc123" {
		t.Fatalf("Open = %q", got)
	}
}

func TestOpenRejectsWrongContext(t *testing.T) {
	s := testSealer(t)
	sealed, _ := s.SealString("secret", "gateway_account:gwa_1:server_key")
	if _, err := s.Open(sealed, "gateway_account:gwa_2:server_key"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("Open with wrong context = %v, want ErrDecrypt", err)
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	s := testSealer(t)
	sealed, _ := s.SealString("secret", "ctx")
	tampered := sealed[:len(sealed)-1] + string(sealed[len(sealed)-1]^1)
	if _, err := s.Open(tampered, "ctx"); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, err := s.Open("garbage", "ctx"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("garbage = %v, want ErrDecrypt", err)
	}
}

func TestSealIsNondeterministic(t *testing.T) {
	s := testSealer(t)
	a, _ := s.SealString("same", "ctx")
	b, _ := s.SealString("same", "ctx")
	if a == b {
		t.Fatal("Seal produced identical ciphertexts; nonce is not random")
	}
}

func TestNewSealerRejectsWrongKeySize(t *testing.T) {
	if _, err := NewSealer(make([]byte, 16)); err == nil {
		t.Fatal("16-byte key accepted")
	}
}

func TestSecretNeverLeaks(t *testing.T) {
	s := Secret("SB-Mid-server-topsecret")
	if got := fmt.Sprintf("%s %v %q", s, s, s); strings.Contains(got, "topsecret") {
		t.Fatalf("Secret leaked through formatting: %s", got)
	}
	if got := fmt.Sprintf("%#v", s); strings.Contains(got, "topsecret") {
		t.Fatalf("Secret leaked through %%#v: %s", got)
	}
	b, err := json.Marshal(struct{ Key Secret }{s})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "topsecret") {
		t.Fatalf("Secret leaked through JSON: %s", b)
	}
	if s.Reveal() != "SB-Mid-server-topsecret" {
		t.Fatal("Reveal did not return the underlying value")
	}
}

func TestPasswordHashing(t *testing.T) {
	p := Argon2Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	hash, err := HashPasswordWith("correct horse battery staple", p)
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if err := VerifyPassword("correct horse battery staple", hash); err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if err := VerifyPassword("wrong password", hash); !errors.Is(err, ErrMismatchedPassword) {
		t.Fatalf("VerifyPassword(wrong) = %v, want ErrMismatchedPassword", err)
	}
}

func TestPasswordHashSaltsDiffer(t *testing.T) {
	p := Argon2Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	a, _ := HashPasswordWith("same", p)
	b, _ := HashPasswordWith("same", p)
	if a == b {
		t.Fatal("identical hashes for the same password; salt is not random")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, h := range []string{"", "not-a-hash", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=99$m=1,t=1,p=1$c2FsdA$a2V5"} {
		if err := VerifyPassword("x", h); err == nil {
			t.Errorf("VerifyPassword accepted malformed hash %q", h)
		}
	}
}

func TestGenerateAPIKey(t *testing.T) {
	k, err := GenerateAPIKey(KeyModeLive)
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if !strings.HasPrefix(k.Plaintext.Reveal(), "pmx_live_") {
		t.Fatalf("unexpected key format: %s", k.Plaintext.Reveal())
	}
	if k.DisplayPrefix != k.Plaintext.Reveal()[:APIKeyDisplayPrefixLen] {
		t.Fatal("display prefix does not match the key")
	}
	if k.Hash != HashAPIKey(k.Plaintext.Reveal()) {
		t.Fatal("stored hash does not match HashAPIKey")
	}
	if len(k.Hash) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(k.Hash))
	}
	if _, err := GenerateAPIKey(KeyMode("staging")); err == nil {
		t.Fatal("unknown key mode accepted")
	}
}

func TestParseAPIKeyMode(t *testing.T) {
	k, _ := GenerateAPIKey(KeyModeTest)
	mode, err := ParseAPIKeyMode(k.Plaintext.Reveal())
	if err != nil || mode != KeyModeTest {
		t.Fatalf("ParseAPIKeyMode = %q, %v", mode, err)
	}
	for _, bad := range []string{"", "pmx_live", "xxx_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "pmx_prod_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "pmx_live_short"} {
		if _, err := ParseAPIKeyMode(bad); !errors.Is(err, ErrInvalidAPIKey) {
			t.Errorf("ParseAPIKeyMode(%q) = %v, want ErrInvalidAPIKey", bad, err)
		}
	}
}

func TestWebhookSigning(t *testing.T) {
	secret, err := GenerateWebhookSecret()
	if err != nil {
		t.Fatalf("GenerateWebhookSecret: %v", err)
	}
	if !strings.HasPrefix(secret.Reveal(), WebhookSecretPrefix) {
		t.Fatalf("unexpected secret format: %s", secret.Reveal())
	}
	body := []byte(`{"type":"payment.paid"}`)
	sig := SignWebhook(secret, 1700000000, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV", body)
	if !strings.HasPrefix(sig, "v1=") {
		t.Fatalf("unexpected signature format: %s", sig)
	}
	if !VerifyWebhookSignature(secret, 1700000000, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV", body, sig) {
		t.Fatal("valid signature rejected")
	}
	// Any change to timestamp, delivery, body or secret must invalidate it.
	if VerifyWebhookSignature(secret, 1700000001, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV", body, sig) {
		t.Error("signature survived a timestamp change")
	}
	if VerifyWebhookSignature(secret, 1700000000, "dlv_other", body, sig) {
		t.Error("signature survived a delivery id change")
	}
	if VerifyWebhookSignature(secret, 1700000000, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV", []byte(`{"type":"payment.failed"}`), sig) {
		t.Error("signature survived a body change")
	}
	other, _ := GenerateWebhookSecret()
	if VerifyWebhookSignature(other, 1700000000, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV", body, sig) {
		t.Error("signature verified under the wrong secret")
	}
}

func TestVerifyWebhookSignatureAcceptsMultipleAdvertisedSignatures(t *testing.T) {
	secret, _ := GenerateWebhookSecret()
	body := []byte("{}")
	sig := SignWebhook(secret, 1700000000, "dlv_1", body)
	header := "v1=deadbeef, " + sig
	if !VerifyWebhookSignature(secret, 1700000000, "dlv_1", body, header) {
		t.Fatal("signature list with a valid entry was rejected")
	}
}
