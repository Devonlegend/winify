// Package server wires the HTTP listener: chi routing, middleware, template
// rendering and the handlers that serve the dashboard, login flow and health
// endpoints. Dashboard and API routes are wrapped by auth.RequireAuth.
package server

import (
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Devonlegend/winify/internal/auth"
	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
	"github.com/Devonlegend/winify/internal/static"
)

// pageTemplates are the page files parsed alongside layout.html.
var pageTemplates = []string{"login", "dashboard", "deployment", "monitoring", "assistant"}

// Server holds the dependencies shared by every handler: configuration, the
// data store, the auth service and one parsed template set per page.
type Server struct {
	cfg    config.Config
	store  *models.Store
	auth   *auth.Service
	pages  map[string]*template.Template
	assets fs.FS
}

// New parses the templates and prepares the asset FS. Each page is parsed as
// its own set (layout + page) because every page defines the same "title" and
// "body" block names; parsing them together would let later files overwrite
// earlier ones. An error here means the embedded templates are malformed, so
// the process should fail fast at boot.
func New(cfg config.Config, store *models.Store, authSvc *auth.Service) (*Server, error) {
	pages := make(map[string]*template.Template, len(pageTemplates))
	for _, name := range pageTemplates {
		t, err := template.ParseFS(static.Templates, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse page %q: %w", name, err)
		}
		pages[name] = t
	}

	sub, err := fs.Sub(static.Web, "web")
	if err != nil {
		return nil, err
	}

	return &Server{cfg: cfg, store: store, auth: authSvc, pages: pages, assets: sub}, nil
}

// Handler builds the router. Public routes are health, login and static assets;
// everything else sits behind auth.RequireAuth.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(requestLogger)

	r.Get("/healthz", s.handleHealthz)
	r.Get("/login", s.handleLoginForm)
	r.Post("/login", s.handleLogin)
	r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(s.assets))))

	r.Group(func(r chi.Router) {
		r.Use(s.auth.RequireAuth)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		})
		r.Get("/dashboard", s.handleDashboard)
		r.Get("/deployment", s.handleDeployment)
		r.Get("/monitoring", s.handleMonitoring)
		r.Get("/assistant", s.handleAssistant)
		r.Post("/logout", s.handleLogout)
	})

	return r
}

// render executes the shared "layout" template for the named page.
func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "unknown page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		// Headers are already sent; the best we can do is log the failure.
		log.Printf("render %s: %v", page, err)
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
