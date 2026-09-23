package bootstrap

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicIPv4Reason(t *testing.T) {
	cases := []struct {
		ip        string
		wantEmpty bool
	}{
		{"8.8.8.8", true},
		{"135.129.125.245", true},
		{"10.0.0.5", false},
		{"192.168.1.195", false},
		{"172.16.5.4", false},
		{"100.64.0.1", false},
		{"100.75.247.55", false},
		{"127.0.0.1", false},
		{"169.254.1.1", false},
		{"2605:59c0:e4f:d508::1", false},
		{"not-an-ip", false},
	}
	for _, c := range cases {
		got := publicIPv4Reason(c.ip)
		if c.wantEmpty && got != "" {
			t.Errorf("publicIPv4Reason(%s) = %q, want empty", c.ip, got)
		}
		if !c.wantEmpty && got == "" {
			t.Errorf("publicIPv4Reason(%s) = empty, want a reason", c.ip)
		}
	}
}

func TestLookupPublicIPv4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.9\n"))
	}))
	defer srv.Close()

	old := publicIPEchoURL
	publicIPEchoURL = srv.URL
	defer func() { publicIPEchoURL = old }()

	got, err := lookupPublicIPv4(context.Background(), net.ParseIP("127.0.0.1"))
	if err != nil {
		t.Fatalf("lookupPublicIPv4: %v", err)
	}
	if got != "203.0.113.9" {
		t.Fatalf("ip = %q, want 203.0.113.9", got)
	}
}
