package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const tokenBytes = 32

// newToken returns a URL-safe random token and the SHA-256 hash to store.
func newToken() (raw string, hash []byte, err error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	h := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(b), h[:], nil
}

// hashToken validates the shape of a raw token and returns its hash.
// ok=false means malformed (U2.1.4) and never touches the DB.
func hashToken(raw string) (hash []byte, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != tokenBytes {
		return nil, false
	}
	h := sha256.Sum256(b)
	return h[:], true
}
