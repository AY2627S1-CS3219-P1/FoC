package jwt

import (
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
)

// VerifyAccess produces the identity attached to protected requests.
func (c *ES256Codec) VerifyAccess(token string) (auth.Caller, error) {
	claims, err := c.Verify(token, AccessToken, time.Now())
	if err != nil {
		return auth.Caller{}, err
	}
	role := string(claims.Role)
	return auth.Caller{ID: claims.Subject, SessionID: claims.SessionID, Role: role,
		Admin: role == "admin" || role == "super_admin", IssuedAt: claims.IssuedAt,
		ExpiresAt: claims.ExpiresAt, TokenID: claims.TokenID}, nil
}
