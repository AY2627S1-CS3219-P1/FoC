// Package provideridentity binds immutable moderation settings to their endpoint.
package provideridentity

import (
	"crypto/sha256"
	"encoding/hex"
)

func Fingerprint(baseURL, modelID string) string {
	sum := sha256.Sum256([]byte(baseURL + "\x00" + modelID))
	return hex.EncodeToString(sum[:])
}
