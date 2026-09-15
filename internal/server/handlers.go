package server

import (
	"context"
	"encoding/json"
	"errors"
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

// pageData is embedded by every page so the layout can render the current user
// and highlight the active tab. User is empty on the login page, which hides
// the nav.
type pageData struct {
	User   string
	Active string
}

func (s *Server) page(r *http.Request) pageData {
	user, _ := auth.UserFromContext(r.Context())
	return pageData{User: user}
}

// ---- Auth ----

type loginData struct {
	User  string
	Error string
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "login", loginData{})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "login", loginData{Error: "Malformed form submission."})
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := s.auth.Authenticate(r.Context(), username, password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		// Deliberately vague: do not reveal whether the user exists.
		s.render(w, http.StatusUnauthorized, "login", loginData{Error: "Invalid username or password."})
		return
	}
	if err != nil {
		log.Printf("login: %v", err) // err has no credentials
		s.render(w, http.StatusInternalServerError, "login", loginData{Error: "Sign-in failed. Try again."})
		return
	}

	if err := s.auth.StartSession(r.Context(), w, user.ID); err != nil {
		log.Printf("start session: %v", err)
		s.render(w, http.StatusInternalServerError, "login", loginData{Error: "Sign-in failed. Try again."})
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.EndSession(r.Context(), w, r); err != nil {
		log.Printf("logout: %v", err)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---- Dashboard shell ----

type dashboardData struct {
	pageData
	DBStatus     string
	ServerCount  int
	ProjectCount int
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := dashboardData{pageData: s.page(r), DBStatus: "ok"}
	data.Active = "dashboard"

	if err := s.store.DB().PingContext(r.Context()); err != nil {
		data.DBStatus = "unavailable"
	}
	if servers, err := s.store.ListServers(r.Context()); err == nil {
		data.ServerCount = len(servers)
	}
	if projects, err := s.store.ListProjects(r.Context()); err == nil {
		data.ProjectCount = len(projects)
	}
	s.render(w, http.StatusOK, "dashboard", data)
}

// projectDeploys is one project row on the Deployment tab: its recent attempts
// plus whether a rollback target exists.
type projectDeploys struct {
	Project     config.Project
	TargetType  string
	Deployments []models.Deployment
	CanRollback bool
}

type deploymentPageData struct {
	pageData
	Projects []projectDeploys
}

func (s *Server) handleDeployment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		log.Printf("deployment: list projects: %v", err)
		http.Error(w, "failed to load projects", http.StatusInternalServerError)
		return
	}
	servers, err := s.store.ListServers(ctx)
	if err != nil {
		log.Printf("deployment: list servers: %v", err)
		http.Error(w, "failed to load servers", http.StatusInternalServerError)
		return
	}
	serverType := make(map[string]string, len(servers))
	for _, srv := range servers {
		serverType[srv.ID] = srv.Type
	}

	rows := make([]projectDeploys, 0, len(projects))
	for _, p := range projects {
		deploys, err := s.store.ListDeployments(ctx, p.ID, 20)
		if err != nil {
			log.Printf("deployment: list %s: %v", p.ID, err)
			http.Error(w, "failed to load deployments", http.StatusInternalServerError)
			return
		}
		successes, err := s.store.SuccessfulDeployments(ctx, p.ID, 2)
		if err != nil {
			log.Printf("deployment: successes %s: %v", p.ID, err)
			http.Error(w, "failed to load deployments", http.StatusInternalServerError)
			return
		}
		rows = append(rows, projectDeploys{
			Project:     p,
			TargetType:  serverType[p.ServerID],
			Deployments: deploys,
			CanRollback: len(successes) >= 2,
		})
	}

	data := deploymentPageData{pageData: s.page(r), Projects: rows}
	data.Active = "deployment"
	s.render(w, http.StatusOK, "deployment", data)
}

