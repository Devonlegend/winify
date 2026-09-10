package deployment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func signGitHub(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyGitHubSignature(t *testing.T) {
	const secret = "topsecret"
	body := []byte(`{"ref":"refs/heads/main","after":"abc123"}`)
	valid := signGitHub(secret, body)

	if err := VerifyGitHubSignature(secret, body, valid); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}

	cases := []struct {
		name   string
		secret string
		body   []byte
		header string
	}{
		{"wrong secret", secret, body, signGitHub("other", body)},
		{"tampered body", secret, []byte(`{"ref":"refs/heads/evil"}`), valid},
		{"missing header", secret, body, ""},
		{"malformed hex", secret, body, "sha256=nothex"},
		{"wrong algorithm (sha1)", secret, body, "sha1=deadbeef"},
		{"empty secret", "", body, valid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := VerifyGitHubSignature(tc.secret, tc.body, tc.header); !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("err = %v, want ErrInvalidSignature", err)
			}
		})
	}
}

func TestVerifyGitLabToken(t *testing.T) {
	if err := VerifyGitLabToken("tok", "tok"); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	for _, tc := range []struct{ secret, token string }{
		{"tok", "wrong"},
		{"tok", ""},
		{"", ""},
	} {
		if err := VerifyGitLabToken(tc.secret, tc.token); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("VerifyGitLabToken(%q,%q) = %v, want ErrInvalidSignature", tc.secret, tc.token, err)
		}
	}
}

func TestParsePushPayloads(t *testing.T) {
	gh, err := ParseGitHubPush([]byte(`{"ref":"refs/heads/main","after":"abc123"}`))
	if err != nil {
		t.Fatalf("ParseGitHubPush: %v", err)
	}
	if gh.Ref != "refs/heads/main" || gh.Commit != "abc123" {
		t.Fatalf("github event = %+v", gh)
	}

	gl, err := ParseGitLabPush([]byte(`{"ref":"refs/heads/dev","checkout_sha":"def456","after":"def456"}`))
	if err != nil {
		t.Fatalf("ParseGitLabPush: %v", err)
	}
	if gl.Ref != "refs/heads/dev" || gl.Commit != "def456" {
		t.Fatalf("gitlab event = %+v", gl)
	}

	if got := BranchOf("refs/heads/feature/x"); got != "feature/x" {
		t.Fatalf("BranchOf = %q", got)
	}
}
