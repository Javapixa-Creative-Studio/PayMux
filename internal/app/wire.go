// Package app assembles PayMux's services from configuration. Both the API
// and the worker build the same object graph here, so they always agree about
// how the domain is constructed.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/application"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/audit"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/auth"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/config"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/delivery"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/event"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway/midtrans"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/metrics"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/netsafe"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/notification"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/payment"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/subscription"
)

// Container holds the constructed services.
type Container struct {
	Config *config.Config
	Logger *slog.Logger
	DB     *storage.DB

	Sealer  *crypto.Sealer
	Guard   *netsafe.Guard
	Metrics *metrics.Metrics

	Auth            *auth.Service
	AuthMiddleware  *auth.Middleware
	Applications    *application.Service
	AppRepository   *application.Repository
	GatewayAccounts *gateway.Repository
	Gateways        *gateway.Registry
	Auditor         *audit.Recorder

	Payments         *payment.Service
	PaymentRepo      *payment.Repository
	Idempotency      *payment.IdempotencyStore
	Events           *event.Repository
	Deliveries       *delivery.Repository
	Publisher        *delivery.Publisher
	Sender           *delivery.Sender
	Notifications    *notification.Processor
	NotificationRepo *notification.Repository
	Subscriptions    *subscription.Service

	// GatewayHTTPClient is shared by every adapter (PRD §74).
	GatewayHTTPClient *http.Client
}

// Build constructs the service graph. It does not open the database; callers
// pass one in so tests can supply their own.
func Build(cfg *config.Config, db *storage.DB, logger *slog.Logger) (*Container, error) {
	sealer, err := crypto.NewSealer(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("app: build sealer: %w", err)
	}
	guard := netsafe.NewGuard(cfg.AllowPrivateWebhookDestinations)

	appRepo := application.NewRepository(db, sealer)
	applications := application.NewService(appRepo, guard)

	authRepo := auth.NewRepository(db)
	authService := auth.NewService(authRepo, cfg.AdminSessionTTL, logger)
	authMiddleware := auth.NewMiddleware(authService, applications, cfg.Env.IsProduction())

	// A nil collector disables instrumentation without changing any call site,
	// so metrics can be turned off without threading a flag through the domain.
	var collector *metrics.Metrics
	if cfg.MetricsEnabled {
		collector = metrics.New()
	}

	gatewayClient := gateway.NewHTTPClient(cfg.HTTPClientTimeout)
	registry := gateway.NewRegistry(gatewayClient)
	registry.Register(midtrans.Name, func(acc *gateway.Account, client *http.Client) (gateway.Gateway, error) {
		adapter, err := midtrans.NewAdapter(acc, client)
		if err != nil {
			return nil, err
		}
		if collector != nil {
			adapter.(*midtrans.Adapter).SetMetrics(collector)
		}
		return adapter, nil
	})

	gatewayAccounts := gateway.NewRepository(db, sealer)
	eventRepo := event.NewRepository(db)
	deliveryRepo := delivery.NewRepository(db)
	publisher := delivery.NewPublisher(db, eventRepo, deliveryRepo, appRepo, logger)

	paymentRepo := payment.NewRepository(db)
	payments := payment.NewService(db, paymentRepo, gatewayAccounts, registry, publisher, logger)

	notificationRepo := notification.NewRepository(db)
	processor := notification.NewProcessor(db, notificationRepo, paymentRepo,
		gatewayAccounts, registry, publisher, logger)

	payments.SetMetrics(collector)
	processor.SetMetrics(collector)

	subscriptions := subscription.NewService(
		subscription.NewRepository(db), gatewayAccounts, registry, publisher, logger)

	sender := delivery.NewSender(guard, cfg.WebhookTimeout, "PayMux-Webhook/1.0")

	return &Container{
		Config:            cfg,
		Logger:            logger,
		DB:                db,
		Sealer:            sealer,
		Guard:             guard,
		Metrics:           collector,
		Auth:              authService,
		AuthMiddleware:    authMiddleware,
		Applications:      applications,
		AppRepository:     appRepo,
		GatewayAccounts:   gatewayAccounts,
		Gateways:          registry,
		Auditor:           audit.NewRecorder(db, logger),
		Payments:          payments,
		PaymentRepo:       paymentRepo,
		Idempotency:       payment.NewIdempotencyStore(db),
		Events:            eventRepo,
		Deliveries:        deliveryRepo,
		Publisher:         publisher,
		Sender:            sender,
		Notifications:     processor,
		NotificationRepo:  notificationRepo,
		Subscriptions:     subscriptions,
		GatewayHTTPClient: gatewayClient,
	}, nil
}

// Bootstrap performs the one-time startup work: applying migrations and
// seeding the first administrator when one is configured.
func (c *Container) Bootstrap(ctx context.Context) error {
	applied, err := c.DB.Migrate(ctx)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		c.Logger.Info("applied database migrations", "count", len(applied), "migrations", applied)
	}

	admin, err := c.Auth.EnsureBootstrapAdmin(ctx,
		c.Config.AdminBootstrapEmail, c.Config.AdminBootstrapPassword)
	if err != nil {
		return err
	}
	if admin != nil {
		c.Logger.Info("created bootstrap administrator",
			"admin_id", admin.ID,
			"email", admin.Email,
			"hint", "remove PAYMUX_ADMIN_EMAIL and PAYMUX_ADMIN_PASSWORD from the environment")
	}
	return nil
}
