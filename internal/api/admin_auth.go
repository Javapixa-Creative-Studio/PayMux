package api

import (
	"net/http"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/auth"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/httpx"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Admin     adminResponse `json:"admin"`
	ExpiresAt string        `json:"expires_at"`
}

// handleLogin authenticates an administrator and starts a dashboard session.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	issued, err := s.authService.Login(r.Context(), auth.LoginInput{
		Email:     req.Email,
		Password:  req.Password,
		UserAgent: r.UserAgent(),
		IPAddress: httpx.ClientIP(r),
	})
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}

	s.authMiddleware.SetSessionCookie(w, issued.Token.Reveal(), issued.Session.ExpiresAt)
	httpx.JSON(w, r, http.StatusOK, sessionResponse{
		Admin:     renderAdmin(issued.Admin),
		ExpiresAt: issued.Session.ExpiresAt.UTC().Format(timeFormat),
	})
}

// handleLogout ends the current session.
//
// It succeeds even without a valid session so that signing out is always
// possible, including from a session the server has already forgotten.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil && cookie.Value != "" {
		if err := s.authService.Logout(r.Context(), cookie.Value); err != nil {
			httpx.Fail(w, r, httpx.ErrInternal(err))
			return
		}
	}
	s.authMiddleware.ClearSessionCookie(w)
	httpx.NoContent(w)
}

// handleCurrentAdmin returns the signed-in administrator.
func (s *Server) handleCurrentAdmin(w http.ResponseWriter, r *http.Request) {
	admin := auth.AdminFromContext(r.Context())
	if admin == nil {
		httpx.Fail(w, r, httpx.ErrUnauthorized("Authentication is required."))
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderAdmin(admin))
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleChangePassword updates the signed-in administrator's password.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	admin := auth.AdminFromContext(r.Context())
	var req changePasswordRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := s.authService.ChangePassword(r.Context(), admin.ID, req.CurrentPassword, req.NewPassword); err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	// Every session was revoked, including this one: the operator signs in
	// again with the new password.
	s.authMiddleware.ClearSessionCookie(w)
	httpx.NoContent(w)
}

type createAdminRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// handleCreateAdmin registers another administrator.
func (s *Server) handleCreateAdmin(w http.ResponseWriter, r *http.Request) {
	var req createAdminRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	admin, err := s.authService.CreateAdmin(r.Context(), req.Email, req.Name, req.Password)
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	s.audit(r, "admin.created", "admin", admin.ID, nil)
	httpx.JSON(w, r, http.StatusCreated, renderAdmin(admin))
}

// handleListAdmins lists administrators.
func (s *Server) handleListAdmins(w http.ResponseWriter, r *http.Request) {
	admins, err := s.authService.ListAdmins(r.Context())
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(admins, false, len(admins), renderAdmin))
}
