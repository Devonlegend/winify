// Package server wires the HTTP listener: chi routing, middleware, template
// rendering and the handlers that serve the dashboard, login flow, deploy
// history, webhook receiver and health endpoints. Dashboard and API routes are
// wrapped by auth.RequireAuth; webhook routes are public but HMAC-verified.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Devonlegend/winify/internal/assistant"
	"github.com/Devonlegend/winify/internal/auth"
	"github.com/Devonlegend/winify/internal/bootstrap"
	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/models"
	"github.com/Devonlegend/winify/internal/proxy"
	"github.com/Devonlegend/winify/internal/static"
)

// pageTemplates are the page files parsed alongside layout.html and partials.html.
var pageTemplates = []string{"login", "register", "dashboard", "deployment", "monitoring", "assistant", "servers", "projects", "project_new", "project_wizard", "resource", "credentials", "variables", "setup", "tokens"}

// Deployer is the subset of *deployment.Deployer the HTTP layer uses, so tests
// can substitute a fake.
type Deployer interface {
	Trigger(ctx context.Context, project config.Project, srv config.Server, trigger, commit, ref string) (int64, error)
	Rollback(ctx context.Context, project config.Project, srv config.Server) (int64, error)
}

// Assistant is the subset of *assistant.Service the HTTP layer uses.
type Assistant interface {
	Ask(ctx context.Context, question string) (assistant.Answer, error)
}

// CredentialAdmin manages encrypted credentials from the dashboard. Values are
// write-only: they are never read back into a response.
type CredentialAdmin interface {
	Put(ctx context.Context, name, secret string) error
	Delete(ctx context.Context, name string) error
}

// Deps are the server's dependencies.
type Deps struct {
	Cfg             config.Config
	Store           *models.Store
	Auth            *auth.Service
	Secrets         deployment.SecretResolver
	Deployer        Deployer
	Assistant       Assistant
	CredentialAdmin CredentialAdmin
	// RunnerFactory opens a connection to a target for repository detection.
	// Nil disables the detect endpoint.
	RunnerFactory deployment.RunnerFactory
	// Bootstrap backs the Setup page. Nil disables it.
	Bootstrap *bootstrap.Bootstrap
	// BootstrapRun applies bootstrap steps now. Only safe when already elevated.
	BootstrapRun func(ctx context.Context) error
	// BootstrapElevate relaunches bootstrap elevated (UAC prompt).
	BootstrapElevate func() error
	// BootstrapElevated reports whether the process can apply steps directly.
	// Nil falls back to a live elevation check.
	BootstrapElevated func(ctx context.Context) (bool, error)
	// SetupToken protects first-run registration when the listener is exposed
	// beyond loopback. An empty token is allowed for tests and embedded uses;
	// the production entrypoint always supplies one on a fresh database.
	SetupToken string
	// Proxy is used to remove stale public routes when projects are deleted or
	// their domain changes. Nil disables cleanup in tests/embedded uses.
	Proxy proxy.Registrar
}

// Server holds the dependencies shared by every handler.
type Server struct {
	cfg               config.Config
	store             *models.Store
	auth              *auth.Service
	secrets           deployment.SecretResolver
	deployer          Deployer
	assistant         Assistant
	credentials       CredentialAdmin
	newRunner         deployment.RunnerFactory
	proxy             proxy.Registrar
	bootstrap         *bootstrap.Bootstrap
	bootstrapRun      func(ctx context.Context) error
	bootstrapElevate  func() error
	bootstrapElevated func(ctx context.Context) (bool, error)
	setupToken        string
	pages             map[string]*template.Template
	assets            fs.FS
}

// templateFuncs are available to every page template.
var templateFuncs = template.FuncMap{"navLabel": navLabel}

// navLabel maps a page's active key to its sidebar/topbar label.
func navLabel(active string) string {
	switch active {
	case "dashboard":
		return "Dashboard"
	case "deployment":
		return "Deployments"
	case "projects":
		return "Projects"
	case "servers":
		return "Servers"
	case "monitoring":
		return "Monitoring"
	case "assistant":
		return "Assistant"
	case "credentials":
		return "Credentials"
	case "variables":
		return "Shared variables"
	case "setup":
		return "Setup"
	case "tokens":
		return "API tokens"
	default:
		return ""
	}
}

// New parses the templates and prepares the asset FS. Each page is parsed as
// its own set (layout + page) because every page defines the same "title" and
// "body" block names; parsing them together would let later files overwrite
// earlier ones. An error here means the embedded templates are malformed, so
// the process should fail fast at boot.
func New(deps Deps) (*Server, error) {
	pages := make(map[string]*template.Template, len(pageTemplates))
	for _, name := range pageTemplates {
		t, err := template.New(name).Funcs(templateFuncs).ParseFS(static.Templates,
			"templates/layout.html", "templates/partials.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse page %q: %w", name, err)
		}
		pages[name] = t
	}

	sub, err := fs.Sub(static.Web, "web")
	if err != nil {
		return nil, err
	}

	return &Server{
		cfg:               deps.Cfg,
		store:             deps.Store,
		auth:              deps.Auth,
		secrets:           deps.Secrets,
		deployer:          deps.Deployer,
		assistant:         deps.Assistant,
		credentials:       deps.CredentialAdmin,
		newRunner:         deps.RunnerFactory,
		proxy:             deps.Proxy,
		bootstrap:         deps.Bootstrap,
		bootstrapRun:      deps.BootstrapRun,
		bootstrapElevate:  deps.BootstrapElevate,
		bootstrapElevated: deps.BootstrapElevated,
		setupToken:        deps.SetupToken,
		pages:             pages,
		assets:            sub,
	}, nil
}

