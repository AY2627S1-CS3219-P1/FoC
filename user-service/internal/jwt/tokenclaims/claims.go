// Package tokenclaims defines the JWT claims shared by token issuers and
// service middleware.
package tokenclaims

import (
	"errors"

	"github.com/golang-jwt/jwt/v5"
)

type TokenUse string

const (
	AccessToken  TokenUse = "access"
	RefreshToken TokenUse = "refresh"
)

type Role string

const (
	RoleSuperAdmin    Role = "super_admin"
	RoleAdmin         Role = "admin"
	RoleUser          Role = "user"
	RoleSuspendedUser Role = "suspended_user"
)

func (r Role) Valid() bool {
	switch r {
	case RoleSuperAdmin, RoleAdmin, RoleUser, RoleSuspendedUser:
		return true
	default:
		return false
	}
}

// Claims is the JWT wire format. expectedUse is supplied by the verifier and
// is deliberately excluded from the token payload.
type Claims struct {
	jwt.RegisteredClaims
	SessionID string   `json:"sid"`
	TokenUse  TokenUse `json:"token_use"`
	Role      Role     `json:"role,omitempty"`

	expectedUse TokenUse
}

func New(expectedUse TokenUse) *Claims {
	return &Claims{expectedUse: expectedUse}
}

// Validate implements jwt.ClaimsValidator for application-specific claims.
func (c Claims) Validate() error {
	if c.expectedUse != AccessToken && c.expectedUse != RefreshToken {
		return errors.New("invalid expected token use")
	}
	if c.TokenUse != c.expectedUse {
		return errors.New("unexpected token use")
	}
	if c.Subject == "" || c.SessionID == "" || c.ID == "" {
		return errors.New("required token identifier is missing")
	}
	if c.IssuedAt == nil || c.ExpiresAt == nil || c.NotBefore == nil {
		return errors.New("required token timestamp is missing")
	}
	if c.IssuedAt.Unix() <= 0 || c.ExpiresAt.Unix() <= c.IssuedAt.Unix() ||
		c.NotBefore.Unix() != c.IssuedAt.Unix() {
		return errors.New("invalid token lifetime")
	}
	if c.expectedUse == AccessToken && !c.Role.Valid() {
		return errors.New("invalid access token role")
	}
	if c.expectedUse == RefreshToken && c.Role != "" {
		return errors.New("refresh token must not contain a role")
	}
	return nil
}
