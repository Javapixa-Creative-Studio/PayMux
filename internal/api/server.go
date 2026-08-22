package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/anggapixa/paymux/internal/application"
	"github.com/anggapixa/paymux/internal/audit"
	"github.com/anggapixa/paymux/internal/auth"
	"github.com/anggapixa/paymux/internal/config"
	"github.com/anggapixa/paymux/internal/delivery"
	"github.com/anggapixa/paymux/internal/event"
	"github.com/anggapixa/paymux/internal/gateway"
	"github.com/anggapixa/paymux/internal/httpx"
	"github.com/anggapixa/paymux/internal/logging"
	"github.com/anggapixa/paymux/internal/notification"
	"github.com/anggapixa/paymux/internal/payment"
	"github.com/anggapixa/paymux/internal/storage"
	"github.com/anggapixa/paymux/internal/subscription"
)

// Server holds the dependencies every handler needs and builds the router.
type Server struct {
	cfg    *config.Config
	db     *storage.DB
	logger *slog.Logger

	authService     *auth.Service
	authMiddleware  *auth.Middleware
	applications    *application.Service
	gatewayAccounts *gateway.Repository
	gateways        *gateway.Registry
	auditor         *audit.Recorder

	payments         *payment.Service
	paymentRepo      *payment.Repository
	idempotency      *payment.IdempotencyStore
	events           *event.Repository
	deliveries       *delivery.Repository
	notifications    *notification.Processor
	notificationRepo *notification.Repository
	subscriptions    *subscription.Service

	router chi.Router
}

// Deps are the collaborators a Server is constructed from.
type Deps struct {
	Config          *config.Config
	DB              *storage.DB
	Logger          *slog.Logger
	Auth            *auth.Service
	AuthMiddleware  *auth.Middleware
	Applications    *application.Service
	GatewayAccounts *gateway.Repository
	Gateways        *gateway.Registry
	Auditor         *audit.Recorder

	Payments         *payment.Service
	PaymentRepo      *payment.Repository
	Idempotency      *payment.IdempotencyStore
	Events           *event.Repository
	Deliveries       *delivery.Repository
	Notifications    *notification.Processor
	NotificationRepo *notification.Repository
	Subscriptions    *subscription.Service
}

// New builds a Server with its routes mounted.
func New(deps Deps) *Server {
	s := &Server{
		cfg:              deps.Config,
		db:               deps.DB,
		logger:           deps.Logger,
		authService:      deps.Auth,
		authMiddleware:   deps.AuthMiddleware,
		applications:     deps.Applications,
		gatewayAccounts:  deps.GatewayAccounts,
		gateways:         deps.Gateways,
		auditor:          deps.Auditor,
		payments:         deps.Payments,
		paymentRepo:      deps.PaymentRepo,
		idempotency:      deps.Idempotency,
		events:           deps.Events,
		deliveries:       deps.Deliveries,
		notifications:    deps.Notifications,
		notificationRepo: deps.NotificationRepo,
		subscriptions:    deps.Subscriptions,
	}
	s.router = s.routes()
	return s
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// routes mounts every route group.
//
// Middleware order matters: the request id and logger come first so everything
// downstream — including the panic recoverer — can attribute what it reports
// to a specific request.
func (s *Server) routes() chi.Router {
	r := chi.NewRouter()

	r.Use(injectLogger(s.logger))
	r.Use(httpx.RequestID)
	r.Use(httpx.RequestLogger)
	r.Use(httpx.Recoverer)
	r.Use(httpx.SecureHeaders)
	r.Use(httpx.CORS(s.cfg.CORSOrigins))
	r.Use(httpx.LimitBody(s.cfg.MaxRequestBodyBytes))

	// Liveness and readiness are unauthenticated and dependency-light.
	r.Get("/health", s.handleHealth)
	r.Get("/ready", s.handleReady)

	// Gateway callbacks authenticate themselves with the gateway's own
	// signature, so they sit outside the application API's auth (PRD §77).
	r.Post("/webhooks/midtrans", s.handleMidtransNotification)

	r.Route("/api/v1", s.applicationRoutes)
	r.Route("/admin/api", s.adminRoutes)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, httpx.ErrNotFound(httpx.CodeNotFound, "The requested resource was not found."))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, httpx.NewError(http.StatusMethodNotAllowed, httpx.CodeInvalidRequest,
			"That method is not allowed on this resource."))
	})

	return r
}

// applicationRoutes mounts the API applications call with an API key.
func (s *Server) applicationRoutes(r chi.Router) {
	r.Use(s.authMiddleware.RequireApplication)

	r.Route("/payments", func(r chi.Router) {
		r.Post("/", s.handleCreatePayment)
		r.Get("/", s.handleListPayments)

		r.Route("/{paymentID}", func(r chi.Router) {
			r.Get("/", s.handleGetPayment)
			r.Post("/sync", s.handleSyncPayment)
			r.Post("/cancel", s.handleCancelPayment)
			r.Post("/expire", s.handleExpirePayment)
			r.Post("/snap/cancel", s.handleCancelSnapSession)

			r.Post("/refunds", s.handleCreateRefund)
			r.Get("/refunds", s.handleListRefunds)
		})
	})

	r.Route("/subscriptions", func(r chi.Router) {
		r.Post("/", s.handleCreateSubscription)
		r.Get("/", s.handleListSubscriptions)

		r.Route("/{subscriptionID}", func(r chi.Router) {
			r.Get("/", s.handleGetSubscription)
			r.Patch("/", s.handleUpdateSubscription)
			r.Post("/sync", s.handleSyncSubscription)
			r.Post("/enable", s.handleEnableSubscription)
			r.Post("/disable", s.handleDisableSubscription)
			r.Post("/cancel", s.handleCancelSubscription)
		})
	})

	r.Get("/events", s.handleListApplicationEvents)
	r.Get("/deliveries", s.handleListApplicationDeliveries)
	r.Post("/deliveries/{deliveryID}/retry", s.handleRetryDelivery)

	// Applications discover what the configured gateway supports rather than
	// assuming a capability is available (PRD §85).
	r.Get("/gateway/capabilities", s.handleCapabilities)
}

