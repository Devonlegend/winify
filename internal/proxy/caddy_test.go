package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCaddyReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := NewCaddy(srv.URL, "srv0").Reachable(context.Background()); err != nil {
		t.Fatalf("Reachable: %v", err)
	}
}

func TestCaddyReachableFailsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := NewCaddy(srv.URL, "srv0").Reachable(context.Background()); err == nil {
		t.Fatal("Reachable succeeded against a 500, want an error")
	}
}

func TestRouteIDDoesNotCollideOnPunctuation(t *testing.T) {
	if routeID("a.example.com") == routeID("a-example-com") {
		t.Fatal("route IDs collided")
	}
}

func TestCaddyRegisterUpsertsRoute(t *testing.T) {
	var posted []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/config/apps/http/servers/srv0":
			w.WriteHeader(http.StatusNotFound) // force server creation
		case r.Method == http.MethodPut && r.URL.Path == "/config/apps/http/servers/srv0":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/config/apps/http/servers/srv0/routes":
			posted, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := NewCaddy(srv.URL, "srv0").Register(context.Background(), "app.example.com", "127.0.0.1:8000"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	body := string(posted)
	for _, want := range []string{"app.example.com", "127.0.0.1:8000", "reverse_proxy"} {
		if !strings.Contains(body, want) {
			t.Errorf("route body missing %q: %s", want, body)
		}
	}
}
