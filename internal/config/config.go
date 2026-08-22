// Package config loads PayMux configuration from the process environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment names the deployment mode PayMux runs in.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvStaging     Environment = "staging"
	EnvProduction  Environment = "production"
)

// IsProduction reports whether hardening defaults should apply.
func (e Environment) IsProduction() bool { return e == EnvProduction }

// Config is the fully resolved configuration for the API and worker binaries.
type Config struct {
	Env      Environment
	HTTPAddr string
	BaseURL  string
	LogLevel string
	LogJSON  bool

	DatabaseURL         string
	DatabaseMaxConns    int32
	DatabaseMinConns    int32
	DatabaseConnTimeout time.Duration

	// EncryptionKey is 32 raw bytes used to seal gateway secrets at rest.
	EncryptionKey []byte

	WorkerConcurrency  int
	WorkerPollInterval time.Duration

	// AllowPrivateWebhookDestinations disables SSRF range blocking. It exists
	// for self-hosted setups whose products live on the same private network.
	AllowPrivateWebhookDestinations bool

	HTTPClientTimeout   time.Duration
	WebhookTimeout      time.Duration
	MaxRequestBodyBytes int64

	AdminSessionTTL time.Duration
	// AdminBootstrapEmail and AdminBootstrapPassword seed the first admin on
	// startup when no admin exists yet.
	AdminBootstrapEmail    string
	AdminBootstrapPassword string

	MetricsEnabled bool
	CORSOrigins    []string
}

// Load reads configuration from the environment, applying defaults and
// validating everything required to run.
func Load() (*Config, error) {
	c := &Config{
		Env:                 Environment(getStr("PAYMUX_ENV", string(EnvDevelopment))),
		HTTPAddr:            getStr("PAYMUX_HTTP_ADDR", ":8080"),
		BaseURL:             strings.TrimRight(getStr("PAYMUX_BASE_URL", "http://localhost:8080"), "/"),
		LogLevel:            getStr("PAYMUX_LOG_LEVEL", "info"),
		DatabaseURL:         getStr("DATABASE_URL", ""),
		DatabaseMaxConns:    int32(getInt("PAYMUX_DB_MAX_CONNS", 20)),
		DatabaseMinConns:    int32(getInt("PAYMUX_DB_MIN_CONNS", 2)),
		DatabaseConnTimeout: getDuration("PAYMUX_DB_CONN_TIMEOUT", 10*time.Second),
		WorkerConcurrency:   getInt("PAYMUX_WORKER_CONCURRENCY", 20),
		WorkerPollInterval:  getDuration("PAYMUX_WORKER_POLL_INTERVAL", 2*time.Second),
		HTTPClientTimeout:   getDuration("PAYMUX_HTTP_CLIENT_TIMEOUT", 30*time.Second),
		WebhookTimeout:      getDuration("PAYMUX_WEBHOOK_TIMEOUT", 15*time.Second),
		MaxRequestBodyBytes: int64(getInt("PAYMUX_MAX_REQUEST_BODY_BYTES", 1<<20)),
		AdminSessionTTL:     getDuration("PAYMUX_ADMIN_SESSION_TTL", 12*time.Hour),

		AdminBootstrapEmail:    getStr("PAYMUX_ADMIN_EMAIL", ""),
		AdminBootstrapPassword: getStr("PAYMUX_ADMIN_PASSWORD", ""),

		AllowPrivateWebhookDestinations: getBool("PAYMUX_ALLOW_PRIVATE_WEBHOOK_DESTINATIONS", false),
		MetricsEnabled:                  getBool("PAYMUX_METRICS_ENABLED", true),
		CORSOrigins:                     getList("PAYMUX_CORS_ORIGINS", []string{"http://localhost:5173"}),
	}
	c.LogJSON = getBool("PAYMUX_LOG_JSON", c.Env.IsProduction())

	var errs []error
	switch c.Env {
	case EnvDevelopment, EnvStaging, EnvProduction:
	default:
		errs = append(errs, fmt.Errorf("PAYMUX_ENV: %q is not one of development, staging, production", c.Env))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	key, err := loadEncryptionKey()
	if err != nil {
		errs = append(errs, err)
	}
	c.EncryptionKey = key

	if c.WorkerConcurrency < 1 {
		errs = append(errs, errors.New("PAYMUX_WORKER_CONCURRENCY must be at least 1"))
	}
	if c.DatabaseMaxConns < c.DatabaseMinConns {
		errs = append(errs, errors.New("PAYMUX_DB_MAX_CONNS must be >= PAYMUX_DB_MIN_CONNS"))
	}
	if (c.AdminBootstrapEmail == "") != (c.AdminBootstrapPassword == "") {
		errs = append(errs, errors.New("PAYMUX_ADMIN_EMAIL and PAYMUX_ADMIN_PASSWORD must be set together"))
	}
	if c.Env.IsProduction() && c.AllowPrivateWebhookDestinations {
		// Permitted, but the operator should have made the choice knowingly.
		// Surfaced as a startup warning by the caller rather than an error.
		_ = c
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

// loadEncryptionKey decodes PAYMUX_ENCRYPTION_KEY, which must be 32 bytes
// expressed as 64 hex characters or standard base64.
func loadEncryptionKey() ([]byte, error) {
	raw := os.Getenv("PAYMUX_ENCRYPTION_KEY")
	if raw == "" {
		return nil, errors.New("PAYMUX_ENCRYPTION_KEY is required (32 bytes, hex or base64 encoded)")
	}
	key, err := decodeKey(raw)
	if err != nil {
		return nil, fmt.Errorf("PAYMUX_ENCRYPTION_KEY: %w", err)
	}
	return key, nil
}

func getStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func getList(key string, def []string) []string {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
