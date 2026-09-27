package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
)

const (
	MagicLinkLifetime = 10 * time.Minute
	// TODO: A revoked session may still have a valid access JWT for up to ten
	// minutes. This does not satisfy backlog NFR-05.4.1's one-minute limit.
	AccessTokenLifetime  = 10 * time.Minute
	RefreshTokenLifetime = 30 * 24 * time.Hour
)

func (s *Service) RequestLink(ctx context.Context, email string) error {
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return err
	}

	user, err := s.deps.Users.FindByEmail(ctx, normalizedEmail)
	isLogin := err == nil
	if isLogin {
		if user.ID == "" {
			return errors.New("found user has no ID")
		}
	} else if !errors.Is(err, jwt.ErrNotFound) {
		return fmt.Errorf("find user by email: %w", err)
	}

	token, digest, err := randomToken(32)
	if err != nil {
		return fmt.Errorf("generate magic link: %w", err)
	}

	expires := s.cfg.Now().UTC().Add(MagicLinkLifetime)
	if isLogin {
		err = s.deps.LoginTokens.Save(ctx, jwt.LoginChallenge{Digest: digest, UserID: user.ID, ExpiresAt: expires})
	} else {
		err = s.deps.RegistrationTokens.Save(ctx, jwt.RegistrationChallenge{Digest: digest, Email: normalizedEmail, ExpiresAt: expires})
	}
	if err != nil {
		return fmt.Errorf("save magic link: %w", err)
	}

	link := s.cfg.FrontendBaseURL
	if isLogin {
		link.Path = strings.TrimRight(link.Path, "/") + "/login"
	} else {
		link.Path = strings.TrimRight(link.Path, "/") + "/register"
	}
	query := link.Query()
	query.Set("token", token)
	link.RawQuery = query.Encode()
	if err := s.sendMagicLinkEmail(ctx, normalizedEmail, link.String()); err != nil {
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
		return jwt.ErrUnavailable
	}

	return nil
}

func (s *Service) sendEmail(ctx context.Context, message email.Email) error {
	return s.deps.EmailSender.Send(ctx, message)
}

func (s *Service) Login(ctx context.Context, loginToken string) (jwt.User, jwt.AuthTokens, error) {
	digest, err := digestMagicToken(loginToken)
	if err != nil {
		return jwt.User{}, jwt.AuthTokens{}, jwt.ErrLoginFailed
	}

	now := s.cfg.Now().UTC()
	user, tokens, err := s.deps.LoginTokens.Complete(ctx, digest, now, func(user jwt.User) (jwt.Session, jwt.AuthTokens, error) {
		if user.ID == "" || !user.Role.Valid() {
			return jwt.Session{}, jwt.AuthTokens{}, jwt.ErrLoginFailed
		}
		return s.newSession(user, now)
	})
	if errors.Is(err, jwt.ErrChallengeRejected) || errors.Is(err, jwt.ErrNotFound) || errors.Is(err, jwt.ErrLoginFailed) {
		return jwt.User{}, jwt.AuthTokens{}, jwt.ErrLoginFailed
	}
	if err != nil {
		return jwt.User{}, jwt.AuthTokens{}, fmt.Errorf("complete login: %w", err)
	}
	return user, tokens, nil
}

