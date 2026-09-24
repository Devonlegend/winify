package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/bootstrap"
)

func TestOnboardDisabledWhenNotConfigured(t *testing.T) {
	s := newTestServer(t)
	cookie := login(t, s)
	rec := postForm(t, s, "/servers/onboard", url.Values{
		"endpoint": {"win01"}, "user": {"u"}, "password": {"p"},
	}, cookie)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestOnboardValidation(t *testing.T) {
	s := newTestServer(t)
	s.onboardWindows = func(context.Context, WindowsOnboardParams) (string, []bootstrap.Result, error) {
		return "remote-win01", nil, nil
	}
	cookie := login(t, s)

	// Missing password.
	rec := postForm(t, s, "/servers/onboard", url.Values{"endpoint": {"win01"}, "user": {"u"}}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing password: status = %d, want 400", rec.Code)
	}
	// HTTP endpoint requires the insecure opt-in.
	rec = postForm(t, s, "/servers/onboard", url.Values{
		"endpoint": {"http://win01:5985/wsman"}, "user": {"u"}, "password": {"p"},
	}, cookie)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "insecure") {
		t.Fatalf("http endpoint: status = %d, want 400 with insecure hint", rec.Code)
	}
	// Bad ID.
	rec = postForm(t, s, "/servers/onboard", url.Values{
		"endpoint": {"win01"}, "user": {"u"}, "password": {"p"}, "id": {"../bad"},
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: status = %d, want 400", rec.Code)
	}
}

func TestOnboardSuccessPassesNormalizedParams(t *testing.T) {
	s := newTestServer(t)
	var got WindowsOnboardParams
	s.onboardWindows = func(_ context.Context, p WindowsOnboardParams) (string, []bootstrap.Result, error) {
		got = p
		return "remote-win01", []bootstrap.Result{{Name: "dirs", Status: bootstrap.StatusApplied}}, nil
	}
	cookie := login(t, s)
	rec := postForm(t, s, "/servers/onboard", url.Values{
		"endpoint": {"win01"}, "user": {"deploy"}, "password": {"secret"}, "provision_caddy": {"on"},
	}, cookie)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "Onboarded") {
		t.Fatalf("status = %d location = %q, want redirect", rec.Code, rec.Header().Get("Location"))
	}
	if got.Endpoint != "https://win01:5986/wsman" {
		t.Errorf("endpoint = %q, want normalized https", got.Endpoint)
	}
	if !got.ProvisionCaddy {
		t.Error("provision_caddy not propagated")
	}
}

func TestOnboardFailureNamesStep(t *testing.T) {
	s := newTestServer(t)
	s.onboardWindows = func(context.Context, WindowsOnboardParams) (string, []bootstrap.Result, error) {
		return "", []bootstrap.Result{{Name: "caddy", Status: bootstrap.StatusError, Error: "boom"}}, errors.New("download failed")
	}
	cookie := login(t, s)
	rec := postForm(t, s, "/servers/onboard", url.Values{
		"endpoint": {"win01"}, "user": {"u"}, "password": {"p"},
	}, cookie)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "caddy") {
		t.Fatalf("status = %d, want 502 naming the failed step (body: %s)", rec.Code, rec.Body.String())
	}
}
