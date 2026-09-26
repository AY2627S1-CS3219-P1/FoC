package auth

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"user-service/internal/dto"
	"user-service/internal/httpx"
	"user-service/internal/models"
)

type Handler struct {
	svc          *Service
	log          *slog.Logger
	cookieName   string
	cookieSecure bool
}

func NewHandler(svc *Service, log *slog.Logger, cookieName string, cookieSecure bool) *Handler {
	return &Handler{svc: svc, log: log, cookieName: cookieName, cookieSecure: cookieSecure}
}

// Register mounts the /auth routes. Magic-link verification is POST (the
// frontend page reads ?token= and posts it) so email scanners that prefetch
// GET links can't consume single-use tokens.
func (h *Handler) Register(r chi.Router) {
	w := func(fn httpx.HandlerFunc) http.HandlerFunc { return httpx.Wrap(h.log, fn) }
	r.Post("/auth/register", w(h.requestRegistration))
	r.Post("/auth/register/verify", w(h.completeRegistration))
	r.Post("/auth/login", w(h.requestLogin))
	r.Post("/auth/login/verify", w(h.verifyLogin))
	r.Post("/auth/logout", w(h.logout))
	r.Post("/auth/logout-all", w(h.logoutAll))
}

type emailRequest struct {
	Email string `json:"email"`
}

type completeRegistrationRequest struct {
	Token          string  `json:"token"`
	DisplayName    string  `json:"display_name"`
	Description    string  `json:"description"`
	TelegramHandle *string `json:"telegram_handle"`
	PhoneNumber    *string `json:"phone_number"`
}

type tokenRequest struct {
	Token string `json:"token"`
}

type messageResponse struct {
	Message string `json:"message"`
}

func (h *Handler) requestRegistration(w http.ResponseWriter, r *http.Request) error {
	var req emailRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if err := h.svc.RequestRegistration(r.Context(), req.Email, clientMeta(r)); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusAccepted, messageResponse{"Check your email for a registration link. It expires in 10 minutes."})
	return nil
}

func (h *Handler) completeRegistration(w http.ResponseWriter, r *http.Request) error {
	var req completeRegistrationRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	u, raw, sess, err := h.svc.CompleteRegistration(r.Context(), req.Token, ProfileInput{
		DisplayName: req.DisplayName, Description: req.Description,
		TelegramHandle: req.TelegramHandle, PhoneNumber: req.PhoneNumber,
	}, clientMeta(r))
	if err != nil {
		return err
	}
	h.setCookie(w, raw, sess)
	httpx.WriteJSON(w, http.StatusCreated, dto.Full(u))
	return nil
}

func (h *Handler) requestLogin(w http.ResponseWriter, r *http.Request) error {
	var req emailRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if err := h.svc.RequestLogin(r.Context(), req.Email, clientMeta(r)); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusAccepted, messageResponse{"If an account exists for this email, a login link is on its way. It expires in 10 minutes."})
	return nil
}

func (h *Handler) verifyLogin(w http.ResponseWriter, r *http.Request) error {
	var req tokenRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	u, raw, sess, err := h.svc.VerifyLogin(r.Context(), req.Token, clientMeta(r))
	if err != nil {
		return err
	}
	h.setCookie(w, raw, sess)
	httpx.WriteJSON(w, http.StatusOK, dto.Full(u))
	return nil
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) error {
	p, err := Require(r.Context())
	if err != nil {
		return err
	}
	if err := h.svc.Logout(r.Context(), p); err != nil {
		return err
	}
	h.clearCookie(w)
	httpx.NoContent(w)
	return nil
}

func (h *Handler) logoutAll(w http.ResponseWriter, r *http.Request) error {
	p, err := Require(r.Context())
	if err != nil {
		return err
	}
	if _, err := h.svc.LogoutAll(r.Context(), p); err != nil {
		return err
	}
	h.clearCookie(w)
	httpx.NoContent(w)
	return nil
}

// setCookie: HttpOnly + Secure + SameSite=Lax, expiring with the server
// session (NFR-05.4.2-6).
func (h *Handler) setCookie(w http.ResponseWriter, raw string, s *models.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName,
		Value:    raw,
		Path:     "/",
		Expires:  s.ExpiresAt,
		MaxAge:   int(s.ExpiresAt.Sub(s.CreatedAt).Seconds()),
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: h.cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
}
