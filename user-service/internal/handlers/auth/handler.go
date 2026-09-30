package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	jwt "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

var (
	_ userv1connect.AuthServiceHandler      = (*Handler)(nil)
	_ userv1connect.PublicKeyServiceHandler = (*Handler)(nil)
)

const (
	RefreshCookieName = "foc-refresh-token"
	authServicePath   = "/user.v1.AuthService/"
)

type Logic interface {
	RequestLink(context.Context, string) error
	Login(context.Context, string) (models.User, jwt.AuthTokens, error)
	Register(context.Context, string, jwt.Profile) (models.User, jwt.AuthTokens, error)
	Refresh(context.Context, string) (models.User, jwt.AuthTokens, error)
	Logout(context.Context, string) error
	PublicKeys() (jwt.JWKSet, error)
}

// Handler adapts the authentication operations to the generated Connect APIs.
// Business rules remain in Logic; this layer owns protobuf and HTTP metadata.
type Handler struct {
	userv1connect.UnimplementedAuthServiceHandler
	userv1connect.UnimplementedPublicKeyServiceHandler

	Logic         Logic
	AllowedOrigin string
}

func (h *Handler) RequestLink(
	ctx context.Context,
	req *connect.Request[userv1.RequestLinkRequest],
) (*connect.Response[userv1.RequestLinkResponse], error) {
	if err := h.Logic.RequestLink(ctx, req.Msg.Email); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&userv1.RequestLinkResponse{}), nil
}

func (h *Handler) Login(
	ctx context.Context,
	req *connect.Request[userv1.LoginRequest],
) (*connect.Response[userv1.LoginResponse], error) {
	user, tokens, err := h.Logic.Login(ctx, req.Msg.Token)
	if err != nil {
		return nil, mapError(err)
	}
	response := connect.NewResponse(&userv1.LoginResponse{
		User:        userMessage(user),
		AccessToken: tokens.AccessToken,
	})
	setSessionHeaders(response, tokens)
	return response, nil
}

func (h *Handler) Register(
	ctx context.Context,
	req *connect.Request[userv1.RegisterRequest],
) (*connect.Response[userv1.RegisterResponse], error) {
	user, tokens, err := h.Logic.Register(ctx, req.Msg.Token, jwt.Profile{
		DisplayName: req.Msg.DisplayName, TelegramHandle: req.Msg.TelegramHandle,
		PhoneNumber: req.Msg.PhoneNumber,
	})
	if err != nil {
		return nil, mapError(err)
	}
	response := connect.NewResponse(&userv1.RegisterResponse{
		User:        userMessage(user),
		AccessToken: tokens.AccessToken,
	})
	setSessionHeaders(response, tokens)
	return response, nil
}

func (h *Handler) Refresh(
	ctx context.Context,
	req *connect.Request[userv1.RefreshRequest],
) (*connect.Response[userv1.RefreshResponse], error) {
	user, tokens, err := h.Logic.Refresh(ctx, cookieValue(req.Header(), RefreshCookieName))
	if err != nil {
		return nil, mapError(err)
	}
	response := connect.NewResponse(&userv1.RefreshResponse{AccessToken: tokens.AccessToken, User: userMessage(user)})
	setSessionHeaders(response, tokens)
	return response, nil
}

func (h *Handler) Logout(
	ctx context.Context,
	req *connect.Request[userv1.LogoutRequest],
) (*connect.Response[userv1.LogoutResponse], error) {
	if err := h.Logic.Logout(ctx, cookieValue(req.Header(), RefreshCookieName)); err != nil {
		return nil, mapError(err)
	}
	response := connect.NewResponse(&userv1.LogoutResponse{})
	response.Header().Add("Set-Cookie", clearRefreshCookie())
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) GetPublicKeys(
	ctx context.Context,
	_ *connect.Request[userv1.GetPublicKeysRequest],
) (*connect.Response[userv1.GetPublicKeysResponse], error) {
	keys, err := h.Logic.PublicKeys()
	if err != nil {
		return nil, mapError(err)
	}
	response := &userv1.GetPublicKeysResponse{Keys: make([]*userv1.JsonWebKey, 0, len(keys.Keys))}
	for _, key := range keys.Keys {
		response.Keys = append(response.Keys, &userv1.JsonWebKey{
			Kty: key.KeyType, Crv: key.Curve, X: key.X, Y: key.Y,
			Use: key.Use, Alg: key.Algorithm, Kid: key.KeyID,
		})
	}
	result := connect.NewResponse(response)
	result.Header().Set("Cache-Control", "public, max-age=300")
	return result, nil
}

func userMessage(user models.User) *userv1.User {
	role := map[models.RoleName]userv1.UserRole{
		models.RoleSuperAdmin: userv1.UserRole_USER_ROLE_SUPER_ADMIN,
		models.RoleAdmin:      userv1.UserRole_USER_ROLE_ADMIN,
		models.RoleUser:       userv1.UserRole_USER_ROLE_USER,
		models.RoleSuspended:  userv1.UserRole_USER_ROLE_SUSPENDED_USER,
	}[user.Role]
	return &userv1.User{Id: strconv.FormatUint(uint64(user.ID), 10), Email: user.Email,
		DisplayName: user.DisplayName, Role: role, TelegramHandle: user.TelegramHandle,
		PhoneNumber: user.PhoneNumber}
}

func setSessionHeaders(response interface{ Header() http.Header }, tokens jwt.AuthTokens) {
	response.Header().Add("Set-Cookie", refreshCookie(tokens))
	response.Header().Set("Cache-Control", "no-store")
}

func mapError(err error) error {
	switch {
	case errors.Is(err, jwt.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("authentication service unavailable"))
	case errors.Is(err, jwt.ErrInvalidEmail), errors.Is(err, jwt.ErrInvalidProfile):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, jwt.ErrLoginFailed), errors.Is(err, jwt.ErrRegistrationFailed),
		errors.Is(err, jwt.ErrRefreshFailed):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, jwt.ErrAlreadyRegistered):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("email already registered; request a login link"))
	default:
		return err
	}
}