// adminRoutes mounts the dashboard API.
func (s *Server) adminRoutes(r chi.Router) {
	// Sign-in is rate limited by address: it is the one unauthenticated
	// endpoint that performs an expensive password hash.
	loginLimiter := httpx.NewRateLimiter(0.5, 10)

	r.Group(func(r chi.Router) {
		r.Use(loginLimiter.Middleware(httpx.ByClientIP))
		r.Post("/auth/login", s.handleLogin)
	})
	r.Post("/auth/logout", s.handleLogout)

	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware.RequireAdmin)

		r.Get("/auth/me", s.handleCurrentAdmin)
		r.Post("/auth/password", s.handleChangePassword)

		r.Get("/overview", s.handleAdminOverview)

		r.Route("/admins", func(r chi.Router) {
			r.Get("/", s.handleListAdmins)
			r.Post("/", s.handleCreateAdmin)
		})

		r.Route("/applications", func(r chi.Router) {
			r.Get("/", s.handleListApplications)
			r.Post("/", s.handleCreateApplication)

			r.Route("/{applicationID}", func(r chi.Router) {
				r.Get("/", s.handleGetApplication)
				r.Patch("/", s.handleUpdateApplication)

				r.Get("/keys", s.handleListAPIKeys)
				r.Post("/keys", s.handleCreateAPIKey)
				r.Post("/keys/{keyID}/revoke", s.handleRevokeAPIKey)

				r.Get("/destinations", s.handleListDestinations)
				r.Post("/destinations", s.handleCreateDestination)
				r.Patch("/destinations/{destinationID}", s.handleUpdateDestination)
				r.Delete("/destinations/{destinationID}", s.handleDeleteDestination)
				r.Post("/destinations/{destinationID}/rotate-secret", s.handleRotateDestinationSecret)
			})
		})

		r.Route("/payments", func(r chi.Router) {
			r.Get("/", s.handleAdminListPayments)
			r.Get("/{paymentID}", s.handleAdminGetPayment)
			r.Post("/{paymentID}/sync", s.handleAdminSyncPayment)
			r.Post("/{paymentID}/cancel", s.handleAdminCancelPayment)
			r.Post("/{paymentID}/expire", s.handleAdminExpirePayment)
			r.Post("/{paymentID}/refunds", s.handleCreateRefund)
			r.Get("/{paymentID}/refunds", s.handleListRefunds)
		})

		r.Get("/events", s.handleAdminListEvents)
		r.Get("/events/{eventID}", s.handleAdminGetEvent)

		r.Get("/deliveries", s.handleAdminListDeliveries)
		r.Get("/deliveries/{deliveryID}", s.handleAdminGetDelivery)
		r.Post("/deliveries/{deliveryID}/retry", s.handleRetryDelivery)

		r.Route("/subscriptions", func(r chi.Router) {
			r.Get("/", s.handleListSubscriptions)
			r.Get("/{subscriptionID}", s.handleGetSubscription)
			r.Post("/{subscriptionID}/sync", s.handleSyncSubscription)
			r.Post("/{subscriptionID}/enable", s.handleEnableSubscription)
			r.Post("/{subscriptionID}/disable", s.handleDisableSubscription)
			r.Post("/{subscriptionID}/cancel", s.handleCancelSubscription)
		})

		r.Get("/gateway-events", s.handleAdminListGatewayEvents)

		r.Route("/gateways", func(r chi.Router) {
			r.Get("/", s.handleListGateways)

			r.Route("/accounts", func(r chi.Router) {
				r.Get("/", s.handleListGatewayAccounts)
				r.Post("/", s.handleCreateGatewayAccount)
				r.Get("/{accountID}", s.handleGetGatewayAccount)
				r.Patch("/{accountID}", s.handleUpdateGatewayAccount)
				r.Delete("/{accountID}", s.handleDeleteGatewayAccount)
			})
		})
	})
}

// audit records an administrative action against the acting principal.
func (s *Server) audit(r *http.Request, action, targetType, targetID string, metadata map[string]any) {
	if s.auditor == nil {
		return
	}
	principal := auth.FromContext(r.Context())
	s.auditor.TryRecord(r.Context(), audit.Entry{
		ActorType:  principal.ActorType(),
		ActorID:    principal.ActorID(),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		RequestID:  httpx.RequestIDFromContext(r.Context()),
		IPAddress:  httpx.ClientIP(r),
		Metadata:   metadata,
	})
}

// storageIsUnique re-exports the storage helper so handlers can recognise a
// specific constraint without importing the storage package everywhere.
func storageIsUnique(err error, constraints ...string) bool {
	return storage.IsUniqueViolation(err, constraints...)
}

// injectLogger seeds every request context with the server's base logger.
func injectLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(logging.WithLogger(r.Context(), logger)))
		})
	}
}
