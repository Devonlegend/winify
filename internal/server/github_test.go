package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
)

func TestGitHubConnectDisabledWhenNotConfigured(t *testing.T) {
	s, store := newTestServerWithStore(t)
	seedWebhookProject(t, store)
	cookie := login(t, s)
	rec := postForm(t, s, "/projects/proj-001/webhook/github", url.Values{"repo": {"acme/app"}}, cookie)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "not%20configured") {
		t.Fatalf("status = %d location = %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestGitHubConnectValidatesRepo(t *testing.T) {
	s, store := newTestServerWithStore(t)
	seedWebhookProject(t, store)
	s.connectGitHub = func(context.Context, config.Project, string, string) error { return nil }
	cookie := login(t, s)
	rec := postForm(t, s, "/projects/proj-001/webhook/github", url.Values{"repo": {"not-a-repo"}}, cookie)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "owner%2Frepo") {
		t.Fatalf("status = %d location = %q, want owner/repo validation error", rec.Code, rec.Header().Get("Location"))
	}
}

func TestGitHubConnectPassesParams(t *testing.T) {
	s, store := newTestServerWithStore(t)
	seedWebhookProject(t, store)
	var gotRepo, gotBase string
	s.connectGitHub = func(_ context.Context, _ config.Project, ownerRepo, baseURL string) error {
		gotRepo, gotBase = ownerRepo, baseURL
		return nil
	}
	cookie := login(t, s)
	rec := postForm(t, s, "/projects/proj-001/webhook/github", url.Values{"repo": {"acme/app"}}, cookie)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "webhook%20connected") {
		t.Fatalf("status = %d location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if gotRepo != "acme/app" || !strings.HasPrefix(gotBase, "http://") {
		t.Errorf("repo = %q base = %q", gotRepo, gotBase)
	}
}

func TestGitHubConnectSurfacesErrors(t *testing.T) {
	s, store := newTestServerWithStore(t)
	seedWebhookProject(t, store)
	s.connectGitHub = func(context.Context, config.Project, string, string) error {
		return errors.New("installation token failed")
	}
	cookie := login(t, s)
	rec := postForm(t, s, "/projects/proj-001/webhook/github", url.Values{"repo": {"acme/app"}}, cookie)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "setup%20failed") {
		t.Fatalf("status = %d location = %q", rec.Code, rec.Header().Get("Location"))
	}
}
