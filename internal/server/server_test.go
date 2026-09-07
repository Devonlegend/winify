package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := models.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := models.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	srv, err := New(config.Default(), db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func doRequest(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDashboardRenders(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodGet, "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "DevOps Control Center") {
		t.Fatalf("body missing title: %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), ">ok<") {
		t.Fatalf("body missing db status: %q", rec.Body.String())
	}
}

func TestHealthzOK(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodGet, "/healthz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("body = %q, want ok status", rec.Body.String())
	}
}

func TestAssetServed(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodGet, "/assets/css/app.css")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "background") {
		t.Fatalf("body = %q, want css content", rec.Body.String())
	}
}
