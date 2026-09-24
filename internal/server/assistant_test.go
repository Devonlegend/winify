package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/assistant"
)

// fakeAssistant returns a fixed grounded answer so handler tests can assert the
// response shape without a real model.
type fakeAssistant struct {
	answer       assistant.Answer
	err          error
	lastQuestion string
}

func (f *fakeAssistant) Ask(_ context.Context, question string) (assistant.Answer, error) {
	f.lastQuestion = question
	if f.err != nil {
		return assistant.Answer{}, f.err
	}
	if f.answer.Answer == "" {
		return assistant.Answer{
			Answer:    "test answer",
			Sources:   []string{"doc: FAQ"},
			Intent:    "docs",
			Model:     "test-model",
			DocsUsed:  true,
			LatencyMS: 5,
		}, nil
	}
	return f.answer, nil
}

func TestAssistantAskEndpoint(t *testing.T) {
	s := newTestServer(t)
	cookie := login(t, s)

	req := httptest.NewRequest(http.MethodPost, "/assistant/ask", strings.NewReader(`{"question":"how do I roll back?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.Host)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "test answer") {
		t.Errorf("body missing answer: %s", body)
	}
	if !strings.Contains(body, "doc: FAQ") {
		t.Errorf("body missing sources: %s", body)
	}
}

func TestAssistantAskRejectsEmptyQuestion(t *testing.T) {
	s := newTestServer(t)
	cookie := login(t, s)

	req := httptest.NewRequest(http.MethodPost, "/assistant/ask", strings.NewReader(`{"question":"  "}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.Host)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAssistantAskRequiresAuth(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodPost, "/assistant/ask")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect to login", rec.Code)
	}
}

func TestAssistantPageRenders(t *testing.T) {
	s := newTestServer(t)
	cookie := login(t, s)
	rec := getWithCookie(t, s, "/assistant", cookie)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "chat-form") {
		t.Error("assistant page missing chat form")
	}
}