// handleRollback redeploys the previous successful image tag for a project.
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	ctx := r.Context()

	project, err := s.store.GetProject(ctx, projectID)
	if errors.Is(err, models.ErrNotFound) {
		http.Error(w, "unknown project", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("rollback: get project: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	srv, err := s.store.GetServer(ctx, project.ServerID)
	if err != nil {
		log.Printf("rollback: get server: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	if _, err := s.deployer.Rollback(ctx, project, srv); err != nil {
		if errors.Is(err, deployment.ErrDeployInProgress) {
			http.Error(w, "a deployment is already in progress", http.StatusConflict)
			return
		}
		log.Printf("rollback %s: %v", projectID, err)
		http.Error(w, "rollback failed", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/deployment", http.StatusSeeOther)
}

// handleAPIDeployments returns a project's deploy history as JSON.
func (s *Server) handleAPIDeployments(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	deploys, err := s.store.ListDeployments(r.Context(), projectID, 50)
	if err != nil {
		log.Printf("api deployments: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, deploys)
}

// handleAPIDeployment returns one deploy attempt (including its log) as JSON.
func (s *Server) handleAPIDeployment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	d, err := s.store.GetDeployment(r.Context(), id)
	if errors.Is(err, models.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("api deployment: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// serverMetricCard is one server's card on the Monitoring tab.
type serverMetricCard struct {
	Server  config.Server
	Latest  models.Metric
	HasData bool
	Down    bool
	Stale   bool
}

type monitoringPageData struct {
	pageData
	Cards           []serverMetricCard
	IntervalSeconds int
}

func (s *Server) handleMonitoring(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	servers, err := s.store.ListServers(ctx)
	if err != nil {
		log.Printf("monitoring: list servers: %v", err)
		http.Error(w, "failed to load servers", http.StatusInternalServerError)
		return
	}
	latest, err := s.store.LatestMetrics(ctx)
	if err != nil {
		log.Printf("monitoring: latest metrics: %v", err)
		http.Error(w, "failed to load metrics", http.StatusInternalServerError)
		return
	}
	byServer := make(map[string]models.Metric, len(latest))
	for _, m := range latest {
		byServer[m.ServerID] = m
	}

	interval := s.cfg.Monitoring.IntervalSeconds
	if interval <= 0 {
		interval = 30
	}
	cards := make([]serverMetricCard, 0, len(servers))
	for _, srv := range servers {
		card := serverMetricCard{Server: srv}
		if m, ok := byServer[srv.ID]; ok {
			card.Latest = m
			card.HasData = true
			card.Down = !m.Reachable
			// Two missed intervals means the poller is no longer updating us.
			card.Stale = time.Since(m.Timestamp) > time.Duration(2*interval)*time.Second
		}
		cards = append(cards, card)
	}

	data := monitoringPageData{pageData: s.page(r), Cards: cards, IntervalSeconds: interval}
	data.Active = "monitoring"
	s.render(w, http.StatusOK, "monitoring", data)
}

// handleAPIMetrics returns the latest sample per server, for the auto-refresh.
func (s *Server) handleAPIMetrics(w http.ResponseWriter, r *http.Request) {
	latest, err := s.store.LatestMetrics(r.Context())
	if err != nil {
		log.Printf("api metrics: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, latest)
}

// handleAPIServerMetrics returns a server's sample history, oldest first, for
// the Chart.js time series.
func (s *Server) handleAPIServerMetrics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	limit := s.cfg.Monitoring.HistoryPoints
	if limit <= 0 {
		limit = 200
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	history, err := s.store.MetricHistory(r.Context(), id, limit)
	if err != nil {
		log.Printf("api server metrics: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// handleAPIAudit returns recent audited remote commands, newest first.
func (s *Server) handleAPIAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	commands, err := s.store.ListRemoteCommands(r.Context(), limit)
	if err != nil {
		log.Printf("api audit: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, commands)
}

func (s *Server) handleAssistant(w http.ResponseWriter, r *http.Request) {
	data := assistantPageData{
		pageData: s.page(r),
		Enabled:  s.assistant != nil,
		Model:    s.cfg.Assistant.Model,
	}
	data.Active = "assistant"
	s.render(w, http.StatusOK, "assistant", data)
}

type assistantPageData struct {
	pageData
	Enabled bool
	Model   string
}

// handleAssistantAsk answers a question from the curated docs and/or live
// deploy/monitoring data. The answer cites which sources it used.
func (s *Server) handleAssistantAsk(w http.ResponseWriter, r *http.Request) {
	if s.assistant == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "assistant is disabled"})
		return
	}

	var req struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	question := strings.TrimSpace(req.Question)
	if question == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question is required"})
		return
	}

	timeout := time.Duration(s.cfg.Assistant.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	answer, err := s.assistant.Ask(ctx, question)
	if err != nil {
		log.Printf("assistant: ask: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "assistant failed"})
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

// ---- Health ----

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// handleHealthz is the liveness/readiness probe: it reports whether the
// process is up and whether it can reach SQLite right now. It is public and
// carries no secrets.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{Status: "ok", Database: "ok"}
	code := http.StatusOK
	if err := s.store.DB().PingContext(r.Context()); err != nil {
		resp.Status = "degraded"
		resp.Database = "error"
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp)
}
