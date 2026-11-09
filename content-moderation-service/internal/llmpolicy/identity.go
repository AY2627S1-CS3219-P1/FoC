package llmpolicy

import (
	"crypto/sha256"
	"encoding/hex"
)

// ProviderFingerprint binds a configuration to its provider endpoint and model.
func ProviderFingerprint(baseURL, modelID string) string {
	sum := sha256.Sum256([]byte(baseURL + "\x00" + modelID))
	return hex.EncodeToString(sum[:])
}
