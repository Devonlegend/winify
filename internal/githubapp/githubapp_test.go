package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func TestAppJWTSignsWithAppID(t *testing.T) {
	jwt, err := appJWT("12345", testPEM(t), time.Now())
	if err != nil {
		t.Fatalf("appJWT: %v", err)
	}
	if len(strings.Split(jwt, ".")) != 3 {
		t.Fatalf("jwt shape = %d parts", len(strings.Split(jwt, ".")))
	}
}

func TestInstallationTokenAndEnsureHook(t *testing.T) {
	var sawAuth string
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		sawAuth = r.Header.Get("Authorization")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/42/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "inst-token"})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/hooks":
			_ = json.NewEncoder(w).Encode([]Hook{})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/hooks":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/tests"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	tok, err := c.InstallationToken(context.Background(), "12345", testPEM(t), 42)
	if err != nil {
		t.Fatalf("InstallationToken: %v", err)
	}
	if tok != "inst-token" || sawAuth == "" || !strings.HasPrefix(sawAuth, "Bearer ") {
		t.Fatalf("token = %q auth = %q", tok, sawAuth)
	}
	hook, err := c.EnsurePushHook(context.Background(), tok, "acme/app", "https://cc.example.com/webhooks/github/proj-001", "s3cr3t")
	if err != nil {
		t.Fatalf("EnsurePushHook: %v", err)
	}
	if hook.ID != 7 {
		t.Fatalf("hook id = %d", hook.ID)
	}
	if err := c.PingHook(context.Background(), tok, "acme/app", hook.ID); err != nil {
		t.Fatalf("PingHook: %v", err)
	}
}

func TestEnsurePushHookUpdatesExisting(t *testing.T) {
	var patched bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 9, "events": []string{"push"}, "config": map[string]string{"url": "https://cc.example.com/webhooks/github/proj-001"}},
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/app/hooks/9":
			patched = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if _, err := c.EnsurePushHook(context.Background(), "tok", "acme/app", "https://cc.example.com/webhooks/github/proj-001", "new-secret"); err != nil {
		t.Fatalf("EnsurePushHook: %v", err)
	}
	if !patched {
		t.Fatal("existing hook was not patched")
	}
}

func TestEnsurePushHookValidatesRepo(t *testing.T) {
	c := NewClient("")
	if _, err := c.EnsurePushHook(context.Background(), "tok", "not-a-repo", "https://x", "s"); err == nil {
		t.Fatal("invalid owner/repo accepted")
	}
}
