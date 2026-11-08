package tokenclaims

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func validClaims(use TokenUse) Claims {
	now := time.Now()
	return Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "subject",
			ID:        "token-id",
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
		SessionID:   "session-id",
		TokenUse:    use,
		Role:        RoleUser,
		expectedUse: use,
	}
}

// TestRefreshTokenRejectsLifecycleState proves a refresh cookie can never carry
// the deleting flag or a role: the replacement session re-derives lifecycle
// state from the live account, so a refresh token asserting it must be rejected.
func TestRefreshTokenRejectsLifecycleState(t *testing.T) {
	c := validClaims(RefreshToken)
	c.Role = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("valid refresh token rejected: %v", err)
	}
	c.IsDeleting = true
	if err := c.Validate(); err == nil {
		t.Fatal("refresh token with is_del was accepted")
	}
	c.IsDeleting = false
	c.Role = RoleUser
	if err := c.Validate(); err == nil {
		t.Fatal("refresh token with a role was accepted")
	}
}

// TestAccessTokenCarriesLifecycleState proves an access token may carry is_del.
func TestAccessTokenCarriesLifecycleState(t *testing.T) {
	c := validClaims(AccessToken)
	if err := c.Validate(); err != nil {
		t.Fatalf("valid access token rejected: %v", err)
	}
	c.IsDeleting = true
	if err := c.Validate(); err != nil {
		t.Fatalf("access token with is_del rejected: %v", err)
	}
	c.Role = ""
	if err := c.Validate(); err == nil {
		t.Fatal("access token without a role was accepted")
	}
}
