package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

var ErrEmailDomainNotAllowed = errs.NewForbiddenError("email domain is not allowed")

const (
	MagicLinkLifetime = 10 * time.Minute
	// AccessTokenLifetime is also the TTL of the revocation dictionary: any
	// access token a revocation entry could cover expires within it.
	AccessTokenLifetime  = 10 * time.Minute
	RefreshTokenLifetime = 30 * 24 * time.Hour
)

// EffectiveAccessTokenLifetime resolves the access-token lifetime the service
// applies, so the revocation store TTL in main stays in lockstep with
// NewService's defaulting instead of duplicating it.
func EffectiveAccessTokenLifetime(configured time.Duration) time.Duration {
	if configured == 0 {
		return AccessTokenLifetime
	}
	return configured
}

func (s *Service) RequestLink(ctx context.Context, email string) error {
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return err
	}

	user, err := s.deps.Resources.Users.GetByEmail(ctx, normalizedEmail)
	isLogin := err == nil
	if isLogin {
		if user.ID == uuid.Nil {
			return errors.New("found user has no ID")
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("find user by email: %w", err)
	}
	role := models.RoleUser
	if isLogin {
		role = user.Role
	}
	if err := requireAllowedEmailDomain(ctx, s.deps.Resources.DomainPolicy, normalizedEmail, role); err != nil {
		return err
	}

	token, digest, err := randomToken(32)
	if err != nil {
		return fmt.Errorf("generate magic link: %w", err)
	}

	expires := s.cfg.Now().UTC().Add(MagicLinkLifetime)
	challenge := models.AuthToken{
		TokenHash: append([]byte(nil), digest[:]...),
		Email:     normalizedEmail,
		ExpiresAt: expires,
	}
	if isLogin {
		challenge.Purpose = models.TokenPurposeLogin
		challenge.UserID = &user.ID
	} else {
		challenge.Purpose = models.TokenPurposeRegister
	}
	if err := s.deps.Resources.AuthTokens.Create(ctx, &challenge); err != nil {
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
	err := s.deps.EmailSender.Send(ctx, email.Email{
		To:      []string{recipient},
		Subject: "Your sign-in link",
		HTMLBody: "<p>Use this link to sign in or create an account:</p><p><a href=\"" + html.EscapeString(
			link,
		) + "\">Continue</a></p>",
		TextBody: "Use this link to sign in or create an account:\n" + link,
	})
	if err != nil {
		slog.WarnContext(ctx, "send magic link email failed", "err", err)
		return jwt.ErrUnavailable
	}

	return nil
}

func (s *Service) Login(ctx context.Context, loginToken string) (models.User, jwt.AuthTokens, error) {
	digest, err := digestMagicToken(loginToken)
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrLoginFailed
	}

	now := s.cfg.Now().UTC()
	var (
		user   models.User
		tokens jwt.AuthTokens
	)
	err = s.deps.WithTransaction(ctx, func(tx Resources) error {
		challenge, err := tx.AuthTokens.Consume(ctx, digest, models.TokenPurposeLogin, now)
		if err != nil {
			return err
		}
		if challenge.UserID == nil {
			return store.ErrChallengeRejected
		}
		storedUser, err := tx.Users.GetByIDForUpdate(ctx, *challenge.UserID)
		if err != nil {
			return err
		}
		if storedUser.ID == uuid.Nil || !storedUser.Role.Valid() {
			return jwt.ErrLoginFailed
		}
		if err := requireAllowedEmailDomain(ctx, tx.DomainPolicy, storedUser.Email, storedUser.Role); err != nil {
			return err
		}
		user = *storedUser
		signed, err := s.createSession(ctx, tx, user, now)
		if err != nil {
			return err
		}
		tokens = signed
		return nil
	})
	if errors.Is(err, store.ErrChallengeRejected) || errors.Is(err, store.ErrNotFound) ||
		errors.Is(err, jwt.ErrLoginFailed) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrLoginFailed
	}
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, fmt.Errorf("login transaction: %w", err)
	}
	return user, tokens, nil
}

