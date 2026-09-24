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

// NewWebhookSecret creates a random shared secret for a provider webhook.
// It is stored encrypted in the credential store and used for HMAC checks.
func NewWebhookSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// NewSetupToken creates the one-time token required by the first-run
// registration page. It is intentionally separate from API tokens so setup
// credentials can be rotated independently and are never stored in the API
// token table.
func NewSetupToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate setup token: %w", err)
	}
	return "setup_" + base64.RawURLEncoding.EncodeToString(raw), nil
}
