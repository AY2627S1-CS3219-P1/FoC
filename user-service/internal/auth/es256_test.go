package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
)

func TestES256Codec(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := NewES256Codec(key, "key-1", "issuer", "audience")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	token, err := codec.Sign(Claims{Type: AccessToken, Subject: "u1", SessionID: "s1",
		Role: RoleAdmin, IssuedAt: now, ExpiresAt: now.Add(service.AccessTokenLifetime), TokenID: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := codec.Verify(token, AccessToken, now)
	if err != nil || claims.Subject != "u1" || claims.Role != RoleAdmin {
		t.Fatalf("verify access token: %+v, %v", claims, err)
	}
	if _, err := codec.Verify(token, RefreshToken, now); err == nil {
		t.Fatal("access token accepted as refresh token")
	}
	if _, err := codec.Verify(token, AccessToken, now.Add(service.AccessTokenLifetime)); err == nil {
		t.Fatal("expired token accepted")
	}
	refreshToken, err := codec.Sign(Claims{Type: RefreshToken, Subject: "u1", SessionID: "s1",
		IssuedAt: now, ExpiresAt: now.Add(service.RefreshTokenLifetime), TokenID: "j2"})
	if err != nil {
		t.Fatal(err)
	}
	refreshClaims, err := codec.Verify(refreshToken, RefreshToken, now)
	if err != nil || refreshClaims.Subject != "u1" || refreshClaims.Role != "" {
		t.Fatalf("verify refresh token: %+v, %v", refreshClaims, err)
	}
	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	fields["role"] = "super_admin"
	changed, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(changed) + "." + parts[2]
	if _, err := codec.Verify(tampered, AccessToken, now); err == nil {
		t.Fatal("tampered token accepted")
	}
	other, err := NewES256Codec(key, "key-1", "issuer", "other-audience")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Verify(token, AccessToken, now); err == nil {
		t.Fatal("wrong audience accepted")
	}
	keys := codec.PublicKeys()
	if len(keys.Keys) != 1 || keys.Keys[0].KeyID != "key-1" || keys.Keys[0].Algorithm != "ES256" {
		t.Fatalf("unexpected JWKS: %+v", keys)
	}
	x, err := base64.RawURLEncoding.DecodeString(keys.Keys[0].X)
	if err != nil || len(x) != 32 {
		t.Fatalf("invalid JWKS x coordinate: %v", err)
	}
	y, err := base64.RawURLEncoding.DecodeString(keys.Keys[0].Y)
	if err != nil || len(y) != 32 {
		t.Fatalf("invalid JWKS y coordinate: %v", err)
	}
	public := ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("invalid JWT signature: %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&public, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("published JWKS cannot verify the access JWT")
	}
}
