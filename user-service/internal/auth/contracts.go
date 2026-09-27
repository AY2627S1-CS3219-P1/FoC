// Package auth owns the authentication rules and the interfaces consumed by
// those rules.
package auth

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth/tokenclaims"
)

var (
	ErrNotFound           = errors.New("user not found")
	ErrChallengeRejected  = errors.New("challenge invalid, expired, or used")
	ErrAlreadyRegistered  = errors.New("email already registered")
	ErrSessionRejected    = errors.New("session invalid or revoked")
	ErrInvalidEmail       = errors.New("invalid email address")
	ErrInvalidProfile     = errors.New("invalid registration profile")
	ErrLoginFailed        = errors.New("login verification failed")
	ErrRegistrationFailed = errors.New("registration verification failed")
	ErrRefreshFailed      = errors.New("refresh failed")
	ErrUnavailable        = errors.New("service unavailable")
)

type Role = tokenclaims.Role

const (
	RoleSuperAdmin    = tokenclaims.RoleSuperAdmin
	RoleAdmin         = tokenclaims.RoleAdmin
	RoleUser          = tokenclaims.RoleUser
	RoleSuspendedUser = tokenclaims.RoleSuspendedUser
)

type User struct {
	ID          string
	Email       string
	DisplayName string
	Role        Role
}

type Profile struct {
	DisplayName string
}

type LoginChallenge struct {
	Digest    [32]byte
	UserID    string
	ExpiresAt time.Time
}

type RegistrationChallenge struct {
	Digest    [32]byte
	Email     string
	ExpiresAt time.Time
}

type Session struct {
	ID            string
	UserID        string
	RefreshDigest [32]byte
	ExpiresAt     time.Time
}

// UserStore is defined at its consumer. FindByEmail receives a lowercase,
// trimmed email. Both lookups return ErrNotFound when the account is absent.
type UserStore interface {
	FindByEmail(context.Context, string) (User, error)
	FindByID(context.Context, string) (User, error)
}

// SessionFactory signs tokens for a user chosen by a valid magic link. The
// adapter persists the returned session in the same transaction as link use.
type SessionFactory func(User) (Session, AuthTokens, error)

// LoginTokenStore keeps challenges independently. Complete atomically
// validates the digest, loads the linked user, invokes the factory with that
// user, stores the session, and consumes the link. On failure it rolls back.
type LoginTokenStore interface {
	Save(context.Context, LoginChallenge) error
	Complete(context.Context, [32]byte, time.Time, SessionFactory) (User, AuthTokens, error)
}

// RegistrationTokenStore has separate storage from LoginTokenStore. Complete
// atomically validates the digest, creates a RoleUser using the email from the
// challenge, invokes the factory, stores the session, and consumes the link.
// On failure it rolls back all changes. Rejected links return
// ErrChallengeRejected; duplicate users return ErrAlreadyRegistered.
type RegistrationTokenStore interface {
	Save(context.Context, RegistrationChallenge) error
	Complete(context.Context, [32]byte, Profile, time.Time, SessionFactory) (User, AuthTokens, error)
}

// SessionStore operations must compare the currently stored refresh digest.
// Rotate and Revoke return ErrSessionRejected for a reused, expired, or revoked token.
type SessionStore interface {
	Rotate(context.Context, string, [32]byte, [32]byte, time.Time, time.Time) error
	Revoke(context.Context, string, [32]byte, time.Time) error
}

type TokenType = tokenclaims.TokenUse

const (
	AccessToken  = tokenclaims.AccessToken
	RefreshToken = tokenclaims.RefreshToken
)

type Claims struct {
	Type      TokenType
	Subject   string
	SessionID string
	Role      Role
	IssuedAt  time.Time
	ExpiresAt time.Time
	TokenID   string
}

type JWK struct {
	KeyType   string `json:"kty"`
	Curve     string `json:"crv"`
	X         string `json:"x"`
	Y         string `json:"y"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

type JWKSet struct {
	Keys []JWK `json:"keys"`
}

type TokenCodec interface {
	Sign(Claims) (string, error)
	Verify(string, TokenType, time.Time) (Claims, error)
	PublicKeys() JWKSet
}

type Dependencies struct {
	Users              UserStore
	LoginTokens        LoginTokenStore
	RegistrationTokens RegistrationTokenStore
	Sessions           SessionStore
	TokenCodec         TokenCodec
	EmailSender        email.EmailSender
}

type Config struct {
	FrontendBaseURL  url.URL
	LocalDevelopment bool
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	Now              func() time.Time
}

// AuthTokens must only be written as HttpOnly cookies by the HTTP adapter.
type AuthTokens struct {
	AccessToken   string    `json:"-"`
	RefreshToken  string    `json:"-"`
	AccessExpiry  time.Time `json:"-"`
	RefreshExpiry time.Time `json:"-"`
}
