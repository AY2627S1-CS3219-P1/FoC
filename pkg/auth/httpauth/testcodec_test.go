package httpauth

import (
	"crypto/ecdsa"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type testKey struct {
	KeyType, Curve, X, Y, Use, Algorithm, KeyID string
}
type testJWKSet struct{ Keys []testKey }
type testClaims struct {
	Type, Subject, SessionID, Role, TokenID string
	IssuedAt, ExpiresAt                     time.Time
}
type testCodec struct {
	key                   *ecdsa.PrivateKey
	kid, issuer, audience string
}

func newTestCodec(key *ecdsa.PrivateKey, kid, issuer, audience string) (*testCodec, error) {
	if key == nil {
		return nil, errors.New("missing key")
	}
	return &testCodec{key, kid, issuer, audience}, nil
}
func (c *testCodec) PublicKeys() testJWKSet {
	encode := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	x, y := make([]byte, 32), make([]byte, 32)
	c.key.PublicKey.X.FillBytes(x)
	c.key.PublicKey.Y.FillBytes(y)
	return testJWKSet{Keys: []testKey{{"EC", "P-256", encode(x), encode(y), "sig", "ES256", c.kid}}}
}
func (c *testCodec) Sign(claims testClaims) (string, error) {
	payload := jwt.MapClaims{"iss": c.issuer, "aud": c.audience, "sub": claims.Subject,
		"sid": claims.SessionID, "jti": claims.TokenID, "token_use": claims.Type,
		"iat": claims.IssuedAt.Unix(), "nbf": claims.IssuedAt.Unix(), "exp": claims.ExpiresAt.Unix()}
	if claims.Role != "" {
		payload["role"] = claims.Role
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, payload)
	token.Header["kid"] = c.kid
	return token.SignedString(c.key)
}
