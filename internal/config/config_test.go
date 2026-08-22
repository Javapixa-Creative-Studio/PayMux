package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

const testKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setMinimal(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/paymux")
	t.Setenv("PAYMUX_ENCRYPTION_KEY", testKeyHex)
}

func TestLoadDefaults(t *testing.T) {
	setMinimal(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.Env != EnvDevelopment {
		t.Errorf("Env = %q, want development", c.Env)
	}
	if c.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q", c.HTTPAddr)
	}
	if c.WorkerConcurrency != 20 {
		t.Errorf("WorkerConcurrency = %d, want 20", c.WorkerConcurrency)
	}
	if len(c.EncryptionKey) != KeySize {
		t.Errorf("EncryptionKey length = %d, want %d", len(c.EncryptionKey), KeySize)
	}
	if c.AllowPrivateWebhookDestinations {
		t.Error("private webhook destinations should be blocked by default")
	}
}

func TestLoadRequiresDatabaseURLAndKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("PAYMUX_ENCRYPTION_KEY", "")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() = nil error, want failure")
	}
	for _, want := range []string{"DATABASE_URL", "PAYMUX_ENCRYPTION_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestEncryptionKeyAcceptsBase64AndRejectsShortKeys(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/paymux")
	t.Setenv("PAYMUX_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, KeySize)))
	if _, err := Load(); err != nil {
		t.Fatalf("base64 key rejected: %v", err)
	}
	t.Setenv("PAYMUX_ENCRYPTION_KEY", "abcd")
	if _, err := Load(); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestBootstrapAdminRequiresBothFields(t *testing.T) {
	setMinimal(t)
	t.Setenv("PAYMUX_ADMIN_EMAIL", "admin@example.com")
	if _, err := Load(); err == nil {
		t.Fatal("admin email without password was accepted")
	}
	t.Setenv("PAYMUX_ADMIN_PASSWORD", "correct horse battery staple")
	if _, err := Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
}

func TestProductionDefaultsToJSONLogs(t *testing.T) {
	setMinimal(t)
	t.Setenv("PAYMUX_ENV", "production")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !c.LogJSON {
		t.Error("production should default to JSON logging")
	}
}

func TestInvalidEnvIsRejected(t *testing.T) {
	setMinimal(t)
	t.Setenv("PAYMUX_ENV", "prod")
	if _, err := Load(); err == nil {
		t.Fatal("invalid PAYMUX_ENV accepted")
	}
}
