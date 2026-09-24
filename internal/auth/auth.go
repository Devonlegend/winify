// Package auth owns the single-admin login, session cookies and the encrypted
// credential store. This is security-sensitive code: it verifies passwords and
// decrypts stored secrets. It must never log a password, a session token, a
// cookie value or a decrypted credential.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Devonlegend/winify/internal/models"
)

// sessionCookieName is the browser cookie holding the raw session token.
const sessionCookieName = "cc_session"

// ErrInvalidCredentials is returned for both "no such user" and "wrong
// password" so callers cannot distinguish the two.
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrRegistrationClosed is returned when the first-run registration is
// attempted after an admin account already exists.
var ErrRegistrationClosed = errors.New("registration is closed")

// dummyHash is compared against when the user does not exist, so a missing
// user takes the same time as a wrong password (mitigates user enumeration).
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)

type contextKey int

const userContextKey contextKey = iota

// Service coordinates login, session lifetime and the auth middleware.
type Service struct {
	store        *models.Store
	cookieSecure bool
	ttl          time.Duration
	now          func() time.Time // injectable clock for tests

	loginMu       sync.Mutex
	loginFailures map[string]loginAttempt
}

type loginAttempt struct {
	count       int
	first       time.Time
	lockedUntil time.Time
}

// NewService builds the auth service. cookieSecure sets the cookie Secure flag
// (true in production; browsers still accept Secure cookies on localhost).
func NewService(store *models.Store, cookieSecure bool, ttl time.Duration) *Service {
	return &Service{store: store, cookieSecure: cookieSecure, ttl: ttl, now: time.Now, loginFailures: make(map[string]loginAttempt)}
}

// HashPassword returns a bcrypt hash suitable for storage or config.
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// HasUsers reports whether any admin account exists. The HTTP layer uses it to
// route first-run visitors to registration instead of login.
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// LoginRetryAfter applies a small in-memory backoff to repeated password
// failures. It protects the single-admin login without creating a durable
// account-lockout state that an operator cannot clear.
func (s *Service) LoginRetryAfter(key string) time.Duration {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := s.now()
	attempt, ok := s.loginFailures[key]
	if !ok {
		return 0
	}
	if now.Before(attempt.lockedUntil) {
		return attempt.lockedUntil.Sub(now)
	}
	if now.Sub(attempt.first) > 15*time.Minute {
		delete(s.loginFailures, key)
	}
	return 0
}

func (s *Service) RecordLoginFailure(key string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := s.now()
	attempt := s.loginFailures[key]
	if attempt.first.IsZero() || now.Sub(attempt.first) > 15*time.Minute {
		attempt = loginAttempt{first: now}
	}
	attempt.count++
	if attempt.count >= 5 {
		attempt.lockedUntil = now.Add(5 * time.Minute)
	}
	s.loginFailures[key] = attempt
}

func (s *Service) ClearLoginFailures(key string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	delete(s.loginFailures, key)
}

// Register creates the first admin account and returns it. It is only valid
// while no account exists: once one does, it returns ErrRegistrationClosed and
// never overwrites the existing account.
func (s *Service) Register(ctx context.Context, username, password string) (models.User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return models.User{}, err
	}
	user, err := s.store.CreateFirstUser(ctx, username, hash)
	if errors.Is(err, models.ErrAlreadyInitialized) {
		return models.User{}, ErrRegistrationClosed
	}
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

// Authenticate verifies a username/password pair. The error never contains the
// password or hash.
func (s *Service) Authenticate(ctx context.Context, username, password string) (models.User, error) {
	user, err := s.store.UserByUsername(ctx, username)
	if errors.Is(err, models.ErrNotFound) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password)) // equalize timing
		return models.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return models.User{}, fmt.Errorf("authenticate: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return models.User{}, ErrInvalidCredentials
	}
	return user, nil
}

// StartSession creates a session row and sets the session cookie. The raw token
// only ever exists in the cookie; the database stores its SHA-256 hash.
func (s *Service) StartSession(ctx context.Context, w http.ResponseWriter, userID int64) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	expires := s.now().Add(s.ttl)
	if err := s.store.CreateSession(ctx, HashToken(token), userID, expires); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.ttl.Seconds()),
	})
	return nil
}

// EndSession deletes the current session and clears the cookie.
func (s *Service) EndSession(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if err := s.store.DeleteSession(ctx, HashToken(c.Value)); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	return nil
}

// RequireAuth is middleware that rejects unauthenticated requests. API paths
// get a 401; page requests are redirected to the login screen.
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err == nil {
			if username, ok := s.validate(r.Context(), c.Value); ok {
				ctx := context.WithValue(r.Context(), userContextKey, username)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

// validate resolves a raw cookie token to a username, or false when the session
// is unknown or expired.
func (s *Service) validate(ctx context.Context, token string) (string, bool) {
	username, err := s.store.UsernameBySession(ctx, HashToken(token), s.now())
	if err != nil {
		return "", false
	}
	return username, true
}

// UserFromContext returns the authenticated username placed there by
// RequireAuth.
func UserFromContext(ctx context.Context) (string, bool) {
	username, ok := ctx.Value(userContextKey).(string)
	return username, ok
}

// HashToken returns the hex SHA-256 of a token. Session and API tokens are
// stored only as this hash, so a database leak exposes no usable token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
