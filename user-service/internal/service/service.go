package service

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

type Service struct {
	deps Dependencies
	cfg  Config
}

type UserStore interface {
	GetByEmail(context.Context, string) (*models.User, error)
	GetByID(context.Context, uuid.UUID) (*models.User, error)
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
}

type DomainStore interface {
	Allows(context.Context, string) (bool, error)
}

type Store struct {
	Users      UserStore
	AuthTokens AuthTokenStore
	Sessions   SessionStore
	Domains    DomainStore
}

type WithTransaction func(context.Context, func(Store) error) error

// TokenCodec is the signing and verification logic consumed by Service.
type TokenCodec interface {
	Sign(jwt.Claims) (string, error)
	Verify(string, jwt.TokenType, time.Time) (jwt.Claims, error)
	PublicKeys() jwt.JWKSet
}

type Dependencies struct {
	Store           Store
	WithTransaction WithTransaction
	TokenCodec      TokenCodec
	EmailSender     email.EmailSender
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
