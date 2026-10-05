package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Devonlegend/winify/internal/auth"
	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/models"
)

// pageData is embedded by every page so the layout can render the current user,
// the sidebar tree and highlight the active section/resource.
type pageData struct {
	User           string
	Active         string
	ActiveResource string
	Nav            []navProject
}

func (s *Server) page(r *http.Request) pageData {
	user, _ := auth.UserFromContext(r.Context())
	return pageData{User: user, Nav: s.navTree(r.Context())}
}

// ---- Auth ----

type loginData struct {
	User  string
	Error string
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	hasUsers, err := s.auth.HasUsers(r.Context())
	if err != nil {
		log.Printf("login: check users: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if !hasUsers {
		http.Redirect(w, r, "/register", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "login", loginData{})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// A fresh install has no account yet: send the visitor to registration.
	if hasUsers, err := s.auth.HasUsers(r.Context()); err == nil && !hasUsers {
		http.Redirect(w, r, "/register", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "login", loginData{Error: "Malformed form submission."})
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	loginKey := r.RemoteAddr + "|" + strings.ToLower(strings.TrimSpace(username))
	if retry := s.auth.LoginRetryAfter(loginKey); retry > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(retry.Seconds())+1, 10))
		s.render(w, http.StatusTooManyRequests, "login", loginData{Error: "Too many failed sign-in attempts. Try again later."})
		return
	}

	user, err := s.auth.Authenticate(r.Context(), username, password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.auth.RecordLoginFailure(loginKey)
		// Deliberately vague: do not reveal whether the user exists.
		s.render(w, http.StatusUnauthorized, "login", loginData{Error: "Invalid username or password."})
		return
	}
	if err != nil {
		log.Printf("login: %v", err) // err has no credentials
		s.render(w, http.StatusInternalServerError, "login", loginData{Error: "Sign-in failed. Try again."})
		return
	}

	s.auth.ClearLoginFailures(loginKey)
	if err := s.auth.StartSession(r.Context(), w, user.ID); err != nil {
		log.Printf("start session: %v", err)
		s.render(w, http.StatusInternalServerError, "login", loginData{Error: "Sign-in failed. Try again."})
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// registerData backs the first-run setup page. User is always empty (the layout
// checks it to decide whether to render the authenticated shell).
type registerData struct {
	User          string
	Error         string
	SetupRequired bool
}

// handleRegisterForm shows the first-run setup page, or redirects to login once
// an admin account exists.
func (s *Server) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	hasUsers, err := s.auth.HasUsers(r.Context())
	if err != nil {
		log.Printf("register: check users: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if hasUsers {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "register", registerData{SetupRequired: s.setupToken != ""})
}

// handleRegister creates the first admin account and signs it in. It is closed
// as soon as an account exists, so it cannot be used to overwrite the admin.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if hasUsers, err := s.auth.HasUsers(r.Context()); err != nil {
		log.Printf("register: check users: %v", err)
		s.render(w, http.StatusInternalServerError, "register", registerData{Error: "Setup failed. Try again.", SetupRequired: s.setupToken != ""})
		return
	} else if hasUsers {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.setupToken != "" {
		if err := r.ParseForm(); err != nil {
			s.render(w, http.StatusBadRequest, "register", registerData{Error: "Malformed form submission.", SetupRequired: true})
			return
		}
		if !validSetupToken(s.setupToken, r.FormValue("setup_token")) {
			s.render(w, http.StatusForbidden, "register", registerData{Error: "A valid setup token is required.", SetupRequired: true})
			return
		}
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "register", registerData{Error: "Malformed form submission.", SetupRequired: s.setupToken != ""})
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	switch {
	case username == "":
		s.render(w, http.StatusBadRequest, "register", registerData{Error: "Username is required.", SetupRequired: s.setupToken != ""})
		return
	case len(password) < 8:
		s.render(w, http.StatusBadRequest, "register", registerData{Error: "Password must be at least 8 characters.", SetupRequired: s.setupToken != ""})
		return
	case password != r.FormValue("confirm"):
		s.render(w, http.StatusBadRequest, "register", registerData{Error: "Passwords do not match.", SetupRequired: s.setupToken != ""})
		return
	}

	user, err := s.auth.Register(r.Context(), username, password)
	if errors.Is(err, auth.ErrRegistrationClosed) {
		// Another request won the race; send them to sign in.
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err != nil {
		log.Printf("register: %v", err) // err has no password
		s.render(w, http.StatusInternalServerError, "register", registerData{Error: "Setup failed. Try again.", SetupRequired: s.setupToken != ""})
		return
	}
	if err := s.auth.StartSession(r.Context(), w, user.ID); err != nil {
		log.Printf("register: start session: %v", err)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func validSetupToken(expected, supplied string) bool {
	return len(expected) > 0 && len(expected) == len(supplied) &&
		subtle.ConstantTimeCompare([]byte(expected), []byte(supplied)) == 1
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
	FailedCount  int
	Recent       []dashboardDeploy
}

// dashboardDeploy is one row in the dashboard activity feed.
type dashboardDeploy struct {
	ID          int64
	ProjectID   string
	ProjectName string
	Status      string
	Trigger     string
	StartedAt   time.Time
}

// dashboardRecent is how many recent deployments the landing page shows.
const dashboardRecent = 6

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := dashboardData{pageData: s.page(r), DBStatus: "ok"}
	data.Active = "dashboard"

	if err := s.store.DB().PingContext(ctx); err != nil {
		data.DBStatus = "unavailable"
	}
	servers, err := s.store.ListServers(ctx)
	if err == nil {
		data.ServerCount = len(servers)
	}
	projects, err := s.store.ListProjects(ctx)
	if err == nil {
		data.ProjectCount = len(projects)
		for _, p := range projects {
			deploys, derr := s.store.ListDeployments(ctx, p.ID, dashboardRecent)
			if derr != nil {
				continue
			}
			for i, d := range deploys {
				// Only the latest attempt per resource counts as "needs attention".
				if i == 0 && d.Status == models.DeployFailed {
					data.FailedCount++
				}
				data.Recent = append(data.Recent, dashboardDeploy{
					ID:          d.ID,
					ProjectID:   p.ID,
					ProjectName: p.Name,
					Status:      d.Status,
					Trigger:     d.Trigger,
					StartedAt:   d.StartedAt,
				})
			}
		}
	}
	sort.Slice(data.Recent, func(i, j int) bool { return data.Recent[i].StartedAt.After(data.Recent[j].StartedAt) })
	if len(data.Recent) > dashboardRecent {
		data.Recent = data.Recent[:dashboardRecent]
	}
	s.render(w, http.StatusOK, "dashboard", data)
}

// deploymentRow is one deploy on the history page: the attempt plus the owning
// resource's name and whether this row carries the rollback action.
type deploymentRow struct {
	models.Deployment
	ProjectName string
	CanRollback bool
}

type deploymentPageData struct {
	pageData
	Rows       []deploymentRow
	Counts     map[string]int
	Status     string
	Query      string
	Page       int
	PrevPage   int
	NextPage   int
	TotalPages int
	Total      int
	Error      string
	Notice     string
}

// deploymentPageSize is how many attempts the history shows per page.
const deploymentPageSize = 25

func (s *Server) handleDeployment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	switch status {
	case "", models.DeploySuccess, models.DeployFailed, models.DeployRunning, models.DeployQueued:
	default:
		status = ""
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	page := 1
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
		page = v
	}

	deploys, total, err := s.store.ListDeploymentsPage(ctx, status, query, deploymentPageSize, (page-1)*deploymentPageSize)
	if err != nil {
		log.Printf("deployment: list: %v", err)
		http.Error(w, "failed to load deployments", http.StatusInternalServerError)
		return
	}
	counts, err := s.store.DeploymentStatusCounts(ctx)
	if err != nil {
		log.Printf("deployment: counts: %v", err)
		counts = map[string]int{}
	}
	all := 0
	for _, n := range counts {
		all += n
	}
	counts["all"] = all

	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		log.Printf("deployment: list projects: %v", err)
		http.Error(w, "failed to load projects", http.StatusInternalServerError)
		return
	}
	nameByID := make(map[string]string, len(projects))
	for _, p := range projects {
		nameByID[p.ID] = p.Name
	}

	// Rollback is offered only on a project's newest attempt and only when a
	// recovery point exists. Computed per project so it does not depend on the
	// page being viewed.
	rollbackOK := make(map[string]bool, len(projects))
	latest := make(map[string]int64, len(projects))
	for _, p := range projects {
		recent, _ := s.store.ListDeployments(ctx, p.ID, 1)
		var latestID int64
		if len(recent) > 0 {
			latestID = recent[0].ID
		}
		successes, _ := s.store.SuccessfulDeployments(ctx, p.ID, 2)
		ok := len(successes) >= 2
		if len(successes) >= 1 && len(recent) > 0 && recent[0].Status != models.DeploySuccess {
			ok = true
		}
		rollbackOK[p.ID] = ok
		latest[p.ID] = latestID
	}

	rows := make([]deploymentRow, 0, len(deploys))
	for _, d := range deploys {
		rows = append(rows, deploymentRow{
			Deployment:  d,
			ProjectName: nameByID[d.ProjectID],
			CanRollback: rollbackOK[d.ProjectID] && latest[d.ProjectID] == d.ID,
		})
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + deploymentPageSize - 1) / deploymentPageSize
	}
	data := deploymentPageData{
		pageData:   s.page(r),
		Rows:       rows,
		Counts:     counts,
		Status:     status,
		Query:      query,
		Page:       page,
		TotalPages: totalPages,
		Total:      total,
		Error:      r.URL.Query().Get("error"),
		Notice:     r.URL.Query().Get("notice"),
	}
	if page > 1 {
		data.PrevPage = page - 1
	}
	if page < totalPages {
		data.NextPage = page + 1
	}
	data.Active = "deployment"
	s.render(w, http.StatusOK, "deployment", data)
}

// auditPageData backs the Audit log page: a filtered, paginated view of the
// command-level audit trail (every remote command winify ran).
type auditPageData struct {
	pageData
	Commands   []models.RemoteCommand
	Servers    []config.Server
	ServerID   string
	Action     string
	Query      string
	Since      string
	Page       int
	PrevPage   int
	NextPage   int
	TotalPages int
	Total      int
}

const auditPageSize = 50

func (s *Server) handleAuditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	serverID := strings.TrimSpace(r.URL.Query().Get("server"))
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	switch action {
	case "", "deploy", "rollback", "monitor", "bootstrap", "onboard":
	default:
		action = ""
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	sinceParam := r.URL.Query().Get("since")
	var since time.Time
	switch sinceParam {
	case "all":
	case "1h":
		since = time.Now().Add(-time.Hour)
	case "7d":
		since = time.Now().Add(-7 * 24 * time.Hour)
	case "30d":
		since = time.Now().Add(-30 * 24 * time.Hour)
	case "", "24h":
		sinceParam = "24h"
		since = time.Now().Add(-24 * time.Hour)
	default:
		sinceParam = "24h"
		since = time.Now().Add(-24 * time.Hour)
	}

	page := 1
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
		page = v
	}

	commands, total, err := s.store.ListRemoteCommandsFiltered(ctx, models.RemoteCommandFilter{
		ServerID: serverID,
		Action:   action,
		Query:    query,
		Since:    since,
		Limit:    auditPageSize,
		Offset:   (page - 1) * auditPageSize,
	})
	if err != nil {
		log.Printf("audit: list: %v", err)
		http.Error(w, "failed to load audit log", http.StatusInternalServerError)
		return
	}
	servers, _ := s.store.ListServers(ctx)

	totalPages := 0
	if total > 0 {
		totalPages = (total + auditPageSize - 1) / auditPageSize
	}
	data := auditPageData{
		pageData:   s.page(r),
		Commands:   commands,
		Servers:    servers,
		ServerID:   serverID,
		Action:     action,
		Query:      query,
		Since:      sinceParam,
		Page:       page,
		TotalPages: totalPages,
		Total:      total,
	}
	if page > 1 {
		data.PrevPage = page - 1
	}
	if page < totalPages {
		data.NextPage = page + 1
	}
	data.Active = "audit"
	s.render(w, http.StatusOK, "audit", data)
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
		msg := "Rollback failed."
		if errors.Is(err, deployment.ErrDeployInProgress) {
			msg = "A deployment is already in progress for " + projectID + "."
		} else {
			log.Printf("rollback %s: %v", projectID, err)
		}
		http.Redirect(w, r, "/deployment?error="+urlQuery(msg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/deployment?notice="+urlQuery("Rollback started for "+projectID), http.StatusSeeOther)
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
