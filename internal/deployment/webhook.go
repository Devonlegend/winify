package deployment

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidSignature is returned when a webhook fails verification. Callers
// must reject the request.
var ErrInvalidSignature = errors.New("invalid webhook signature")

// VerifyGitHubSignature checks the X-Hub-Signature-256 header ("sha256=<hex>")
// against the raw request body using HMAC-SHA256 and a constant-time compare.
func VerifyGitHubSignature(secret string, body []byte, signatureHeader string) error {
	const prefix = "sha256="
	if secret == "" || !strings.HasPrefix(signatureHeader, prefix) {
		return ErrInvalidSignature
	}
	got, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, prefix))
	if err != nil {
		return ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return ErrInvalidSignature
	}
	return nil
}

// VerifyGitLabToken checks the X-Gitlab-Token header with a constant-time
// compare. GitLab push webhooks use a shared secret token, not an HMAC.
func VerifyGitLabToken(secret, token string) error {
	if secret == "" || subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
		return ErrInvalidSignature
	}
	return nil
}

// PushEvent is the normalized result of a provider push payload.
type PushEvent struct {
	Ref    string // full ref, e.g. refs/heads/main
	Commit string
}

// ParseGitHubPush extracts the ref and head commit from a GitHub push payload.
func ParseGitHubPush(body []byte) (PushEvent, error) {
	var p struct {
		Ref   string `json:"ref"`
		After string `json:"after"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return PushEvent{}, fmt.Errorf("parse github push: %w", err)
	}
	return PushEvent{Ref: p.Ref, Commit: p.After}, nil
}

// ParseGitLabPush extracts the ref and head commit from a GitLab push payload.
// GitLab sets checkout_sha for normal pushes; after is the fallback.
func ParseGitLabPush(body []byte) (PushEvent, error) {
	var p struct {
		Ref         string `json:"ref"`
		After       string `json:"after"`
		CheckoutSHA string `json:"checkout_sha"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return PushEvent{}, fmt.Errorf("parse gitlab push: %w", err)
	}
	commit := p.CheckoutSHA
	if commit == "" {
		commit = p.After
	}
	return PushEvent{Ref: p.Ref, Commit: commit}, nil
}

// BranchOf strips the refs/heads/ prefix, returning the branch name.
func BranchOf(ref string) string { return strings.TrimPrefix(ref, "refs/heads/") }