// Handler builds the router. Public routes are health, login, static assets and
// the HMAC-verified webhooks; everything else sits behind auth.RequireAuth.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(requestLogger)
	r.Use(requireSameOrigin)

	r.Get("/healthz", s.handleHealthz)
	r.Get("/login", s.handleLoginForm)
	r.Post("/login", s.handleLogin)
	// First-run setup. Both handlers redirect to /login once an admin exists.
	r.Get("/register", s.handleRegisterForm)
	r.Post("/register", s.handleRegister)
	r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(s.assets))))

	// Webhooks are authenticated by their per-project signature, not a session.
	r.Post("/webhooks/github/{projectID}", s.handleGitHubWebhook)
	r.Post("/webhooks/gitlab/{projectID}", s.handleGitLabWebhook)

	// The REST API is authenticated by bearer token, not a session.
	r.Route("/api/v1", s.apiRoutes)

	r.Group(func(r chi.Router) {
		r.Use(s.auth.RequireAuth)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		})
		r.Get("/dashboard", s.handleDashboard)
		r.Get("/deployment", s.handleDeployment)
		r.Post("/deployment/rollback/{projectID}", s.handleRollback)
		r.Get("/monitoring", s.handleMonitoring)
		r.Get("/assistant", s.handleAssistant)
		r.Post("/assistant/ask", s.handleAssistantAsk)

		r.Get("/servers", s.handleServersPage)
		r.Post("/servers", s.handleServerSave)
		r.Post("/servers/delete", s.handleServerDelete)
		r.Get("/projects", s.handleProjectsPage)
		r.Get("/projects/new", s.handleProjectNew)
		r.Post("/projects/detect", s.handleProjectDetect)
		r.Get("/projects/{projectID}", s.handleResourcePage)
		r.Post("/projects", s.handleProjectSave)
		r.Post("/projects/delete", s.handleProjectDelete)
		r.Post("/projects/env", s.handleProjectEnvSave)
		r.Post("/projects/deploy/{projectID}", s.handleManualDeploy)
		r.Post("/deployments/start/{projectID}", s.handleDeployStart)
		r.Get("/deployments/{id}/stream", s.handleDeploymentStream)
		r.Get("/credentials", s.handleCredentialsPage)
		r.Post("/credentials", s.handleCredentialSave)
		r.Post("/credentials/delete", s.handleCredentialDelete)
		r.Get("/variables", s.handleVariablesPage)
		r.Post("/variables", s.handleVariableSave)
		r.Post("/variables/delete", s.handleVariableDelete)
		r.Get("/setup", s.handleSetupPage)
		r.Post("/setup/run", s.handleSetupRun)
		r.Post("/setup/local-target", s.handleSetupLocalTarget)
		r.Get("/tokens", s.handleTokensPage)
		r.Post("/tokens", s.handleTokenCreate)
		r.Post("/tokens/delete", s.handleTokenDelete)

		r.Post("/logout", s.handleLogout)

		r.Get("/api/projects/{projectID}/deployments", s.handleAPIDeployments)
		r.Get("/api/deployments/{id}", s.handleAPIDeployment)
		r.Get("/api/metrics", s.handleAPIMetrics)
		r.Get("/api/servers/{id}/metrics", s.handleAPIServerMetrics)
		r.Get("/api/audit", s.handleAPIAudit)
	})

	return r
}

// requireSameOrigin is a lightweight CSRF boundary for browser mutations.
// Requests without Origin/Referer are left alone for CLI clients and the
// existing HMAC/webhook and bearer-token APIs; browser form submissions send
// one of these headers and must remain same-host.
func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || r.Method == http.MethodTrace ||
			strings.HasPrefix(r.URL.Path, "/api/v1/") || strings.HasPrefix(r.URL.Path, "/webhooks/") {
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			origin = strings.TrimSpace(r.Header.Get("Referer"))
		}
		if origin == "" {
			// A cookie-authenticated browser mutation without same-origin
			// metadata is rejected. Unauthenticated login/registration and
			// non-browser API clients can continue to the handler.
			if _, err := r.Cookie("cc_session"); err == nil {
				http.Error(w, "same-origin request metadata required", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// render executes the shared "layout" template for the named page.
func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "unknown page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		// Headers are already sent; the best we can do is log the failure.
		log.Printf("render %s: %v", page, err)
	}
}

// writeJSON encodes v as a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

// statusRecorder wraps http.ResponseWriter so the logger can see the status
// code a handler wrote. Embedding the interface lets it keep acting like a
// ResponseWriter with no extra code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer so streaming (SSE) works through the
// logging middleware.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// requestLogger logs one line per request. It logs method, path, status and
// latency only — never query strings or headers, which could carry secrets.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Microsecond))
	})
}
