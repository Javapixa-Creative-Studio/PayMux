// Package api assembles PayMux's HTTP surface: the application API, the
// dashboard admin API and the gateway notification endpoints.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/anggapixa/paymux/internal/httpx"
)

// Version is stamped at build time with -ldflags.
var Version = "dev"

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

type readyResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// handleHealth reports process liveness. It touches no dependencies, so an
// unhealthy database never causes an orchestrator to restart a working process
// (PRD §68).
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, r, http.StatusOK, healthResponse{Status: "ok", Version: Version})
}

// handleReady reports whether PayMux can serve traffic, which requires a
// reachable database.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]string{"database": "ok"}
	status := http.StatusOK
	body := readyResponse{Status: "ready", Checks: checks}

	if err := s.db.Ping(ctx); err != nil {
		checks["database"] = "unavailable"
		body.Status = "not_ready"
		status = http.StatusServiceUnavailable
	}
	httpx.JSON(w, r, status, body)
}
