package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Devonlegend/winify/internal/auth"
	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/models"
)

const maxAPIBody = 64 << 10 // 64 KiB

// apiRoutes registers the token-authenticated REST API under /api/v1.
func (s *Server) apiRoutes(r chi.Router) {
	r.Use(s.apiAuth)

	r.Get("/servers", s.apiListServers)
	r.Get("/servers/{id}", s.apiGetServer)
	r.Post("/servers", s.apiSaveServer)
	r.Delete("/servers/{id}", s.apiDeleteServer)

	r.Get("/projects", s.apiListProjects)
	r.Get("/projects/{id}", s.apiGetProject)
	r.Post("/projects", s.apiSaveProject)
	r.Delete("/projects/{id}", s.apiDeleteProject)
	r.Get("/projects/{id}/deployments", s.apiProjectDeployments)
	r.Post("/projects/{id}/deploy", s.apiDeploy)
	r.Post("/projects/{id}/rollback", s.apiRollback)

	r.Get("/deployments/{id}", s.handleAPIDeployment)
	r.Get("/metrics", s.handleAPIMetrics)
	r.Get("/servers/{id}/metrics", s.handleAPIServerMetrics)
	r.Get("/audit", s.handleAPIAudit)
}

// apiAuth authenticates a request with a bearer token. Read-only tokens may
// only issue GET requests.
func (s *Server) apiAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			apiError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if token == "" {
			apiError(w, http.StatusUnauthorized, "empty bearer token")
			return
		}
		record, err := s.store.APITokenByHash(r.Context(), auth.HashToken(token))
		if err != nil {
			apiError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if r.Method != http.MethodGet && record.Scope != "write" {
			apiError(w, http.StatusForbidden, "token is read-only")
			return
		}
		// Best-effort bookkeeping; never blocks the request.
		_ = s.store.TouchAPIToken(context.WithoutCancel(r.Context()), record.ID, time.Now())
		next.ServeHTTP(w, r)
	})
}

func apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON reads exactly one size-limited JSON value and rejects unknown
// fields. API callers should not be able to smuggle a second value or silently
// misspell a deployment setting.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func redactProject(p config.Project) config.Project {
	if len(p.Env) > 0 {
		redacted := make(map[string]string, len(p.Env))
		for key := range p.Env {
			redacted[key] = "[redacted]"
		}
		p.Env = redacted
	}
	if len(p.BuildEnv) > 0 {
		redacted := make(map[string]string, len(p.BuildEnv))
		for key := range p.BuildEnv {
			redacted[key] = "[redacted]"
		}
		p.BuildEnv = redacted
	}
	return p
}

// ---- servers ----

func (s *Server) apiListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := s.store.ListServers(r.Context())
	if err != nil {
		log.Printf("api: list servers: %v", err)
		apiError(w, http.StatusInternalServerError, "failed to load servers")
		return
	}
	if servers == nil {
		servers = []config.Server{}
	}
	writeJSON(w, http.StatusOK, servers)
}

func (s *Server) apiGetServer(w http.ResponseWriter, r *http.Request) {
	srv, err := s.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, models.ErrNotFound) {
		apiError(w, http.StatusNotFound, "unknown server")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "error")
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (s *Server) apiSaveServer(w http.ResponseWriter, r *http.Request) {
	var srv config.Server
	if err := decodeJSON(w, r, &srv); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if srv.WinRMTransport == "" {
		srv.WinRMTransport = "ntlm"
	}
	if err := validateServer(srv); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.UpsertServer(r.Context(), srv); err != nil {
		log.Printf("api: save server: %v", err)
		apiError(w, http.StatusInternalServerError, "failed to save server")
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (s *Server) apiDeleteServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	n, err := s.store.CountProjectsByServer(r.Context(), id)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "error")
		return
	}
	if n > 0 {
		apiError(w, http.StatusConflict, "server has dependent projects")
		return
	}
	if err := s.store.DeleteServer(r.Context(), id); err != nil {
		log.Printf("api: delete server: %v", err)
		apiError(w, http.StatusInternalServerError, "failed to delete server")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ---- projects ----

func (s *Server) apiListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		log.Printf("api: list projects: %v", err)
		apiError(w, http.StatusInternalServerError, "failed to load projects")
		return
	}
	if projects == nil {
		projects = []config.Project{}
	}
	for i := range projects {
		projects[i] = redactProject(projects[i])
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) apiGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProject(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, models.ErrNotFound) {
		apiError(w, http.StatusNotFound, "unknown project")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "error")
		return
	}
	writeJSON(w, http.StatusOK, redactProject(p))
}

func (s *Server) apiSaveProject(w http.ResponseWriter, r *http.Request) {
	var p config.Project
	if err := decodeJSON(w, r, &p); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if p.Branch == "" {
		p.Branch = "main"
	}
	if p.HealthPath == "" {
		p.HealthPath = "/"
	}
	srv, err := s.store.GetServer(r.Context(), p.ServerID)
	if err != nil {
		apiError(w, http.StatusBadRequest, "server_id does not match a server")
		return
	}
	p.Strategy = srv.Type
	if srv.Type == config.ServerTypeIIS || srv.Type == config.ServerTypeWindowsService {
		p.Source = ""
	}
	// The host port is derived; keep the legacy field in sync for records.
	p.Port = p.EffectiveHostPort()
	if err := validateProject(p, srv); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.UpsertProject(r.Context(), p); err != nil {
		log.Printf("api: save project: %v", err)
		apiError(w, http.StatusInternalServerError, "failed to save project")
		return
	}
	writeJSON(w, http.StatusOK, redactProject(p))
}

