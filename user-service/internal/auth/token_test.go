package auth

import (
	"bytes"
	"testing"
)

func TestTokenRoundTrip(t *testing.T) {
	raw, hash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 43 || len(hash) != 32 {
		t.Fatalf("raw=%d hash=%d", len(raw), len(hash))
	}
	got, ok := hashToken(raw)
	if !ok || !bytes.Equal(got, hash) {
		t.Fatal("hash mismatch")
	}
	raw2, _, _ := newToken()
	if raw2 == raw {
		t.Fatal("tokens not random")
	}
}

func TestHashToken_Malformed(t *testing.T) {
	for _, raw := range []string{"", "abc", "not base64 !!", "AAAA"} {
		if _, ok := hashToken(raw); ok {
			t.Errorf("%q accepted", raw)
		}
	}
}
