// Package server wires the HTTP listener: chi routing, middleware, template
// rendering and the handlers that serve the dashboard and health endpoints.
package server

import (
	"database/sql"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/static"
)

// Server holds the dependencies shared by every handler: configuration, the
// database handle and the parsed template set. Handlers are methods on
// *Server so they can reach these without globals.
type Server struct {
	cfg    config.Config
	db     *sql.DB
	tmpl   *template.Template
	assets fs.FS
}

// New parses the templates and prepares the asset FS. An error here means the
// embedded templates are malformed, so the process should fail fast at boot.
func New(cfg config.Config, db *sql.DB) (*Server, error) {
	tmpl, err := template.ParseFS(static.Templates, "templates/*.html")
	if err != nil {
		return nil, err
	}

	sub, err := fs.Sub(static.Web, "web")
	if err != nil {
		return nil, err
	}

	return &Server{cfg: cfg, db: db, tmpl: tmpl, assets: sub}, nil
}

// Handler builds the router. We use chi rather than the stdlib mux because
// wildcard mounts like /assets/* and method-scoped subrouters (added in later
// phases) read more clearly with chi's middleware chain.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(requestLogger)

	r.Get("/", s.handleDashboard)
	r.Get("/healthz", s.handleHealthz)
	r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(s.assets))))

	return r
}

// render executes the shared "layout" template, which pulls the page-specific
// "title" and "body" blocks in from whichever template defined them.
func (s *Server) render(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		// Headers are already sent; the best we can do is log the failure.
		log.Printf("render layout: %v", err)
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

// requestLogger is middleware that logs one line per request with method,
// path, status and latency.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Microsecond))
	})
}
