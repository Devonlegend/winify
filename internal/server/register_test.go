package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestFirstRunRedirectsToRegister(t *testing.T) {
	s, _ := newTestServerNoUsers(t)

	rec := doRequest(t, s, http.MethodGet, "/login")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/register" {
		t.Fatalf("GET /login = %d %q, want 303 /register", rec.Code, rec.Header().Get("Location"))
	}

	rec = doRequest(t, s, http.MethodGet, "/register")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Create admin account") {
		t.Fatalf("GET /register = %d, want 200 registration page", rec.Code)
	}
}

func TestRegisterCreatesAdminAndSignsIn(t *testing.T) {
	s, store := newTestServerNoUsers(t)

	rec := postForm(t, s, "/register", url.Values{
		"username": {"admin"},
		"password": {"hunter2hunter2"},
		"confirm":  {"hunter2hunter2"},
	}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard" {
		t.Fatalf("register = %d %q, want 303 /dashboard", rec.Code, rec.Header().Get("Location"))
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("register did not set a session cookie")
	}
	if _, err := store.UserByUsername(context.Background(), "admin"); err != nil {
		t.Fatalf("admin was not created: %v", err)
	}

	// Once an account exists, registration is closed.
	rec = doRequest(t, s, http.MethodGet, "/register")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("GET /register after setup = %d %q, want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
	rec = postForm(t, s, "/register", url.Values{
		"username": {"intruder"}, "password": {"anotherpass"}, "confirm": {"anotherpass"},
	}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("second register = %d, want redirect", rec.Code)
	}
	if _, err := store.UserByUsername(context.Background(), "intruder"); err == nil {
		t.Fatal("second registration created a user")
	}
}

func TestRegisterRequiresSetupToken(t *testing.T) {
	s, _ := newTestServerNoUsers(t)
	s.setupToken = "setup_test_token"

	form := url.Values{
		"username": {"admin"},
		"password": {"hunter2hunter2"},
		"confirm":  {"hunter2hunter2"},
	}
	rec := postForm(t, s, "/register", form, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("register without token = %d, want 403", rec.Code)
	}

	form.Set("setup_token", s.setupToken)
	rec = postForm(t, s, "/register", form, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("register with token = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestRegisterValidation(t *testing.T) {
	s, _ := newTestServerNoUsers(t)

	rec := postForm(t, s, "/register", url.Values{
		"username": {"admin"}, "password": {"short"}, "confirm": {"short"},
	}, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "at least 8 characters") {
		t.Fatalf("short password = %d, want 400 with message", rec.Code)
	}

	rec = postForm(t, s, "/register", url.Values{
		"username": {"admin"}, "password": {"hunter2hunter2"}, "confirm": {"mismatchpass"},
	}, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "do not match") {
		t.Fatalf("mismatched confirm = %d, want 400 with message", rec.Code)
	}
}
