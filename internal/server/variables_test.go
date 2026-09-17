package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSharedVariablesCRUD(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)

	rec := postForm(t, s, "/variables", url.Values{
		"scope": {"project"}, "scope_id": {"Default"},
		"key": {"DATABASE_URL"}, "value": {"postgres://db"},
	}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	vars, err := store.ListSharedVariables(context.Background())
	if err != nil || len(vars) != 1 || vars[0].Key != "DATABASE_URL" {
		t.Fatalf("variables = %+v, %v", vars, err)
	}

	rec = getWithCookie(t, s, "/variables", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "DATABASE_URL") {
		t.Fatalf("variables page = %d, want 200 listing the variable", rec.Code)
	}

	rec = postForm(t, s, "/variables/delete", url.Values{
		"scope": {"project"}, "scope_id": {"Default"}, "key": {"DATABASE_URL"},
	}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d, want 303", rec.Code)
	}
	if vars, _ := store.ListSharedVariables(context.Background()); len(vars) != 0 {
		t.Fatalf("variable not deleted: %+v", vars)
	}
}

func TestSharedVariableValidation(t *testing.T) {
	s, _ := newTestServerWithStore(t)
	cookie := login(t, s)

	rec := postForm(t, s, "/variables", url.Values{
		"scope": {"bogus"}, "scope_id": {"x"}, "key": {"K"}, "value": {"v"},
	}, cookie)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Scope must be") {
		t.Fatalf("bad scope = %d, want 400 with message", rec.Code)
	}
}
