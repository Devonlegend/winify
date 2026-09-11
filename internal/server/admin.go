package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Devonlegend/winify/internal/config"
)

// ---- Servers ----

type serversPageData struct {
	pageData
	Servers []config.Server
	Edit    *config.Server
	Error   string
	Notice  string
}

func (s *Server) handleServersPage(w http.ResponseWriter, r *http.Request) {
	s.renderServers(w, r, http.StatusOK, s.serverFromQuery(r), r.URL.Query().Get("error"))
}

func (s *Server) renderServers(w http.ResponseWriter, r *http.Request, status int, edit *config.Server, errMsg string) {
	servers, err := s.store.ListServers(r.Context())
	if err != nil {
		log.Printf("servers: list: %v", err)
		http.Error(w, "failed to load servers", http.StatusInternalServerError)
		return
	}
	data := serversPageData{
		pageData: s.page(r),
		Servers:  servers,
		Edit:     edit,
		Error:    errMsg,
		Notice:   r.URL.Query().Get("notice"),
	}
	data.Active = "servers"
	s.render(w, status, "servers", data)
}

func (s *Server) serverFromQuery(r *http.Request) *config.Server {
	id := r.URL.Query().Get("edit")
	if id == "" {
		return nil
	}
	srv, err := s.store.GetServer(r.Context(), id)
	if err != nil {
		return nil
	}
	return &srv
}

func (s *Server) handleServerSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderServers(w, r, http.StatusBadRequest, nil, "Malformed form submission.")
		return
	}
	srv := config.Server{
		ID:             strings.TrimSpace(r.FormValue("id")),
		Name:           strings.TrimSpace(r.FormValue("name")),
		Type:           strings.TrimSpace(r.FormValue("type")),
		Host:           strings.TrimSpace(r.FormValue("host")),
		WinRMEndpoint:  strings.TrimSpace(r.FormValue("winrm_endpoint")),
		WinRMUser:      strings.TrimSpace(r.FormValue("winrm_user")),
		WinRMTransport: strings.TrimSpace(r.FormValue("winrm_transport")),
		WinRMInsecure:  r.FormValue("winrm_insecure") != "",
		CredentialRef:  strings.TrimSpace(r.FormValue("credential_ref")),
		SSHHost:        strings.TrimSpace(r.FormValue("ssh_host")),
		SSHUser:        strings.TrimSpace(r.FormValue("ssh_user")),
		SSHKeyRef:      strings.TrimSpace(r.FormValue("ssh_key_ref")),
		Services:       parseList(r.FormValue("services")),
		DiskPath:       strings.TrimSpace(r.FormValue("disk_path")),
	}
	if port := strings.TrimSpace(r.FormValue("ssh_port")); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil {
			s.renderServers(w, r, http.StatusBadRequest, &srv, "ssh_port must be a number.")
			return
		}
		srv.SSHPort = n
	}
	if srv.WinRMTransport == "" {
		srv.WinRMTransport = "ntlm"
	}

	if err := validateServer(srv); err != nil {
		s.renderServers(w, r, http.StatusBadRequest, &srv, err.Error())
		return
	}
	if err := s.store.UpsertServer(r.Context(), srv); err != nil {
		log.Printf("servers: save: %v", err)
		s.renderServers(w, r, http.StatusInternalServerError, &srv, "Failed to save server.")
		return
	}
	http.Redirect(w, r, "/servers?notice=Server+saved", http.StatusSeeOther)
}

