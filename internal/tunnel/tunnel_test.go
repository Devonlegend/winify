package tunnel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProvisionCloudflareCreatesEverything(t *testing.T) {
	var calls []string
	var ingressBody, dnsBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer cf-token-secret" {
			t.Errorf("authorization = %q", got)
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			writeCF(w, []map[string]string{{"id": "zone1", "name": "example.com"}})
		case r.Method == http.MethodGet && r.URL.Path == "/accounts/acct1/cfd_tunnel":
			writeCF(w, []map[string]string{})
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/acct1/cfd_tunnel":
			writeCF(w, map[string]string{"id": "tun1"})
		case r.Method == http.MethodGet && r.URL.Path == "/accounts/acct1/cfd_tunnel/tun1/token":
			writeCF(w, "connector-token")
		case r.Method == http.MethodPut && r.URL.Path == "/accounts/acct1/cfd_tunnel/tun1/configurations":
			ingressBody, _ = io.ReadAll(r.Body)
			writeCF(w, map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone1/dns_records":
			writeCF(w, []map[string]string{})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone1/dns_records":
			dnsBody, _ = io.ReadAll(r.Body)
			writeCF(w, map[string]string{"id": "rec1"})
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &client{baseURL: srv.URL, token: "cf-token-secret", http: srv.Client()}
	res, err := c.provisionCloudflare(context.Background(), Config{
		Domain:     "example.com",
		AccountID:  "acct1",
		TunnelName: "winify",
		APIToken:   "cf-token-secret",
		Origin:     "http://localhost:80",
	}, nil)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if res.TunnelID != "tun1" || res.ConnectToken != "connector-token" || res.Hostname != "*.example.com" || res.DNSRecordID != "rec1" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(string(ingressBody), "*.example.com") || !strings.Contains(string(ingressBody), "http://localhost:80") {
		t.Errorf("ingress body = %s", ingressBody)
	}
	if !strings.Contains(string(dnsBody), "tun1.cfargotunnel.com") || !strings.Contains(string(dnsBody), `"proxied":true`) {
		t.Errorf("dns body = %s", dnsBody)
	}
}

func TestProvisionCloudflareIsIdempotent(t *testing.T) {
	var createdTunnel, createdDNS bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			writeCF(w, []map[string]string{{"id": "zone1", "name": "example.com"}})
		case r.Method == http.MethodGet && r.URL.Path == "/accounts/acct1/cfd_tunnel":
			writeCF(w, []map[string]string{{"id": "tun1", "name": "winify"}})
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/acct1/cfd_tunnel":
			createdTunnel = true
			writeCF(w, map[string]string{"id": "tun-new"})
		case r.Method == http.MethodGet && r.URL.Path == "/accounts/acct1/cfd_tunnel/tun1/token":
			writeCF(w, "connector-token")
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/configurations"):
			writeCF(w, map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone1/dns_records":
			writeCF(w, []map[string]string{{"id": "rec1", "name": "*.example.com"}})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone1/dns_records":
			createdDNS = true
			writeCF(w, map[string]string{"id": "rec-new"})
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &client{baseURL: srv.URL, token: "cf-token-secret", http: srv.Client()}
	res, err := c.provisionCloudflare(context.Background(), Config{
		Domain: "example.com", AccountID: "acct1", TunnelName: "winify", APIToken: "cf-token-secret",
	}, nil)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if createdTunnel {
		t.Error("created a tunnel when one already existed")
	}
	if createdDNS {
		t.Error("created a DNS record when one already existed")
	}
	if res.TunnelID != "tun1" || res.DNSRecordID != "rec1" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestProvisionRejectsMissingToken(t *testing.T) {
	if _, err := Provision(context.Background(), Config{Domain: "example.com", AccountID: "a", TunnelName: "winify"}, nil); err == nil {
		t.Fatal("expected an error for a missing API token")
	}
}

func TestProvisionZoneNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCF(w, []map[string]string{})
	}))
	defer srv.Close()
	c := &client{baseURL: srv.URL, token: "t", http: srv.Client()}
	if _, err := c.provisionCloudflare(context.Background(), Config{
		Domain: "example.com", AccountID: "a", TunnelName: "winify", APIToken: "t",
	}, nil); err == nil {
		t.Fatal("expected a zone-not-found error")
	}
}

func writeCF(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
}
