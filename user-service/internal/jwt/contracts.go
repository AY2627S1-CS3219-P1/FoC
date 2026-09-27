// Package auth owns authentication domain types, roles, and errors.
package jwt

import (
	"errors"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt/tokenclaims"
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

// AuthTokens must only be written as HttpOnly cookies by the HTTP adapter.
type AuthTokens struct {
	AccessToken   string    `json:"-"`
	RefreshToken  string    `json:"-"`
	AccessExpiry  time.Time `json:"-"`
	RefreshExpiry time.Time `json:"-"`
}
