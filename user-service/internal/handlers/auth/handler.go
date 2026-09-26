package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/exterrors/errs"
	logic "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/dto/params"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/dto/views"
)

const (
	RefreshCookieName = "foc-refresh-token"
)

type Logic interface {
	RequestLink(context.Context, string) error
	Login(context.Context, string) (logic.User, logic.AuthTokens, error)
	Register(context.Context, string, logic.Profile) (logic.User, logic.AuthTokens, error)
	Refresh(context.Context, string) (logic.AuthTokens, error)
	Logout(context.Context, string) error
	PublicKeys() (logic.JWKSet, error)
}

type Handler struct {
	Logic         Logic
	AllowedOrigin string
}

func (h *Handler) RequestLink(r *http.Request, _ *deps.Env) (*api.Response, error) {
	if err := h.checkOrigin(r); err != nil {
		return nil, err
	}
	var req params.MagicLinkRequest
	if err := api.Decode(r, &req); err != nil {
		return nil, err
	}
	if err := h.Logic.RequestLink(r.Context(), req.Email); err != nil {
		return nil, mapError(err)
	}
	return api.NewResponse(nil, api.WithCode(http.StatusAccepted),
		api.WithStatus(api.SUCCESS, "sign-in link requested"))
}

func (h *Handler) Login(r *http.Request, _ *deps.Env) (*api.Response, error) {
	if err := h.checkOrigin(r); err != nil {
		return nil, err
	}
	var req params.LoginToken
	if err := api.Decode(r, &req); err != nil {
		return nil, err
	}
	user, tokens, err := h.Logic.Login(r.Context(), req.Token)
	if err != nil {
		return nil, mapError(err)
	}
	return api.NewResponse(views.Session{User: userView(user), AccessToken: tokens.AccessToken}, refreshCookieHeader(tokens), api.WithHeader("Cache-Control", "no-store"),
		api.WithStatus(api.SUCCESS, "login successful"))
}

func (h *Handler) Register(r *http.Request, _ *deps.Env) (*api.Response, error) {
	if err := h.checkOrigin(r); err != nil {
		return nil, err
	}
	var req params.UserRegistration
	if err := api.Decode(r, &req); err != nil {
		return nil, err
	}
	user, tokens, err := h.Logic.Register(r.Context(), req.Token, logic.Profile{
		DisplayName: req.DisplayName})
	if err != nil {
		return nil, mapError(err)
	}
	return api.NewResponse(views.Session{User: userView(user), AccessToken: tokens.AccessToken}, api.WithCode(http.StatusCreated), refreshCookieHeader(tokens), api.WithHeader("Cache-Control", "no-store"),
		api.WithStatus(api.SUCCESS, "registration successful"))
}

func (h *Handler) Refresh(r *http.Request, _ *deps.Env) (*api.Response, error) {
	if err := h.checkOrigin(r); err != nil {
		return nil, err
	}
	token := cookieValue(r, RefreshCookieName)
	tokens, err := h.Logic.Refresh(r.Context(), token)
	if err != nil {
		return nil, mapError(err)
	}
	return api.NewResponse(views.RefreshedSession{AccessToken: tokens.AccessToken}, refreshCookieHeader(tokens), api.WithHeader("Cache-Control", "no-store"),
		api.WithStatus(api.SUCCESS, "session refreshed"))
}

// TODO: also add handler for all-device log out
func (h *Handler) Logout(r *http.Request, _ *deps.Env) (*api.Response, error) {
	if err := h.checkOrigin(r); err != nil {
		return nil, err
	}
	if err := h.Logic.Logout(r.Context(), cookieValue(r, RefreshCookieName)); err != nil {
		return nil, mapError(err)
	}
	return api.NewResponse(nil, api.WithHeader("Set-Cookie", clearRefreshCookie()), api.WithHeader("Cache-Control", "no-store"), api.WithStatus(api.SUCCESS, "logged out"))
}

func (h *Handler) PublicKeys(_ *http.Request, _ *deps.Env) (*api.Response, error) {
	keys, err := h.Logic.PublicKeys()
	if err != nil {
		return nil, mapError(err)
	}
	body, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	return api.NewRawResponse(body, "application/json", api.WithHeader("Cache-Control", "public, max-age=300"))
}

func userView(user logic.User) views.User {
	return views.User{ID: user.ID, Email: user.Email, DisplayName: user.DisplayName, Role: string(user.Role)}
}

func (h *Handler) checkOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin != "" && (h.AllowedOrigin == "" || origin != h.AllowedOrigin) {
		return &errs.UnauthorizedError{}
	}
	return nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, logic.ErrUnavailable):
		return &errs.UnavailableError{}
	case errors.Is(err, logic.ErrInvalidEmail), errors.Is(err, logic.ErrInvalidProfile):
		return errs.NewBadRequestError(err.Error())
	case errors.Is(err, logic.ErrLoginFailed), errors.Is(err, logic.ErrRegistrationFailed),
		errors.Is(err, logic.ErrRefreshFailed):
		return errs.NewUnauthorizedError(err.Error())
	case errors.Is(err, logic.ErrAlreadyRegistered):
		return errs.NewConflictError("email already registered; request a login link")
	default:
		return err
	}
}