func (s *Server) apiDeleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteProject(r.Context(), chi.URLParam(r, "id")); err != nil {
		log.Printf("api: delete project: %v", err)
		apiError(w, http.StatusInternalServerError, "failed to delete project")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) apiProjectDeployments(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 50, 1, 500)
	deploys, err := s.store.ListDeployments(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "error")
		return
	}
	if deploys == nil {
		deploys = []models.Deployment{}
	}
	writeJSON(w, http.StatusOK, deploys)
}

func (s *Server) apiDeploy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.store.GetProject(ctx, chi.URLParam(r, "id"))
	if errors.Is(err, models.ErrNotFound) {
		apiError(w, http.StatusNotFound, "unknown project")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "error")
		return
	}
	srv, err := s.store.GetServer(ctx, project.ServerID)
	if err != nil {
		apiError(w, http.StatusBadRequest, "project has no valid server")
		return
	}

	var req struct {
		Commit string `json:"commit"`
		Ref    string `json:"ref"`
	}
	// The body is optional, but a supplied body must be valid JSON.
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
			apiError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	ref := req.Ref
	if ref == "" {
		branch := project.Branch
		if branch == "" {
			branch = "main"
		}
		ref = "refs/heads/" + branch
	}

	id, err := s.deployer.Trigger(ctx, project, srv, "api", req.Commit, ref)
	if errors.Is(err, deployment.ErrDeployInProgress) {
		apiError(w, http.StatusConflict, "a deployment is already in progress")
		return
	}
	if err != nil {
		log.Printf("api: deploy: %v", err)
		apiError(w, http.StatusInternalServerError, "failed to start deployment")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"deployment_id": id, "status": "accepted"})
}

func (s *Server) apiRollback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.store.GetProject(ctx, chi.URLParam(r, "id"))
	if errors.Is(err, models.ErrNotFound) {
		apiError(w, http.StatusNotFound, "unknown project")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "error")
		return
	}
	srv, err := s.store.GetServer(ctx, project.ServerID)
	if err != nil {
		apiError(w, http.StatusBadRequest, "project has no valid server")
		return
	}
	id, err := s.deployer.Rollback(ctx, project, srv)
	if errors.Is(err, deployment.ErrDeployInProgress) {
		apiError(w, http.StatusConflict, "a deployment is already in progress")
		return
	}
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"deployment_id": id, "status": "accepted"})
}

// ---- API token management (session-authenticated UI) ----

type tokensPageData struct {
	pageData
	Tokens   []models.APIToken
	NewToken string
	Error    string
	Notice   string
}

func (s *Server) handleTokensPage(w http.ResponseWriter, r *http.Request) {
	s.renderTokens(w, r, http.StatusOK, nil, r.URL.Query().Get("error"), r.URL.Query().Get("notice"))
}

func (s *Server) renderTokens(w http.ResponseWriter, r *http.Request, status int, newToken *string, errMsg, notice string) {
	tokens, err := s.store.ListAPITokens(r.Context())
	if err != nil {
		log.Printf("tokens: list: %v", err)
		http.Error(w, "failed to load tokens", http.StatusInternalServerError)
		return
	}
	data := tokensPageData{pageData: s.page(r), Tokens: tokens, Error: errMsg, Notice: notice}
	if newToken != nil {
		data.NewToken = *newToken
	}
	data.Active = "tokens"
	s.render(w, status, "tokens", data)
}

func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderTokens(w, r, http.StatusBadRequest, nil, "Malformed form submission.", "")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	scope := strings.TrimSpace(r.FormValue("scope"))
	if scope == "" {
		scope = "write"
	}
	if scope != "read" && scope != "write" {
		s.renderTokens(w, r, http.StatusBadRequest, nil, "Scope must be read or write.", "")
		return
	}
	if name == "" {
		s.renderTokens(w, r, http.StatusBadRequest, nil, "Name is required.", "")
		return
	}
	plaintext, hash, err := auth.NewAPIToken()
	if err != nil {
		log.Printf("tokens: generate: %v", err)
		s.renderTokens(w, r, http.StatusInternalServerError, nil, "Failed to create token.", "")
		return
	}
	if _, err := s.store.CreateAPIToken(r.Context(), name, hash, scope); err != nil {
		log.Printf("tokens: create: %v", err)
		s.renderTokens(w, r, http.StatusInternalServerError, nil, "Failed to create token.", "")
		return
	}
	// Log the name only; the token value is shown once to the operator.
	log.Printf("api token %q created (scope=%s)", name, scope)
	s.renderTokens(w, r, http.StatusOK, &plaintext, "", "Token created. Copy it now; it will not be shown again.")
}

func (s *Server) handleTokenDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/tokens?error=Invalid+token+id", http.StatusSeeOther)
		return
	}
	if err := s.store.DeleteAPIToken(r.Context(), id); err != nil {
		log.Printf("tokens: delete: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	log.Printf("api token id=%d revoked", id)
	http.Redirect(w, r, "/tokens?notice=Token+revoked", http.StatusSeeOther)
}

func queryInt(r *http.Request, key string, def, min, max int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return def
	}
	return n
}
