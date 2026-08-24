// Command paymux-worker delivers PayMux events to application webhook
// destinations and performs PayMux's periodic housekeeping.
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
	"sync"
	"syscall"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/app"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/config"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/delivery"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/logging"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

// housekeepingInterval is how often expired sessions and idempotency keys are
// pruned. These are cheap deletes, so the interval is generous.
const housekeepingInterval = time.Hour

func main() {
	// The container image has no shell or curl, so the binary probes itself
	// for the container health check.
	healthcheck := flag.Bool("healthcheck", false, "probe the local metrics endpoint and exit")
	flag.Parse()

	if *healthcheck {
		os.Exit(probeHealth())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "paymux-worker: "+err.Error())
		os.Exit(1)
	}
}

// probeHealth performs the container health check and returns an exit code.
//
// The worker's only listener is the metrics server, so that is what proves the
// process is alive and serving rather than wedged.
func probeHealth() int {
	addr := os.Getenv("PAYMUX_METRICS_ADDR")
	if addr == "" {
		addr = ":9090"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}

	client := &http.Client{Timeout: 3 * time.Second}
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

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	logger := logging.New(logging.Options{Level: cfg.LogLevel, JSON: cfg.LogJSON}).
		With("service", "paymux-worker", "version", version, "env", string(cfg.Env))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := storage.Connect(ctx, storage.Options{
		URL: cfg.DatabaseURL,
		// The worker's pool is sized to its concurrency: every in-flight
		// delivery needs a connection to record its outcome.
		MaxConns:    maxConns(cfg.WorkerConcurrency, cfg.DatabaseMaxConns),
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

	var wg sync.WaitGroup
	// The worker waits for the schema rather than applying it: the API owns
	// migrations, and two processes racing to migrate helps nobody. The
	// advisory lock in Migrate makes this safe either way.
	if _, err := db.Migrate(ctx); err != nil {
		return err
	}

	if cfg.MetricsEnabled {
		startMetricsServer(ctx, cfg.MetricsAddr, container, logger, &wg)
	}

	worker := delivery.NewWorker(
		container.Deliveries,
		container.Events,
		container.AppRepository,
		container.Sender,
		delivery.WorkerOptions{
			Concurrency:  cfg.WorkerConcurrency,
			PollInterval: cfg.WorkerPollInterval,
			Logger:       logger,
		},
	)

	worker.SetMetrics(container.Metrics)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := worker.Run(ctx); err != nil {
			logger.Error("delivery worker stopped with an error", "error", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runHousekeeping(ctx, container, logger)
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received; finishing in-flight work")
	wg.Wait()
	logger.Info("shutdown complete")
	return nil
}

// startMetricsServer exposes the worker's metrics.
//
// The worker serves no API of its own, so without this its counters would be
// invisible — which is exactly backwards, since delivery health is the thing
// an operator most wants to watch.
func startMetricsServer(ctx context.Context, addr string, container *app.Container, logger *slog.Logger, wg *sync.WaitGroup) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", container.Metrics.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("metrics server listening", "addr", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server stopped", "error", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
}

// runHousekeeping periodically prunes records that have outlived their use.
func runHousekeeping(ctx context.Context, container *app.Container, logger *slog.Logger) {
	ticker := time.NewTicker(housekeepingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if pruned, err := container.Auth.PruneSessions(ctx); err != nil {
				logger.Error("could not prune expired sessions", "error", err)
			} else if pruned > 0 {
				logger.Info("pruned expired dashboard sessions", "count", pruned)
			}

			if pruned, err := container.Idempotency.Prune(ctx); err != nil {
				logger.Error("could not prune expired idempotency keys", "error", err)
			} else if pruned > 0 {
				logger.Info("pruned expired idempotency keys", "count", pruned)
			}
		}
	}
}

// maxConns keeps the pool large enough for the worker's concurrency, with a
// little headroom for housekeeping.
func maxConns(concurrency int, configured int32) int32 {
	needed := int32(concurrency) + 2
	if configured > needed {
		return configured
	}
	return needed
}
