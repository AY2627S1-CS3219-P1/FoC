package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
)

const (
	MagicLinkLifetime = 10 * time.Minute
	// TODO: A revoked session may still have a valid access JWT for up to ten
	// minutes. This does not satisfy backlog NFR-05.4.1's one-minute limit.
	AccessTokenLifetime  = 10 * time.Minute
	RefreshTokenLifetime = 30 * 24 * time.Hour
)

type Service struct {
	deps Dependencies
	cfg  Config
	base *url.URL
}

func NewService(deps Dependencies, cfg Config) (*Service, error) {
	base, err := url.Parse(cfg.FrontendBaseURL)
	if err != nil || base.Host == "" || base.User != nil || base.Opaque != "" ||
		(base.Scheme != "https" && base.Scheme != "http") || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("invalid frontend base URL")
	}
	if !cfg.LocalDevelopment && base.Scheme != "https" {
		return nil, errors.New("frontend base URL must use HTTPS outside local mode")
	}
	if cfg.AccessTokenTTL == 0 {
		cfg.AccessTokenTTL = AccessTokenLifetime
	}
	if cfg.RefreshTokenTTL == 0 {
		cfg.RefreshTokenTTL = RefreshTokenLifetime
	}
	if cfg.AccessTokenTTL < time.Second || cfg.RefreshTokenTTL < time.Second {
		return nil, errors.New("JWT token lifetimes must be at least one second")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{deps: deps, cfg: cfg, base: base}, nil
}

func (s *Service) RequestLink(ctx context.Context, email string) error {
	email, err := normalizeEmail(email)
	if err != nil {
		return err
	}

	user, err := s.deps.Users.FindByEmail(ctx, email)
	isLogin := err == nil
	if isLogin {
		if user.ID == "" {
			return errors.New("found user has no ID")
		}
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("find user by email: %w", err)
	}

	token, digest, err := randomToken(32)
	if err != nil {
		return fmt.Errorf("generate magic link: %w", err)
	}

	expires := s.cfg.Now().UTC().Add(MagicLinkLifetime)
	if isLogin {
		err = s.deps.LoginTokens.Save(ctx, LoginChallenge{Digest: digest, UserID: user.ID, ExpiresAt: expires})
	} else {
		err = s.deps.RegistrationTokens.Save(ctx, RegistrationChallenge{Digest: digest, Email: email, ExpiresAt: expires})
	}
	if err != nil {
		return fmt.Errorf("save magic link: %w", err)
	}

	link := *s.base
	if isLogin {
		link.Path = strings.TrimRight(link.Path, "/") + "/login"
	} else {
		link.Path = strings.TrimRight(link.Path, "/") + "/register"
	}
	query := link.Query()
	query.Set("token", token)
	link.RawQuery = query.Encode()
	if err := s.sendMagicLinkEmail(ctx, email, link.String()); err != nil {
		return err
	}
	return nil
}

func (s *Service) sendMagicLinkEmail(ctx context.Context, recipient, link string) error {
	err := s.sendEmail(ctx, email.Email{
		To:       []string{recipient},
		Subject:  "Your sign-in link",
		HTMLBody: "<p>Use this link to sign in or create an account:</p><p><a href=\"" + html.EscapeString(link) + "\">Continue</a></p>",
		TextBody: "Use this link to sign in or create an account:\n" + link,
	})
	if err != nil {
		return ErrUnavailable
	}

	return nil
}

func (s *Service) sendEmail(ctx context.Context, message email.Email) error {
	return s.deps.EmailSender.Send(ctx, message)
}

func (s *Service) Login(ctx context.Context, loginToken string) (User, AuthTokens, error) {
	digest, err := digestMagicToken(loginToken)
	if err != nil {
		return User{}, AuthTokens{}, ErrLoginFailed
	}

	now := s.cfg.Now().UTC()
	user, tokens, err := s.deps.LoginTokens.Complete(ctx, digest, now, func(user User) (Session, AuthTokens, error) {
		if user.ID == "" || !user.Role.Valid() {
			return Session{}, AuthTokens{}, ErrLoginFailed
		}
		return s.newSession(user, now)
	})
	if errors.Is(err, ErrChallengeRejected) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrLoginFailed) {
		return User{}, AuthTokens{}, ErrLoginFailed
	}
	if err != nil {
		return User{}, AuthTokens{}, fmt.Errorf("complete login: %w", err)
	}
	return user, tokens, nil
}

