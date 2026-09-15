package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// NewAPIToken returns a new random API token and the hash to store. The
// plaintext is shown to the operator once and never persisted; the database
// keeps only HashToken(plaintext).
func NewAPIToken() (plaintext, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate api token: %w", err)
	}
	plaintext = "cc_" + base64.RawURLEncoding.EncodeToString(raw)
	return plaintext, HashToken(plaintext), nil
}