func (s *Service) Register(
	ctx context.Context,
	registrationToken string,
	profile jwt.Profile,
) (models.User, jwt.AuthTokens, error) {
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	if profile.DisplayName == "" || utf8.RuneCountInString(profile.DisplayName) > MaxDisplayNameLength {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrInvalidProfile
	}
	phone, ok := normalizedPhoneNumber(profile.PhoneNumber)
	if !ok {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrInvalidProfile
	}
	digest, err := digestMagicToken(registrationToken)
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRegistrationFailed
	}
	now := s.cfg.Now().UTC()
	var (
		user   models.User
		tokens jwt.AuthTokens
	)
	err = s.deps.WithTransaction(ctx, func(tx Resources) error {
		challenge, err := tx.AuthTokens.Consume(ctx, digest, models.TokenPurposeRegister, now)
		if err != nil {
			return err
		}
		if err := requireAllowedEmailDomain(ctx, tx.DomainPolicy, challenge.Email, models.RoleUser); err != nil {
			return err
		}
		user = models.User{Email: challenge.Email, DisplayName: profile.DisplayName,
			PhoneNumber: &phone, Role: models.RoleUser}
		provisioner := UserProvisioner{Users: tx.Users, Events: tx.UserCreatedEvents}
		if err := provisioner.Create(ctx, &user); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return jwt.ErrAlreadyRegistered
			}
			return err
		}
		signed, err := s.createSession(ctx, tx, user, now)
		if err != nil {
			return err
		}
		tokens = signed
		return nil
	})
	if errors.Is(err, store.ErrChallengeRejected) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRegistrationFailed
	}
	if errors.Is(err, jwt.ErrRegistrationFailed) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRegistrationFailed
	}
	if errors.Is(err, jwt.ErrAlreadyRegistered) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrAlreadyRegistered
	}
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, fmt.Errorf("registration transaction: %w", err)
	}
	return user, tokens, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (models.User, jwt.AuthTokens, error) {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, jwt.RefreshToken, now)
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	userID, err := parseID(claims.Subject)
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	sessionID, err := parseID(claims.SessionID)
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	var (
		tokens jwt.AuthTokens
		user   models.User
	)
	err = s.deps.WithTransaction(ctx, func(tx Resources) error {
		// Read the user under a row lock: a role change committing first must
		// be reflected in the rotated tokens, and one committing later sweeps
		// this session into its revocation.
		locked, err := tx.Users.GetByIDForUpdate(ctx, userID)
		if errors.Is(err, store.ErrNotFound) {
			return jwt.ErrRefreshFailed
		}
		if err != nil {
			return fmt.Errorf("find refresh user: %w", err)
		}
		if locked.ID != userID || !locked.Role.Valid() {
			return jwt.ErrRefreshFailed
		}
		user = *locked
		if err := requireAllowedEmailDomain(ctx, tx.DomainPolicy, user.Email, user.Role); err != nil {
			return err
		}
		if err := tx.Sessions.Revoke(ctx, sessionID, sha256.Sum256([]byte(refreshToken)), now); err != nil {
			return err
		}
		tokens, err = s.createSession(ctx, tx, user, now)
		return err
	})
	if errors.Is(err, store.ErrSessionRejected) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	if errors.Is(err, jwt.ErrRefreshFailed) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, fmt.Errorf("replace refresh session: %w", err)
	}
	// Rotation revokes the old session; its outstanding access tokens must die
	// with it instead of living out their remaining TTL.
	RecordRevokedSessions(ctx, s.deps.Revocations, []uuid.UUID{sessionID})
	return user, tokens, nil
}

// Logout is idempotent. An unrecognizable or already revoked cookie is cleared
// by the HTTP adapter; a valid cookie is revoked before success is returned.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, jwt.RefreshToken, now)
	if err != nil {
		return nil
	}
	sessionID, err := parseID(claims.SessionID)
	if err != nil {
		return nil
	}
	err = s.deps.Resources.Sessions.Revoke(ctx, sessionID, sha256.Sum256([]byte(refreshToken)), now)
	if errors.Is(err, store.ErrSessionRejected) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("revoke refresh session: %w", err)
	}
	RecordRevokedSessions(ctx, s.deps.Revocations, []uuid.UUID{sessionID})
	return nil
}

// LogoutAll revokes every session of the refresh cookie's user, signing them
// out of all devices. Unlike Logout, the cookie must belong to a live session,
// so an unauthenticated caller cannot sign anyone out.
func (s *Service) LogoutAll(ctx context.Context, refreshToken string) error {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, jwt.RefreshToken, now)
	if err != nil {
		return jwt.ErrRefreshFailed
	}
	userID, err := parseID(claims.Subject)
	if err != nil {
		return jwt.ErrRefreshFailed
	}
	sessionID, err := parseID(claims.SessionID)
	if err != nil {
		return jwt.ErrRefreshFailed
	}
	var revoked []uuid.UUID
	err = s.deps.WithTransaction(ctx, func(tx Resources) error {
		if err := tx.Sessions.Revoke(ctx, sessionID, sha256.Sum256([]byte(refreshToken)), now); err != nil {
			return err
		}
		ids, err := tx.Sessions.RevokeAllForUser(ctx, userID, now)
		if err != nil {
			return err
		}
		// RevokeAllForUser skips the just-revoked cookie session, so list it
		// explicitly; duplicate entries are harmless.
		revoked = append([]uuid.UUID{sessionID}, ids...)
		return nil
	})
	if errors.Is(err, store.ErrSessionRejected) {
		return jwt.ErrRefreshFailed
	}
	if err != nil {
		return fmt.Errorf("revoke all sessions: %w", err)
	}
	RecordRevokedSessions(ctx, s.deps.Revocations, revoked)
	return nil
}

