package service

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	userdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/user"
	"github.com/google/uuid"
)

type Service struct {
	deps Dependencies
	cfg  Config
}

type UserStore interface {
	GetByEmail(context.Context, string) (*models.User, error)
	GetByID(context.Context, uuid.UUID) (*models.User, error)
	// GetByIDForUpdate locks the user row until the transaction ends, so a
	// concurrent role change either committed (new role is read) or waits
	// (its revocation sweep then covers this transaction's session).
	GetByIDForUpdate(context.Context, uuid.UUID) (*models.User, error)
	Create(context.Context, *models.User) error
}

type AuthTokenStore interface {
	Create(context.Context, *models.AuthToken) error
	Consume(context.Context, [32]byte, models.TokenPurpose, time.Time) (*models.AuthToken, error)
}

type SessionStore interface {
	Create(context.Context, *models.Session) error
	UpdateTokenHash(context.Context, uuid.UUID, []byte) error
	Revoke(context.Context, uuid.UUID, [32]byte, time.Time) error
	RevokeAllForUser(context.Context, uuid.UUID, time.Time) ([]uuid.UUID, error)
}

// AccessTokenRevocations records sessions in the shared revocation dictionary
// so every service rejects their outstanding access tokens.
type AccessTokenRevocations interface {
	Revoke(ctx context.Context, sessionIDs ...string) error
}

// DiscardRevocations accepts publication calls without recording them. It is
// only correct for callers whose revoked sessions cannot have outstanding
// access tokens, such as the demo seeder revoking fixture sessions it just
// created; production composition must always supply the shared store.
type DiscardRevocations struct{}

func (DiscardRevocations) Revoke(context.Context, ...string) error { return nil }

// EmailDomainPolicy reports membership in the email-domain allowlist.
type EmailDomainPolicy interface {
	IsDomainAllowed(context.Context, string) (bool, error)
}

type UserCreatedEventStore interface {
	Append(context.Context, userdomain.CreatedEvent) error
}

type Resources struct {
	Users             UserStore
	AuthTokens        AuthTokenStore
	Sessions          SessionStore
	DomainPolicy      EmailDomainPolicy
	UserCreatedEvents UserCreatedEventStore
}

type WithTransaction func(context.Context, func(Resources) error) error

// TokenCodec is the signing and verification logic consumed by Service.
type TokenCodec interface {
	Sign(jwt.Claims) (string, error)
	Verify(string, jwt.TokenType, time.Time) (jwt.Claims, error)
	PublicKeys() jwt.JWKSet
}

type Dependencies struct {
	Resources       Resources
	WithTransaction WithTransaction
	TokenCodec      TokenCodec
	EmailSender     email.EmailSender
	Revocations     AccessTokenRevocations
}

type Config struct {
	FrontendBaseURL  url.URL
	LocalDevelopment bool
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	Now              func() time.Time
}

func NewService(deps Dependencies, cfg Config) (*Service, error) {
	base := cfg.FrontendBaseURL
	if base.Host == "" || base.User != nil || base.Opaque != "" ||
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
	return &Service{deps: deps, cfg: cfg}, nil
}
