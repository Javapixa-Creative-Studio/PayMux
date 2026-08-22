// Command paymux-api serves PayMux's HTTP API: the application-facing
// payment API, the dashboard admin API and gateway notification endpoints.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/api"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/app"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/config"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/logging"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// The container image has no shell or curl, so the binary can probe
	// itself for the container health check.
	healthcheck := flag.Bool("healthcheck", false, "probe the local /health endpoint and exit")
	flag.Parse()

	if *healthcheck {
		os.Exit(probeHealth())
	}
	if err := run(); err != nil {
		// The logger may not exist yet, so report to stderr as well.
		fmt.Fprintln(os.Stderr, "paymux-api: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	api.Version = version
	logger := logging.New(logging.Options{Level: cfg.LogLevel, JSON: cfg.LogJSON}).
		With("service", "paymux-api", "version", version, "env", string(cfg.Env))
	slog.SetDefault(logger)

	// Signals cancel the root context, which unwinds startup and serving alike.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := storage.Connect(ctx, storage.Options{
		URL:         cfg.DatabaseURL,
		MaxConns:    cfg.DatabaseMaxConns,
		MinConns:    cfg.DatabaseMinConns,
		ConnTimeout: cfg.DatabaseConnTimeout,
	})
	if err != nil {
		return err
	}
	defer db.Close()
	logger.Info("connected to database")

	container, err := app.Build(cfg, db, logger)
	if err != nil {
		return err
	}
	if err := container.Bootstrap(ctx); err != nil {
		return err
	}

	if cfg.AllowPrivateWebhookDestinations && cfg.Env.IsProduction() {
		logger.Warn("webhook destinations may target private networks; " +
			"this disables SSRF protection and should only be used on a trusted network")
	}

	server := api.New(api.Deps{
		Config:           cfg,
		DB:               db,
		Logger:           logger,
		Auth:             container.Auth,
		AuthMiddleware:   container.AuthMiddleware,
		Applications:     container.Applications,
		GatewayAccounts:  container.GatewayAccounts,
		Gateways:         container.Gateways,
		Auditor:          container.Auditor,
		Payments:         container.Payments,
		PaymentRepo:      container.PaymentRepo,
		Idempotency:      container.Idempotency,
		Events:           container.Events,
		Deliveries:       container.Deliveries,
		Notifications:    container.Notifications,
		NotificationRepo: container.NotificationRepo,
		Subscriptions:    container.Subscriptions,
		Metrics:          container.Metrics,
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received; draining connections")
	}

	// Stop accepting new work, then let in-flight requests finish (PRD §69).
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

// probeHealth performs the container health check and returns an exit code.
func probeHealth() int {
	addr := os.Getenv("PAYMUX_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}

	client := &http.Client{Timeout: 3 * time.Second}
	// The address is this process's own listener, taken from the same
	// configuration it bound to; there is no external input here.
	resp, err := client.Get("http://" + addr + "/health") //nolint:gosec,noctx // self-probe on a fixed local address
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: "+err.Error())
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: HTTP %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
