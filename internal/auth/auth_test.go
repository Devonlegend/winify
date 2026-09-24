package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/models"
)

func newTestService(t *testing.T) (*Service, *models.Store) {
	t.Helper()
	db, err := models.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := models.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := models.NewStore(db)

	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := store.UpsertUser(context.Background(), "admin", hash); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	return NewService(store, false, time.Hour), store
}

func TestLoginBackoff(t *testing.T) {
	svc := NewService(nil, false, time.Hour)
	for i := 0; i < 5; i++ {
		svc.RecordLoginFailure("ip|user")
	}
	if svc.LoginRetryAfter("ip|user") <= 0 {
		t.Fatal("login backoff was not applied after five failures")
	}
	svc.ClearLoginFailures("ip|user")
	if svc.LoginRetryAfter("ip|user") != 0 {
		t.Fatal("login backoff was not cleared")
	}
}

func TestAuthenticate(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Authenticate(ctx, "admin", "correct horse"); err != nil {
		t.Fatalf("valid login failed: %v", err)
	}
	if _, err := svc.Authenticate(ctx, "admin", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(ctx, "nobody", "whatever"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user err = %v, want ErrInvalidCredentials", err)
	}
}

func TestSessionLifecycleAndMiddleware(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	user, err := store.UserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("UserByUsername: %v", err)
	}

	rec := httptest.NewRecorder()
	if err := svc.StartSession(ctx, rec, user.ID); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if cookie.Value == "" {
		t.Fatal("session cookie value is empty")
	}

	protected := svc.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, ok := UserFromContext(r.Context())
		if !ok {
			t.Error("username missing from context")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(username))
	}))

	// With a valid cookie: allowed.
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(cookie)
	recOK := httptest.NewRecorder()
	protected.ServeHTTP(recOK, req)
	if recOK.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200", recOK.Code)
	}
	if recOK.Body.String() != "admin" {
		t.Fatalf("context user = %q, want admin", recOK.Body.String())
	}

	// Without a cookie: redirected to login.
	recNo := httptest.NewRecorder()
	protected.ServeHTTP(recNo, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if recNo.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated status = %d, want 303", recNo.Code)
	}
	if loc := recNo.Header().Get("Location"); loc != "/login" {
		t.Fatalf("redirect Location = %q, want /login", loc)
	}
}

func TestEndSessionRevokes(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()
	user, _ := store.UserByUsername(ctx, "admin")

	rec := httptest.NewRecorder()
	if err := svc.StartSession(ctx, rec, user.ID); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(cookie)
	if err := svc.EndSession(ctx, httptest.NewRecorder(), req); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	if _, ok := svc.validate(ctx, cookie.Value); ok {
		t.Fatal("session still valid after logout")
	}
}

func TestRegisterOnlyOnce(t *testing.T) {
	db, err := models.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := models.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	svc := NewService(models.NewStore(db), false, time.Hour)
	ctx := context.Background()

	if ok, err := svc.HasUsers(ctx); err != nil || ok {
		t.Fatalf("HasUsers = %v, %v; want false", ok, err)
	}
	user, err := svc.Register(ctx, "admin", "hunter2hunter2")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.Username != "admin" {
		t.Fatalf("user = %+v", user)
	}
	if ok, _ := svc.HasUsers(ctx); !ok {
		t.Fatal("HasUsers = false after registration")
	}
	if _, err := svc.Authenticate(ctx, "admin", "hunter2hunter2"); err != nil {
		t.Fatalf("Authenticate after register: %v", err)
	}

	// A second registration is closed and must not overwrite the admin.
	if _, err := svc.Register(ctx, "other", "anotherpass"); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("second Register err = %v, want ErrRegistrationClosed", err)
	}
	if _, err := svc.Authenticate(ctx, "admin", "hunter2hunter2"); err != nil {
		t.Fatalf("admin password changed after second register: %v", err)
	}
}
