package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/anggapixa/paymux/internal/application"
	"github.com/anggapixa/paymux/internal/crypto"
	"github.com/anggapixa/paymux/internal/httpx"
	"github.com/anggapixa/paymux/internal/storage"
)

// timeFormat is the timestamp format PayMux emits: RFC 3339 in UTC.
const timeFormat = time.RFC3339

type createApplicationRequest struct {
	Name             string         `json:"name"`
	Slug             string         `json:"slug"`
	Description      string         `json:"description"`
	GatewayAccountID string         `json:"gateway_account_id"`
	Metadata         map[string]any `json:"metadata"`
}

func (s *Server) handleCreateApplication(w http.ResponseWriter, r *http.Request) {
	var req createApplicationRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	app, err := s.applications.Create(r.Context(), application.CreateInput{
		Name:             req.Name,
		Slug:             req.Slug,
		Description:      req.Description,
		GatewayAccountID: req.GatewayAccountID,
		Metadata:         req.Metadata,
	})
	if err != nil {
		fail(w, r, err, applicationMissing)
		return
	}
	s.audit(r, "application.created", "application", app.ID, map[string]any{"slug": app.Slug})
	httpx.JSON(w, r, http.StatusCreated, renderApplication(app))
}

func (s *Server) handleListApplications(w http.ResponseWriter, r *http.Request) {
	page := pageFromRequest(r)
	list, err := s.applications.List(r.Context(), page)
	if err != nil {
		fail(w, r, err, applicationMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK,
		renderList(list.Items, list.HasMore, list.Limit, renderApplication))
}

func (s *Server) handleGetApplication(w http.ResponseWriter, r *http.Request) {
	app, err := s.applications.Get(r.Context(), chi.URLParam(r, "applicationID"))
	if err != nil {
		fail(w, r, err, applicationMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderApplication(app))
}

type updateApplicationRequest struct {
	Name             *string        `json:"name"`
	Description      *string        `json:"description"`
	GatewayAccountID *string        `json:"gateway_account_id"`
	Metadata         map[string]any `json:"metadata"`
	Disabled         *bool          `json:"disabled"`
}

func (s *Server) handleUpdateApplication(w http.ResponseWriter, r *http.Request) {
	var req updateApplicationRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	id := chi.URLParam(r, "applicationID")
	app, err := s.applications.Update(r.Context(), id, application.ApplicationUpdate{
		Name:             req.Name,
		Description:      req.Description,
		GatewayAccountID: req.GatewayAccountID,
		Metadata:         req.Metadata,
		Disabled:         req.Disabled,
	})
	if err != nil {
		fail(w, r, err, applicationMissing)
		return
	}
	s.audit(r, "application.updated", "application", app.ID, nil)
	httpx.JSON(w, r, http.StatusOK, renderApplication(app))
}

// ---------------------------------------------------------------------------
// API keys
// ---------------------------------------------------------------------------

type createAPIKeyRequest struct {
	Name      string     `json:"name"`
	Mode      string     `json:"mode"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	mode := crypto.KeyMode(req.Mode)
	if req.Mode == "" {
		mode = crypto.KeyModeLive
	}
	applicationID := chi.URLParam(r, "applicationID")

	issued, err := s.applications.CreateAPIKey(r.Context(), applicationID, req.Name, mode, req.ExpiresAt)
	if err != nil {
		fail(w, r, err, applicationMissing)
		return
	}
	s.audit(r, "api_key.created", "api_key", issued.Key.ID, map[string]any{
		"application_id": applicationID,
		"mode":           string(mode),
	})

	// The only time the plaintext key is ever returned.
	response := renderAPIKey(issued.Key)
	response.Key = issued.Plaintext.Reveal()
	httpx.JSON(w, r, http.StatusCreated, response)
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.applications.ListAPIKeys(r.Context(), chi.URLParam(r, "applicationID"))
	if err != nil {
		fail(w, r, err, applicationMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(keys, false, len(keys), renderAPIKey))
}

func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	applicationID := chi.URLParam(r, "applicationID")
	key, err := s.applications.RevokeAPIKey(r.Context(), applicationID, chi.URLParam(r, "keyID"))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	s.audit(r, "api_key.revoked", "api_key", key.ID, map[string]any{"application_id": applicationID})
	httpx.JSON(w, r, http.StatusOK, renderAPIKey(key))
}

// ---------------------------------------------------------------------------
// Webhook destinations
// ---------------------------------------------------------------------------

type createDestinationRequest struct {
	URL         string   `json:"url"`
	Description string   `json:"description"`
	EventTypes  []string `json:"event_types"`
	Enabled     *bool    `json:"enabled"`
}

func (s *Server) handleCreateDestination(w http.ResponseWriter, r *http.Request) {
	var req createDestinationRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	applicationID := chi.URLParam(r, "applicationID")
	dst, err := s.applications.CreateDestination(r.Context(), applicationID, application.CreateDestinationInput{
		URL:         req.URL,
		Description: req.Description,
		EventTypes:  req.EventTypes,
		Enabled:     req.Enabled,
	})
	if err != nil {
		fail(w, r, err, applicationMissing)
		return
	}
	s.audit(r, "destination.created", "webhook_destination", dst.ID, map[string]any{
		"application_id": applicationID,
	})

	// The signing secret is shown at creation so it can be copied into the
	// receiving application; afterwards it is write-only.
	response := renderDestination(dst)
	response.Secret = dst.Secret.Reveal()
	httpx.JSON(w, r, http.StatusCreated, response)
}

func (s *Server) handleListDestinations(w http.ResponseWriter, r *http.Request) {
	destinations, err := s.applications.ListDestinations(r.Context(), chi.URLParam(r, "applicationID"))
	if err != nil {
		fail(w, r, err, applicationMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(destinations, false, len(destinations), renderDestination))
}

type updateDestinationRequest struct {
	URL         *string  `json:"url"`
	Description *string  `json:"description"`
	EventTypes  []string `json:"event_types"`
	Enabled     *bool    `json:"enabled"`
}

func (s *Server) handleUpdateDestination(w http.ResponseWriter, r *http.Request) {
	var req updateDestinationRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	applicationID := chi.URLParam(r, "applicationID")
	dst, err := s.applications.UpdateDestination(r.Context(), applicationID, chi.URLParam(r, "destinationID"),
		application.DestinationUpdate{
			URL:         req.URL,
			Description: req.Description,
			EventTypes:  req.EventTypes,
			Enabled:     req.Enabled,
		})
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	s.audit(r, "destination.updated", "webhook_destination", dst.ID, nil)
	httpx.JSON(w, r, http.StatusOK, renderDestination(dst))
}

func (s *Server) handleRotateDestinationSecret(w http.ResponseWriter, r *http.Request) {
	applicationID := chi.URLParam(r, "applicationID")
	dst, err := s.applications.RotateDestinationSecret(r.Context(), applicationID, chi.URLParam(r, "destinationID"))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	s.audit(r, "destination.secret_rotated", "webhook_destination", dst.ID, nil)

	response := renderDestination(dst)
	response.Secret = dst.Secret.Reveal()
	httpx.JSON(w, r, http.StatusOK, response)
}

func (s *Server) handleDeleteDestination(w http.ResponseWriter, r *http.Request) {
	applicationID := chi.URLParam(r, "applicationID")
	destinationID := chi.URLParam(r, "destinationID")
	if err := s.applications.DeleteDestination(r.Context(), applicationID, destinationID); err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	s.audit(r, "destination.deleted", "webhook_destination", destinationID, nil)
	httpx.NoContent(w)
}

// pageFromRequest reads keyset pagination parameters from the query string.
func pageFromRequest(r *http.Request) storage.Page {
	query := r.URL.Query()
	page := storage.Page{
		StartingAfter: query.Get("starting_after"),
		EndingBefore:  query.Get("ending_before"),
	}
	if limit := query.Get("limit"); limit != "" {
		// A malformed limit falls back to the default rather than failing the
		// request: pagination is a hint, not a correctness concern.
		if n, err := strconv.Atoi(limit); err == nil {
			page.Limit = n
		}
	}
	return page.Normalize()
}
