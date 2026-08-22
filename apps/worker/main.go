// Command paymux-worker delivers PayMux events to application webhook
// destinations and performs PayMux's periodic housekeeping.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/anggapixa/paymux/internal/app"
	"github.com/anggapixa/paymux/internal/config"
	"github.com/anggapixa/paymux/internal/delivery"
	"github.com/anggapixa/paymux/internal/logging"
	"github.com/anggapixa/paymux/internal/storage"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

// housekeepingInterval is how often expired sessions and idempotency keys are
// pruned. These are cheap deletes, so the interval is generous.
const housekeepingInterval = time.Hour

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "paymux-worker: "+err.Error())
		os.Exit(1)
	}
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
	// The worker waits for the schema rather than applying it: the API owns
	// migrations, and two processes racing to migrate helps nobody. The
	// advisory lock in Migrate makes this safe either way.
	if _, err := db.Migrate(ctx); err != nil {
		return err
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

	var wg sync.WaitGroup
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