func (s *Service) PublicKeys() (jwt.JWKSet, error) {
	return s.deps.TokenCodec.PublicKeys(), nil
}

func (s *Service) createSession(
	ctx context.Context,
	tx Resources,
	user models.User,
	now time.Time,
) (jwt.AuthTokens, error) {
	placeholderHash := make([]byte, sha256.Size)
	if _, err := rand.Read(placeholderHash); err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("generate session placeholder: %w", err)
	}
	session := models.Session{UserID: user.ID, TokenHash: placeholderHash,
		LastSeenAt: now, ExpiresAt: now.Add(s.cfg.RefreshTokenTTL)}
	if err := tx.Sessions.Create(ctx, &session); err != nil {
		return jwt.AuthTokens{}, err
	}
	tokens, err := s.signSessionTokens(user, session.ID, now)
	if err != nil {
		return jwt.AuthTokens{}, err
	}
	digest := sha256.Sum256([]byte(tokens.RefreshToken))
	if err := tx.Sessions.UpdateTokenHash(ctx, session.ID, digest[:]); err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("save refresh token hash: %w", err)
	}
	return tokens, nil
}

func (s *Service) signSessionTokens(user models.User, sessionID uuid.UUID, now time.Time) (jwt.AuthTokens, error) {
	role, ok := jwtRole(user.Role)
	if !ok {
		return jwt.AuthTokens{}, errors.New("user has invalid role")
	}
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
	subject := user.ID.String()
	sessionSubject := sessionID.String()
	access, err := s.deps.TokenCodec.Sign(jwt.Claims{Type: jwt.AccessToken, Subject: subject,
		SessionID: sessionSubject, Role: role, IssuedAt: now, ExpiresAt: accessExpiry, TokenID: accessID})
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("sign access token: %w", err)
	}
	refresh, err := s.deps.TokenCodec.Sign(jwt.Claims{Type: jwt.RefreshToken, Subject: subject,
		SessionID: sessionSubject, IssuedAt: now, ExpiresAt: refreshExpiry, TokenID: refreshID})
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("sign refresh token: %w", err)
	}
	return jwt.AuthTokens{AccessToken: access, RefreshToken: refresh,
		AccessExpiry: accessExpiry, RefreshExpiry: refreshExpiry}, nil
}

func parseID(value string) (uuid.UUID, error) {
	return uuid.Parse(value)
}

// RecordRevokedSessions lists sessions in the revocation dictionary after the
// database revocation committed. Failures are logged and swallowed: the
// revocation must not fail the operation that already revoked the session, and
// a missed entry only leaves the tokens valid until their own expiry. Callers
// are the auth service, the role service, and the bootstrap promotion.
func RecordRevokedSessions(
	ctx context.Context,
	revocations AccessTokenRevocations,
	sessionIDs []uuid.UUID,
) {
	if len(sessionIDs) == 0 {
		return
	}
	ids := make([]string, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		ids = append(ids, sessionID.String())
	}
	if err := revocations.Revoke(ctx, ids...); err != nil {
		slog.WarnContext(ctx, "record revoked access tokens", "err", err)
	}
}

func jwtRole(role models.RoleName) (jwt.Role, bool) {
	switch role {
	case models.RoleSuperAdmin:
		return jwt.RoleSuperAdmin, true
	case models.RoleAdmin:
		return jwt.RoleAdmin, true
	case models.RoleUser:
		return jwt.RoleUser, true
	case models.RoleSuspended:
		return jwt.RoleSuspendedUser, true
	default:
		return "", false
	}
}

// NormalizeEmail trims and lowercases a bare address, or returns jwt.ErrInvalidEmail.
func NormalizeEmail(input string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(input))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || strings.ContainsAny(email, "\r\n") {
		return "", jwt.ErrInvalidEmail
	}
	return email, nil
}

func requireAllowedEmailDomain(
	ctx context.Context,
	policy EmailDomainPolicy,
	input string,
	role models.RoleName,
) error {
	if role == models.RoleSuperAdmin {
		return nil
	}
	email, err := NormalizeEmail(input)
	if err != nil {
		return ErrEmailDomainNotAllowed
	}
	separator := strings.LastIndexByte(email, '@')
	if separator <= 0 || separator == len(email)-1 {
		return ErrEmailDomainNotAllowed
	}
	allowed, err := policy.IsDomainAllowed(ctx, email[separator+1:])
	if err != nil {
		return fmt.Errorf("check email domain: %w", err)
	}
	if !allowed {
		return ErrEmailDomainNotAllowed
	}
	return nil
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
