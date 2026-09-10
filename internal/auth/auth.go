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
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Devonlegend/winify/internal/models"
)

// sessionCookieName is the browser cookie holding the raw session token.
const sessionCookieName = "cc_session"

// ErrInvalidCredentials is returned for both "no such user" and "wrong
// password" so callers cannot distinguish the two.
var ErrInvalidCredentials = errors.New("invalid credentials")

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
}

// NewService builds the auth service. cookieSecure sets the cookie Secure flag
// (true in production; browsers still accept Secure cookies on localhost).
func NewService(store *models.Store, cookieSecure bool, ttl time.Duration) *Service {
	return &Service{store: store, cookieSecure: cookieSecure, ttl: ttl, now: time.Now}
}

// HashPassword returns a bcrypt hash suitable for storage or config.
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
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
	if err := s.store.CreateSession(ctx, hashToken(token), userID, expires); err != nil {
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
		if err := s.store.DeleteSession(ctx, hashToken(c.Value)); err != nil {
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
	username, err := s.store.UsernameBySession(ctx, hashToken(token), s.now())
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

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