func (s *Service) Register(ctx context.Context, registrationToken string, profile jwt.Profile) (jwt.User, jwt.AuthTokens, error) {
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	if profile.DisplayName == "" || utf8.RuneCountInString(profile.DisplayName) > 100 {
		return jwt.User{}, jwt.AuthTokens{}, jwt.ErrInvalidProfile
	}
	digest, err := digestMagicToken(registrationToken)
	if err != nil {
		return jwt.User{}, jwt.AuthTokens{}, jwt.ErrRegistrationFailed
	}
	now := s.cfg.Now().UTC()
	user, tokens, err := s.deps.RegistrationTokens.Complete(ctx, digest, profile, now, func(user jwt.User) (jwt.Session, jwt.AuthTokens, error) {
		if user.ID == "" || user.Role != jwt.RoleUser {
			return jwt.Session{}, jwt.AuthTokens{}, errors.New("created user has invalid identity or role")
		}
		return s.newSession(user, now)
	})
	if errors.Is(err, jwt.ErrChallengeRejected) {
		return jwt.User{}, jwt.AuthTokens{}, jwt.ErrRegistrationFailed
	}
	if err != nil {
		if errors.Is(err, jwt.ErrAlreadyRegistered) {
			return jwt.User{}, jwt.AuthTokens{}, jwt.ErrAlreadyRegistered
		}
		return jwt.User{}, jwt.AuthTokens{}, fmt.Errorf("complete registration: %w", err)
	}
	return user, tokens, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (jwt.AuthTokens, error) {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, jwt.RefreshToken, now)
	if err != nil {
		return jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	user, err := s.deps.Users.FindByID(ctx, claims.Subject)
	if errors.Is(err, jwt.ErrNotFound) {
		return jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("find refresh user: %w", err)
	}
	if user.ID != claims.Subject || !user.Role.Valid() {
		return jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	tokens, err := s.signSessionTokens(user, claims.SessionID, now)
	if err != nil {
		return jwt.AuthTokens{}, err
	}
	err = s.deps.Sessions.Rotate(ctx, claims.SessionID, sha256.Sum256([]byte(refreshToken)),
		sha256.Sum256([]byte(tokens.RefreshToken)), now, tokens.RefreshExpiry)
	if errors.Is(err, jwt.ErrSessionRejected) {
		return jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("rotate refresh session: %w", err)
	}
	return tokens, nil
}

// Logout is idempotent. An unrecognizable or already revoked cookie is cleared
// by the HTTP adapter; a valid cookie is revoked before success is returned.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, jwt.RefreshToken, now)
	if err != nil {
		return nil
	}
	err = s.deps.Sessions.Revoke(ctx, claims.SessionID, sha256.Sum256([]byte(refreshToken)), now)
	if errors.Is(err, jwt.ErrSessionRejected) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("revoke refresh session: %w", err)
	}
	return nil
}

func (s *Service) PublicKeys() (jwt.JWKSet, error) {
	return s.deps.TokenCodec.PublicKeys(), nil
}

func (s *Service) newSession(user jwt.User, now time.Time) (jwt.Session, jwt.AuthTokens, error) {
	sessionID, _, err := randomToken(16)
	if err != nil {
		return jwt.Session{}, jwt.AuthTokens{}, fmt.Errorf("generate session ID: %w", err)
	}
	tokens, err := s.signSessionTokens(user, sessionID, now)
	if err != nil {
		return jwt.Session{}, jwt.AuthTokens{}, err
	}
	return jwt.Session{ID: sessionID, UserID: user.ID,
		RefreshDigest: sha256.Sum256([]byte(tokens.RefreshToken)), ExpiresAt: tokens.RefreshExpiry}, tokens, nil
}

func (s *Service) signSessionTokens(user jwt.User, sessionID string, now time.Time) (jwt.AuthTokens, error) {
	accessID, _, err := randomToken(16)
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("generate access token ID: %w", err)
	}
	refreshID, _, err := randomToken(16)
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("generate refresh token ID: %w", err)
	}
	accessExpiry := now.Add(s.cfg.AccessTokenTTL)
	refreshExpiry := now.Add(s.cfg.RefreshTokenTTL)
	access, err := s.deps.TokenCodec.Sign(jwt.Claims{Type: jwt.AccessToken, Subject: user.ID,
		SessionID: sessionID, Role: user.Role, IssuedAt: now, ExpiresAt: accessExpiry, TokenID: accessID})
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("sign access token: %w", err)
	}
	refresh, err := s.deps.TokenCodec.Sign(jwt.Claims{Type: jwt.RefreshToken, Subject: user.ID,
		SessionID: sessionID, IssuedAt: now, ExpiresAt: refreshExpiry, TokenID: refreshID})
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("sign refresh token: %w", err)
	}
	return jwt.AuthTokens{AccessToken: access, RefreshToken: refresh,
		AccessExpiry: accessExpiry, RefreshExpiry: refreshExpiry}, nil
}

func normalizeEmail(input string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(input))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || strings.ContainsAny(email, "\r\n") {
		return "", jwt.ErrInvalidEmail
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
		return [32]byte{}, jwt.ErrChallengeRejected
	}
	return sha256.Sum256([]byte(token)), nil
}