func (s *Server) handleServerDelete(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if id == "" {
		http.Redirect(w, r, "/servers?error=Missing+server+id", http.StatusSeeOther)
		return
	}
	n, err := s.store.CountProjectsByServer(r.Context(), id)
	if err != nil {
		log.Printf("servers: delete count: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if n > 0 {
		http.Redirect(w, r, "/servers?error="+urlQuery("Server has "+strconv.Itoa(n)+" project(s); remove them first"), http.StatusSeeOther)
		return
	}
	if err := s.store.DeleteServer(r.Context(), id); err != nil {
		log.Printf("servers: delete: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/servers?notice=Server+deleted", http.StatusSeeOther)
}

func validateServer(srv config.Server) error {
	if srv.ID == "" {
		return errors.New("id is required")
	}
	if srv.Name == "" {
		return errors.New("name is required")
	}
	switch srv.Type {
	case config.ServerTypeDocker:
		if srv.SSHHost == "" {
			return errors.New("ssh_host is required for a docker server")
		}
		if srv.SSHUser == "" {
			return errors.New("ssh_user is required for a docker server")
		}
		if srv.SSHKeyRef == "" {
			return errors.New("ssh_key_ref is required for a docker server")
		}
	case config.ServerTypeIIS:
		if srv.WinRMEndpoint == "" {
			return errors.New("winrm_endpoint is required for an IIS server")
		}
		if srv.WinRMUser == "" {
			return errors.New("winrm_user is required for an IIS server")
		}
		if srv.CredentialRef == "" {
			return errors.New("credential_ref is required for an IIS server")
		}
	default:
		return fmt.Errorf("type must be %q or %q", config.ServerTypeDocker, config.ServerTypeIIS)
	}
	return nil
}

// ---- Projects ----

type projectRow struct {
	Project    config.Project
	ServerType string
}

type projectsPageData struct {
	pageData
	Projects []projectRow
	Servers  []config.Server
	Edit     *config.Project
	EditEnv  string
	Error    string
	Notice   string
}

func (s *Server) handleProjectsPage(w http.ResponseWriter, r *http.Request) {
	var edit *config.Project
	if id := r.URL.Query().Get("edit"); id != "" {
		if p, err := s.store.GetProject(r.Context(), id); err == nil {
			edit = &p
		}
	}
	s.renderProjects(w, r, http.StatusOK, edit, r.URL.Query().Get("error"))
}

func (s *Server) renderProjects(w http.ResponseWriter, r *http.Request, status int, edit *config.Project, errMsg string) {
	ctx := r.Context()
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		log.Printf("projects: list: %v", err)
		http.Error(w, "failed to load projects", http.StatusInternalServerError)
		return
	}
	servers, err := s.store.ListServers(ctx)
	if err != nil {
		log.Printf("projects: servers: %v", err)
		http.Error(w, "failed to load servers", http.StatusInternalServerError)
		return
	}
	serverType := make(map[string]string, len(servers))
	for _, srv := range servers {
		serverType[srv.ID] = srv.Type
	}
	rows := make([]projectRow, 0, len(projects))
	for _, p := range projects {
		rows = append(rows, projectRow{Project: p, ServerType: serverType[p.ServerID]})
	}

	data := projectsPageData{
		pageData: s.page(r),
		Projects: rows,
		Servers:  servers,
		Edit:     edit,
		Error:    errMsg,
		Notice:   r.URL.Query().Get("notice"),
	}
	if edit != nil {
		data.EditEnv = formatEnv(edit.Env)
	}
	data.Active = "projects"
	s.render(w, status, "projects", data)
}

func (s *Server) handleProjectSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderProjects(w, r, http.StatusBadRequest, nil, "Malformed form submission.")
		return
	}
	port, err := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
	if err != nil {
		s.renderProjects(w, r, http.StatusBadRequest, nil, "port must be a number.")
		return
	}
	p := config.Project{
		ID:               strings.TrimSpace(r.FormValue("id")),
		Name:             strings.TrimSpace(r.FormValue("name")),
		ServerID:         strings.TrimSpace(r.FormValue("server_id")),
		RepoURL:          strings.TrimSpace(r.FormValue("repo_url")),
		DockerfilePath:   strings.TrimSpace(r.FormValue("dockerfile_path")),
		IISSite:          strings.TrimSpace(r.FormValue("iis_site")),
		IISPhysicalPath:  strings.TrimSpace(r.FormValue("iis_physical_path")),
		IISAppPool:       strings.TrimSpace(r.FormValue("iis_app_pool")),
		IISService:       strings.TrimSpace(r.FormValue("iis_service")),
		IISBuildCommand:  strings.TrimSpace(r.FormValue("iis_build_command")),
		IISSourceSubdir:  strings.TrimSpace(r.FormValue("iis_source_subdir")),
		Branch:           strings.TrimSpace(r.FormValue("branch")),
		Domain:           strings.TrimSpace(r.FormValue("domain")),
		Port:             port,
		HealthPath:       strings.TrimSpace(r.FormValue("health_path")),
		WebhookSecretRef: strings.TrimSpace(r.FormValue("webhook_secret_ref")),
		Env:              parseEnv(r.FormValue("env")),
	}
	if p.Branch == "" {
		p.Branch = "main"
	}
	if p.HealthPath == "" {
		p.HealthPath = "/"
	}

	// Strategy follows the bound server's type; the deploy pipeline selects on it.
	srv, err := s.store.GetServer(r.Context(), p.ServerID)
	if err != nil {
		s.renderProjects(w, r, http.StatusBadRequest, &p, "Select a valid server.")
		return
	}
	p.Strategy = srv.Type

	if err := validateProject(p, srv); err != nil {
		s.renderProjects(w, r, http.StatusBadRequest, &p, err.Error())
		return
	}
	if err := s.store.UpsertProject(r.Context(), p); err != nil {
		log.Printf("projects: save: %v", err)
		s.renderProjects(w, r, http.StatusInternalServerError, &p, "Failed to save project.")
		return
	}
	http.Redirect(w, r, "/projects?notice=Project+saved", http.StatusSeeOther)
}

