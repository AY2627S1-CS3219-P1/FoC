package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth/tokenclaims"
	"github.com/golang-jwt/jwt/v5"
)

var errInvalidJWT = errors.New("invalid JWT")

type ES256Codec struct {
	private  *ecdsa.PrivateKey
	kid      string
	issuer   string
	audience string
}

func NewES256Codec(key *ecdsa.PrivateKey, kid, issuer, audience string) (*ES256Codec, error) {
	if key == nil || key.Curve != elliptic.P256() ||
		strings.TrimSpace(kid) == "" || strings.TrimSpace(issuer) == "" || strings.TrimSpace(audience) == "" {
		return nil, errors.New("invalid ES256 codec configuration")
	}
	if _, err := key.PublicKey.Bytes(); err != nil {
		return nil, errors.New("invalid ES256 codec configuration")
	}
	return &ES256Codec{private: key, kid: kid, issuer: issuer, audience: audience}, nil
}

func LoadPrivateKeyPEM(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read signing key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid PEM signing key")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, errors.New("signing key must be unencrypted PKCS#8 PEM (BEGIN PRIVATE KEY)")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse signing key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("signing key must be ECDSA P-256")
	}
	return key, nil
}

func (c *ES256Codec) Sign(claims Claims) (string, error) {
	if claims.Subject == "" || claims.SessionID == "" || claims.TokenID == "" ||
		(claims.Type != AccessToken && claims.Type != RefreshToken) ||
		claims.ExpiresAt.Unix() <= claims.IssuedAt.Unix() ||
		(claims.Type == AccessToken && !claims.Role.Valid()) ||
		(claims.Type == RefreshToken && claims.Role != "") {
		return "", errors.New("invalid JWT claims")
	}
	payload := jwt.MapClaims{
		"iss": c.issuer, "aud": c.audience, "sub": claims.Subject,
		"iat": claims.IssuedAt.Unix(), "nbf": claims.IssuedAt.Unix(),
		"exp": claims.ExpiresAt.Unix(), "jti": claims.TokenID,
		"sid": claims.SessionID, "token_use": claims.Type,
	}
	if claims.Role != "" {
		payload["role"] = claims.Role
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, payload)
	token.Header["kid"] = c.kid
	signed, err := token.SignedString(c.private)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signed, nil
}

func (c *ES256Codec) Verify(token string, expected TokenType, now time.Time) (Claims, error) {
	if expected != AccessToken && expected != RefreshToken {
		return Claims{}, errInvalidJWT
	}
	if len(token) > 8192 {
		return Claims{}, errInvalidJWT
	}
	payload := tokenclaims.New(expected)
	parsed, err := jwt.ParseWithClaims(token, payload, func(t *jwt.Token) (any, error) {
		if t.Header["typ"] != "JWT" || t.Header["kid"] != c.kid {
			return nil, errInvalidJWT
		}
		return &c.private.PublicKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(c.issuer), jwt.WithAudience(c.audience),
		jwt.WithExpirationRequired(), jwt.WithNotBeforeRequired(),
		jwt.WithIssuedAt(), jwt.WithTimeFunc(func() time.Time { return now }),
		jwt.WithStrictDecoding())
	if err != nil || !parsed.Valid {
		return Claims{}, errInvalidJWT
	}
	return Claims{Type: expected, Subject: payload.Subject, SessionID: payload.SessionID,
		Role: payload.Role, IssuedAt: payload.IssuedAt.Time.UTC(),
		ExpiresAt: payload.ExpiresAt.Time.UTC(), TokenID: payload.ID}, nil
}

func (c *ES256Codec) PublicKeys() JWKSet {
	publicKey, err := c.private.PublicKey.Bytes()
	if err != nil {
		return JWKSet{}
	}
	x, y := publicKey[1:33], publicKey[33:65]
	return JWKSet{Keys: []JWK{{KeyType: "EC", Curve: "P-256",
		X: base64.RawURLEncoding.EncodeToString(x), Y: base64.RawURLEncoding.EncodeToString(y),
		Use: "sig", Algorithm: "ES256", KeyID: c.kid}}}
}