func (s *Service) Register(ctx context.Context, registrationToken string, profile Profile) (User, AuthTokens, error) {
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	if profile.DisplayName == "" || utf8.RuneCountInString(profile.DisplayName) > 100 {
		return User{}, AuthTokens{}, ErrInvalidProfile
	}
	digest, err := digestMagicToken(registrationToken)
	if err != nil {
		return User{}, AuthTokens{}, ErrRegistrationFailed
	}
	now := s.cfg.Now().UTC()
	user, tokens, err := s.deps.RegistrationTokens.Complete(ctx, digest, profile, now, func(user User) (Session, AuthTokens, error) {
		if user.ID == "" || user.Role != RoleUser {
			return Session{}, AuthTokens{}, errors.New("created user has invalid identity or role")
		}
		return s.newSession(user, now)
	})
	if errors.Is(err, ErrChallengeRejected) {
		return User{}, AuthTokens{}, ErrRegistrationFailed
	}
	if err != nil {
		if errors.Is(err, ErrAlreadyRegistered) {
			return User{}, AuthTokens{}, ErrAlreadyRegistered
		}
		return User{}, AuthTokens{}, fmt.Errorf("complete registration: %w", err)
	}
	return user, tokens, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (AuthTokens, error) {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, RefreshToken, now)
	if err != nil {
		return AuthTokens{}, ErrRefreshFailed
	}
	user, err := s.deps.Users.FindByID(ctx, claims.Subject)
	if errors.Is(err, ErrNotFound) {
		return AuthTokens{}, ErrRefreshFailed
	}
	if err != nil {
		return AuthTokens{}, fmt.Errorf("find refresh user: %w", err)
	}
	if user.ID != claims.Subject || !user.Role.Valid() {
		return AuthTokens{}, ErrRefreshFailed
	}
	tokens, err := s.signSessionTokens(user, claims.SessionID, now)
	if err != nil {
		return AuthTokens{}, err
	}
	err = s.deps.Sessions.Rotate(ctx, claims.SessionID, sha256.Sum256([]byte(refreshToken)),
		sha256.Sum256([]byte(tokens.RefreshToken)), now, tokens.RefreshExpiry)
	if errors.Is(err, ErrSessionRejected) {
		return AuthTokens{}, ErrRefreshFailed
	}
	if err != nil {
		return AuthTokens{}, fmt.Errorf("rotate refresh session: %w", err)
	}
	return tokens, nil
}

// Logout is idempotent. An unrecognizable or already revoked cookie is cleared
// by the HTTP adapter; a valid cookie is revoked before success is returned.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, RefreshToken, now)
	if err != nil {
		return nil
	}
	err = s.deps.Sessions.Revoke(ctx, claims.SessionID, sha256.Sum256([]byte(refreshToken)), now)
	if errors.Is(err, ErrSessionRejected) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("revoke refresh session: %w", err)
	}
	return nil
}

func (s *Service) PublicKeys() (JWKSet, error) {
	return s.deps.TokenCodec.PublicKeys(), nil
}

func (s *Service) newSession(user User, now time.Time) (Session, AuthTokens, error) {
	sessionID, _, err := randomToken(16)
	if err != nil {
		return Session{}, AuthTokens{}, fmt.Errorf("generate session ID: %w", err)
	}
	tokens, err := s.signSessionTokens(user, sessionID, now)
	if err != nil {
		return Session{}, AuthTokens{}, err
	}
	return Session{ID: sessionID, UserID: user.ID,
		RefreshDigest: sha256.Sum256([]byte(tokens.RefreshToken)), ExpiresAt: tokens.RefreshExpiry}, tokens, nil
}

func (s *Service) signSessionTokens(user User, sessionID string, now time.Time) (AuthTokens, error) {
	accessID, _, err := randomToken(16)
	if err != nil {
		return AuthTokens{}, fmt.Errorf("generate access token ID: %w", err)
	}
	refreshID, _, err := randomToken(16)
	if err != nil {
		return AuthTokens{}, fmt.Errorf("generate refresh token ID: %w", err)
	}
	accessExpiry := now.Add(s.cfg.AccessTokenTTL)
	refreshExpiry := now.Add(s.cfg.RefreshTokenTTL)
	access, err := s.deps.TokenCodec.Sign(Claims{Type: AccessToken, Subject: user.ID,
		SessionID: sessionID, Role: user.Role, IssuedAt: now, ExpiresAt: accessExpiry, TokenID: accessID})
	if err != nil {
		return AuthTokens{}, fmt.Errorf("sign access token: %w", err)
	}
	refresh, err := s.deps.TokenCodec.Sign(Claims{Type: RefreshToken, Subject: user.ID,
		SessionID: sessionID, IssuedAt: now, ExpiresAt: refreshExpiry, TokenID: refreshID})
	if err != nil {
		return AuthTokens{}, fmt.Errorf("sign refresh token: %w", err)
	}
	return AuthTokens{AccessToken: access, RefreshToken: refresh,
		AccessExpiry: accessExpiry, RefreshExpiry: refreshExpiry}, nil
}

func normalizeEmail(input string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(input))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || strings.ContainsAny(email, "\r\n") {
		return "", ErrInvalidEmail
	}
	return email, nil
}

func randomToken(size int) (string, [32]byte, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	return token, sha256.Sum256([]byte(token)), nil
}

func digestMagicToken(token string) ([32]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return [32]byte{}, ErrChallengeRejected
	}
	return sha256.Sum256([]byte(token)), nil
}