func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if id == "" {
		http.Redirect(w, r, "/projects?error=Missing+project+id", http.StatusSeeOther)
		return
	}
	if err := s.store.DeleteProject(r.Context(), id); err != nil {
		log.Printf("projects: delete: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/projects?notice=Project+deleted", http.StatusSeeOther)
}

func validateProject(p config.Project, srv config.Server) error {
	switch {
	case p.ID == "":
		return errors.New("id is required")
	case p.Name == "":
		return errors.New("name is required")
	case p.ServerID == "":
		return errors.New("server is required")
	case p.RepoURL == "":
		return errors.New("repo_url is required")
	case p.Port < 1 || p.Port > 65535:
		return errors.New("port must be between 1 and 65535")
	}
	if srv.Type == config.ServerTypeIIS {
		if p.IISPhysicalPath == "" {
			return errors.New("iis_physical_path is required for an IIS project")
		}
		if p.IISAppPool == "" {
			return errors.New("iis_app_pool is required for an IIS project")
		}
	}
	return nil
}

// handleManualDeploy starts a deploy without a webhook (first deploy / retry).
func (s *Server) handleManualDeploy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := chi.URLParam(r, "projectID")

	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		http.Redirect(w, r, "/projects?error="+urlQuery("Unknown project "+projectID), http.StatusSeeOther)
		return
	}
	srv, err := s.store.GetServer(ctx, project.ServerID)
	if err != nil {
		http.Redirect(w, r, "/projects?error="+urlQuery("Project has no valid server"), http.StatusSeeOther)
		return
	}
	branch := project.Branch
	if branch == "" {
		branch = "main"
	}
	// Empty commit: the pipeline checks out the branch instead.
	if _, err := s.deployer.Trigger(ctx, project, srv, "manual", "", "refs/heads/"+branch); err != nil {
		http.Redirect(w, r, "/projects?error="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/deployment?notice="+urlQuery("Deploy started for "+projectID), http.StatusSeeOther)
}

// ---- Credentials ----

type credentialsPageData struct {
	pageData
	Names  []string
	Error  string
	Notice string
}

func (s *Server) handleCredentialsPage(w http.ResponseWriter, r *http.Request) {
	s.renderCredentials(w, r, http.StatusOK, r.URL.Query().Get("error"))
}

func (s *Server) renderCredentials(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	names, err := s.store.CredentialNames(r.Context())
	if err != nil {
		log.Printf("credentials: list: %v", err)
		http.Error(w, "failed to load credentials", http.StatusInternalServerError)
		return
	}
	data := credentialsPageData{
		pageData: s.page(r),
		Names:    names,
		Error:    errMsg,
		Notice:   r.URL.Query().Get("notice"),
	}
	data.Active = "credentials"
	s.render(w, status, "credentials", data)
}

func (s *Server) handleCredentialSave(w http.ResponseWriter, r *http.Request) {
	if s.credentials == nil {
		s.renderCredentials(w, r, http.StatusServiceUnavailable, "Credential management is disabled.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderCredentials(w, r, http.StatusBadRequest, "Malformed form submission.")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	secret := r.FormValue("secret")
	if name == "" {
		s.renderCredentials(w, r, http.StatusBadRequest, "Name is required.")
		return
	}
	if strings.TrimSpace(secret) == "" {
		s.renderCredentials(w, r, http.StatusBadRequest, "Secret value is required.")
		return
	}
	// The value is encrypted at rest and never returned to the browser.
	if err := s.credentials.Put(r.Context(), name, secret); err != nil {
		log.Printf("credentials: put: %v", err)
		s.renderCredentials(w, r, http.StatusInternalServerError, "Failed to store credential.")
		return
	}
	http.Redirect(w, r, "/credentials?notice="+urlQuery("Credential "+name+" stored"), http.StatusSeeOther)
}

func (s *Server) handleCredentialDelete(w http.ResponseWriter, r *http.Request) {
	if s.credentials == nil {
		http.Error(w, "credential management is disabled", http.StatusServiceUnavailable)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/credentials?error=Missing+name", http.StatusSeeOther)
		return
	}
	if err := s.credentials.Delete(r.Context(), name); err != nil {
		log.Printf("credentials: delete: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/credentials?notice="+urlQuery("Credential "+name+" deleted"), http.StatusSeeOther)
}

// ---- helpers ----

// parseList splits a comma/whitespace separated list into a slice.
func parseList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseEnv parses "KEY=VALUE" lines into a map, ignoring blanks and # comments.
func parseEnv(raw string) map[string]string {
	env := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); key != "" {
			env[key] = strings.TrimSpace(value)
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

// formatEnv renders env as sorted KEY=VALUE lines for the edit form.
func formatEnv(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, env[k])
	}
	return strings.TrimRight(b.String(), "\n")
}

// urlQuery escapes a value for use in a query string.
func urlQuery(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
