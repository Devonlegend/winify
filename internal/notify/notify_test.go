package notify

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func testEvent() DeployEvent {
	return DeployEvent{DeploymentID: 7, ProjectID: "proj-001", ProjectName: "Storefront", ServerID: "s1", Status: "success", CommitSHA: "abcdef1234567", Ref: "refs/heads/main", URL: "https://cc.example.com/projects/proj-001"}
}

func TestWebhookNotifierPostsJSON(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := &WebhookNotifier{URL: srv.URL}
	if err := n.NotifyDeploy(context.Background(), testEvent()); err != nil {
		t.Fatalf("NotifyDeploy: %v", err)
	}
	if body["project_id"] != "proj-001" || body["status"] != "success" {
		t.Fatalf("payload = %v", body)
	}
}

func TestSlackNotifierText(t *testing.T) {
	var body struct {
		Text string `json:"text"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := &SlackNotifier{URL: srv.URL}
	ev := testEvent()
	ev.Status = "failed"
	ev.Error = "build exploded"
	if err := n.NotifyDeploy(context.Background(), ev); err != nil {
		t.Fatalf("NotifyDeploy: %v", err)
	}
	if !strings.Contains(body.Text, "Storefront") || !strings.Contains(body.Text, "failed") || !strings.Contains(body.Text, "build exploded") {
		t.Fatalf("text = %q", body.Text)
	}
}

func TestGitHubNotifierSkipsWithoutRepo(t *testing.T) {
	n := &GitHubNotifier{PrivateKey: func(context.Context) (string, error) { return "", errors.New("must not be called") }}
	if err := n.NotifyDeploy(context.Background(), testEvent()); err != nil {
		t.Fatalf("NotifyDeploy: %v", err)
	}
}

func TestGitHubNotifierPostsStatus(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
			return
		}
		if strings.Contains(r.URL.Path, "/statuses/") {
			_ = json.NewDecoder(r.Body).Decode(&posted)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	n := &GitHubNotifier{
		AppID: "123", InstallationID: 42, APIURL: srv.URL,
		PrivateKey: func(context.Context) (string, error) { return testKey(t), nil },
	}
	ev := testEvent()
	ev.GitHubRepo = "acme/app"
	if err := n.NotifyDeploy(context.Background(), ev); err != nil {
		t.Fatalf("NotifyDeploy: %v", err)
	}
	if posted["state"] != "success" || posted["context"] != "winify/deploy" {
		t.Fatalf("status = %v", posted)
	}
}

func TestMultiFansOut(t *testing.T) {
	ok := &fakeNotifier{}
	bad := &fakeNotifier{err: errors.New("nope")}
	m := Multi{ok, bad}
	if err := m.NotifyDeploy(context.Background(), testEvent()); err == nil {
		t.Fatal("expected the failing notifier's error")
	}
	if ok.calls != 1 || bad.calls != 1 {
		t.Fatalf("calls = %d, %d, want 1 each", ok.calls, bad.calls)
	}
}

type fakeNotifier struct {
	calls int
	err   error
}

func (f *fakeNotifier) NotifyDeploy(context.Context, DeployEvent) error {
	f.calls++
	return f.err
}
