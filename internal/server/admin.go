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
	"github.com/Devonlegend/winify/internal/models"
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
		NSSMPath:       strings.TrimSpace(r.FormValue("nssm_path")),
		CaddyPath:      strings.TrimSpace(r.FormValue("caddy_path")),
		PublicIP:       strings.TrimSpace(r.FormValue("public_ip")),
		Local:          r.FormValue("local") != "",
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
	return config.ValidateServer(srv)
}

// ---- Projects (project group -> environment -> resources) ----

type resourceCard struct {
	Project    config.Project
	ServerType string
	LastStatus string
	HasDeploy  bool
}

type envGroup struct {
	Name      string
	Resources []resourceCard
}

type projectGroup struct {
	Name         string
	Environments []envGroup
	Count        int
}

type projectsPageData struct {
	pageData
	Groups []projectGroup
	Error  string
	Notice string
}

func (s *Server) handleProjectsPage(w http.ResponseWriter, r *http.Request) {
	s.renderProjects(w, r, http.StatusOK, r.URL.Query().Get("error"))
}

func (s *Server) renderProjects(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
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

	byGroup := make(map[string]map[string][]resourceCard)
	envOrder := make(map[string][]string)
	var groupOrder []string
	for _, p := range projects {
		group := p.ProjectGroup
		if group == "" {
			group = "Default"
		}
		env := p.Environment
		if env == "" {
			env = "production"
		}
		if _, ok := byGroup[group]; !ok {
			byGroup[group] = make(map[string][]resourceCard)
			groupOrder = append(groupOrder, group)
		}
		if _, ok := byGroup[group][env]; !ok {
			envOrder[group] = append(envOrder[group], env)
		}
		card := resourceCard{Project: p, ServerType: serverType[p.ServerID]}
		if deploys, err := s.store.ListDeployments(ctx, p.ID, 1); err == nil && len(deploys) > 0 {
			card.LastStatus = deploys[0].Status
			card.HasDeploy = true
		}
		byGroup[group][env] = append(byGroup[group][env], card)
	}
	sort.Strings(groupOrder)

	groups := make([]projectGroup, 0, len(groupOrder))
	for _, g := range groupOrder {
		envs := envOrder[g]
		sort.Strings(envs)
		pg := projectGroup{Name: g}
		for _, e := range envs {
			cards := byGroup[g][e]
			sort.Slice(cards, func(i, j int) bool { return cards[i].Project.Name < cards[j].Project.Name })
			pg.Environments = append(pg.Environments, envGroup{Name: e, Resources: cards})
			pg.Count += len(cards)
		}
		groups = append(groups, pg)
	}

	data := projectsPageData{
		pageData: s.page(r),
		Groups:   groups,
		Error:    errMsg,
		Notice:   r.URL.Query().Get("notice"),
	}
	data.Active = "projects"
	s.render(w, status, "projects", data)
}

type projectFormPageData struct {
	pageData
	Form         *config.Project
	Servers      []config.Server
	EditEnv      string
	EditBuildEnv string
	Error        string
}

func (s *Server) handleProjectNew(w http.ResponseWriter, r *http.Request) {
	s.renderWizard(w, r, http.StatusOK, r.URL.Query().Get("error"))
}

// renderWizard renders the guided New Resource flow. Validation errors fall
// back to the full form (renderProjectForm) so nothing stays hidden.
func (s *Server) renderWizard(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	servers, err := s.store.ListServers(r.Context())
	if err != nil {
		log.Printf("projects: servers: %v", err)
		http.Error(w, "failed to load servers", http.StatusInternalServerError)
		return
	}
	data := projectFormPageData{pageData: s.page(r), Servers: servers, Error: errMsg}
	data.Active = "projects"
	s.render(w, status, "project_wizard", data)
}

func (s *Server) renderProjectForm(w http.ResponseWriter, r *http.Request, status int, form *config.Project, errMsg string) {
	servers, err := s.store.ListServers(r.Context())
	if err != nil {
		log.Printf("projects: servers: %v", err)
		http.Error(w, "failed to load servers", http.StatusInternalServerError)
		return
	}
	data := projectFormPageData{pageData: s.page(r), Form: form, Servers: servers, Error: errMsg}
	if form != nil {
		data.EditEnv = formatEnv(form.Env)
		data.EditBuildEnv = formatEnv(form.BuildEnv)
	}
	data.Active = "projects"
	s.render(w, status, "project_new", data)
}

type resourcePageData struct {
	pageData
	Project          config.Project
	ServerType       string
	Tab              string
	Deployments      []models.Deployment
	CanRollback      bool
	Servers          []config.Server
	Form             *config.Project
	EditEnv          string
	EditBuildEnv     string
	GitHubConfigured bool
	Error            string
	Notice           string
}

func (s *Server) handleResourcePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "projectID")
	project, err := s.store.GetProject(ctx, id)
	if errors.Is(err, models.ErrNotFound) {
		http.Error(w, "unknown resource", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("resource: get: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	serverType := ""
	if srv, err := s.store.GetServer(ctx, project.ServerID); err == nil {
		serverType = srv.Type
	}
	tab := r.URL.Query().Get("tab")
	switch tab {
	case "overview", "deployments", "environment", "settings":
	default:
		tab = "overview"
	}
	deploys, _ := s.store.ListDeployments(ctx, id, 20)
	successes, _ := s.store.SuccessfulDeployments(ctx, id, 2)
	canRollback := len(successes) >= 2
	if len(successes) >= 1 && len(deploys) > 0 && deploys[0].Status != models.DeploySuccess {
		canRollback = true
	}
	servers, _ := s.store.ListServers(ctx)

	data := resourcePageData{
		pageData:         s.page(r),
		Project:          project,
		ServerType:       serverType,
		Tab:              tab,
		Deployments:      deploys,
		CanRollback:      canRollback,
		Servers:          servers,
		Form:             &project,
		EditEnv:          formatEnv(project.Env),
		EditBuildEnv:     formatEnv(project.BuildEnv),
		GitHubConfigured: s.cfg.GitHub.Enabled() && s.connectGitHub != nil,
		Error:            r.URL.Query().Get("error"),
		Notice:           r.URL.Query().Get("notice"),
	}
	data.Active = "projects"
	data.ActiveResource = id
	s.render(w, http.StatusOK, "resource", data)
}

func (s *Server) handleProjectEnvSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := strings.TrimSpace(r.FormValue("id"))
	project, err := s.store.GetProject(ctx, id)
	if err != nil {
		http.Redirect(w, r, "/projects?error="+urlQuery("Unknown resource"), http.StatusSeeOther)
		return
	}
	project.Env = preserveRedactedEnv(parseEnv(r.FormValue("env")), project.Env)
	if _, ok := r.Form["build_env"]; ok {
		project.BuildEnv = preserveRedactedEnv(parseEnv(r.FormValue("build_env")), project.BuildEnv)
	}
	if err := s.store.UpsertProject(ctx, project); err != nil {
		log.Printf("projects: env: %v", err)
		http.Redirect(w, r, "/projects/"+id+"?tab=environment&error="+urlQuery("Failed to save environment"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/projects/"+id+"?tab=environment&notice="+urlQuery("Environment saved"), http.StatusSeeOther)
}

func (s *Server) handleProjectSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderProjectForm(w, r, http.StatusBadRequest, nil, "Malformed form submission.")
		return
	}
	exposes, err := formInt(r, "ports_exposes")
	if err != nil {
		s.renderProjectForm(w, r, http.StatusBadRequest, nil, err.Error())
		return
	}
	if exposes == 0 {
		// Backwards compatibility with the old single "port" field.
		exposes, _ = formInt(r, "port")
	}
	mappings, err := parsePortMappings(r.FormValue("ports_mappings"))
	if err != nil {
		s.renderProjectForm(w, r, http.StatusBadRequest, nil, err.Error())
		return
	}
	healthInterval, err := formInt(r, "health_interval_seconds")
	if err != nil {
		s.renderProjectForm(w, r, http.StatusBadRequest, nil, err.Error())
		return
	}
	healthTimeout, err := formInt(r, "health_timeout_seconds")
	if err != nil {
		s.renderProjectForm(w, r, http.StatusBadRequest, nil, err.Error())
		return
	}
	healthRetries, err := formInt(r, "health_retries")
	if err != nil {
		s.renderProjectForm(w, r, http.StatusBadRequest, nil, err.Error())
		return
	}
	healthStart, err := formInt(r, "health_start_period_seconds")
	if err != nil {
		s.renderProjectForm(w, r, http.StatusBadRequest, nil, err.Error())
		return
	}
	p := config.Project{
		ID:                       strings.TrimSpace(r.FormValue("id")),
		Name:                     strings.TrimSpace(r.FormValue("name")),
		ServerID:                 strings.TrimSpace(r.FormValue("server_id")),
		Source:                   strings.TrimSpace(r.FormValue("source")),
		RepoURL:                  strings.TrimSpace(r.FormValue("repo_url")),
		DockerfilePath:           strings.TrimSpace(r.FormValue("dockerfile_path")),
		ComposePath:              strings.TrimSpace(r.FormValue("compose_path")),
		Image:                    strings.TrimSpace(r.FormValue("image")),
		PortsExposes:             exposes,
		PortsMappings:            mappings,
		IISSite:                  strings.TrimSpace(r.FormValue("iis_site")),
		IISPhysicalPath:          strings.TrimSpace(r.FormValue("iis_physical_path")),
		IISAppPool:               strings.TrimSpace(r.FormValue("iis_app_pool")),
		IISService:               strings.TrimSpace(r.FormValue("iis_service")),
		IISBuildCommand:          strings.TrimSpace(r.FormValue("iis_build_command")),
		IISBlueGreen:             r.FormValue("iis_blue_green") != "",
		IISSourceSubdir:          strings.TrimSpace(r.FormValue("iis_source_subdir")),
		ServiceName:              strings.TrimSpace(r.FormValue("service_name")),
		ServiceExe:               strings.TrimSpace(r.FormValue("service_exe")),
		ServiceArgs:              strings.TrimSpace(r.FormValue("service_args")),
		ServiceWorkDir:           strings.TrimSpace(r.FormValue("service_work_dir")),
		ServiceBuildCommand:      strings.TrimSpace(r.FormValue("service_build_command")),
		ServiceSourceSubdir:      strings.TrimSpace(r.FormValue("service_source_subdir")),
		ServiceLogDir:            strings.TrimSpace(r.FormValue("service_log_dir")),
		ServiceAccount:           strings.TrimSpace(r.FormValue("service_account")),
		Runtime:                  normalizeRuntime(r.FormValue("runtime")),
		CaddyMode:                strings.TrimSpace(r.FormValue("caddy_mode")),
		Branch:                   strings.TrimSpace(r.FormValue("branch")),
		Domain:                   strings.TrimSpace(r.FormValue("domain")),
		HealthPath:               strings.TrimSpace(r.FormValue("health_path")),
		WebhookSecretRef:         strings.TrimSpace(r.FormValue("webhook_secret_ref")),
		GitHubRepo:               strings.TrimSpace(r.FormValue("github_repo")),
		GitCredentialRef:         strings.TrimSpace(r.FormValue("git_credential_ref")),
		Env:                      parseEnv(r.FormValue("env")),
		BuildEnv:                 parseEnv(r.FormValue("build_env")),
		HealthIntervalSeconds:    healthInterval,
		HealthTimeoutSeconds:     healthTimeout,
		HealthRetries:            healthRetries,
		HealthStartPeriodSeconds: healthStart,
		ProjectGroup:             strings.TrimSpace(r.FormValue("project_group")),
		Environment:              strings.TrimSpace(r.FormValue("environment")),
		DisableHealthCheck:       r.FormValue("disable_health_check") != "",
	}
	// The compact settings form does not include every advanced field. Preserve
	// omitted values on updates instead of silently erasing them.
	oldDomain := ""
	if existing, err := s.store.GetProject(r.Context(), p.ID); err == nil {
		oldDomain = existing.Domain
		p.Env = preserveRedactedEnv(p.Env, existing.Env)
		p.BuildEnv = preserveRedactedEnv(p.BuildEnv, existing.BuildEnv)
		if _, present := r.Form["iis_site"]; !present {
			p.IISSite = existing.IISSite
		}
		if _, present := r.Form["runtime"]; !present {
			p.Runtime = existing.Runtime
		}
	}
	p.Env = preserveRedactedEnv(p.Env, nil)
	p.BuildEnv = preserveRedactedEnv(p.BuildEnv, nil)
	if p.Branch == "" {
		p.Branch = "main"
	}
	if p.HealthPath == "" {
		p.HealthPath = "/"
	}
	// The host port is derived; keep the legacy field in sync for records.
	p.Port = p.EffectiveHostPort()

	// Strategy follows the bound server's type; the deploy pipeline selects on it.
	srv, err := s.store.GetServer(r.Context(), p.ServerID)
	if err != nil {
		s.renderProjectForm(w, r, http.StatusBadRequest, &p, "Select a valid server.")
		return
	}
	p.Strategy = srv.Type
	if srv.Type == config.ServerTypeIIS || srv.Type == config.ServerTypeWindowsService {
		p.Source = ""
	}
	if srv.Type == config.ServerTypeIIS {
		// Zero-touch IIS: winify creates the site and pool, so an unnamed one
		// defaults to the project id.
		if p.IISSite == "" {
			p.IISSite = p.ID
		}
		if p.IISAppPool == "" {
			p.IISAppPool = p.ID
		}
	}

	if err := validateProject(p, srv); err != nil {
		s.renderProjectForm(w, r, http.StatusBadRequest, &p, err.Error())
		return
	}
	if err := s.validateProjectTargetPaths(p, srv); err != nil {
		s.renderProjectForm(w, r, http.StatusBadRequest, &p, err.Error())
		return
	}
	if err := s.store.UpsertProject(r.Context(), p); err != nil {
		log.Printf("projects: save: %v", err)
		s.renderProjectForm(w, r, http.StatusInternalServerError, &p, "Failed to save project.")
		return
	}
	if oldDomain != "" && oldDomain != p.Domain {
		if err := s.store.RetireProxyDomain(r.Context(), p.ID, oldDomain); err != nil {
			log.Printf("projects: retire old domain %s: %v", oldDomain, err)
		}
	}
	http.Redirect(w, r, "/projects/"+p.ID+"?notice="+urlQuery("Saved"), http.StatusSeeOther)
}

func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if id == "" {
		http.Redirect(w, r, "/projects?error=Missing+project+id", http.StatusSeeOther)
		return
	}
	project, err := s.store.GetProject(r.Context(), id)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		log.Printf("projects: delete lookup: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if err == nil && project.Domain != "" && s.proxy != nil {
		if err := s.proxy.Deregister(r.Context(), project.Domain); err != nil {
			log.Printf("projects: deregister %s: %v", project.Domain, err)
			http.Redirect(w, r, "/projects?error="+urlQuery("Could not remove the public route; project was not deleted"), http.StatusSeeOther)
			return
		}
	}
	if err := s.store.DeleteProject(r.Context(), id); err != nil {
		log.Printf("projects: delete: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if err := s.store.DeleteRetiredProxyDomains(r.Context(), id); err != nil {
		log.Printf("projects: clear retired domains: %v", err)
	}
	http.Redirect(w, r, "/projects?notice=Project+deleted", http.StatusSeeOther)
}

// normalizeRuntime maps the wizard's language choice onto the toolchain winify
// provisions. "static" and "auto" mean no toolchain, so they store empty.
func normalizeRuntime(raw string) string {
	switch strings.TrimSpace(raw) {
	case "python", "node", "go", "dotnet":
		return strings.TrimSpace(raw)
	default:
		return ""
	}
}

func validateProject(p config.Project, srv config.Server) error {
	return config.ValidateProject(p, srv)
}

func (s *Server) validateProjectTargetPaths(p config.Project, srv config.Server) error {
	if s.cfg.Deploy.AllowExternalTargetPaths || (srv.Type != config.ServerTypeIIS && srv.Type != config.ServerTypeWindowsService) {
		return nil
	}
	root := s.cfg.Deploy.TargetRoot
	if srv.Type == config.ServerTypeIIS {
		if err := config.ValidateTargetPath(root, p.IISPhysicalPath); err != nil {
			return fmt.Errorf("iis_physical_path: %w", err)
		}
	} else if p.ServiceWorkDir != "" {
		if err := config.ValidateTargetPath(root, p.ServiceWorkDir); err != nil {
			return fmt.Errorf("service_work_dir: %w", err)
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

// formInt parses an optional integer form field; empty means zero.
func formInt(r *http.Request, key string) (int, error) {
	v := strings.TrimSpace(r.FormValue(key))
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", key)
	}
	return n, nil
}

// parsePortMappings parses "host:container" mappings, one per line or
// comma-separated, validating each.
func parsePortMappings(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' })
	var out []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, _, err := config.ParsePortMapping(f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
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

const redactedEnvValue = "[redacted]"

// formatEnv renders env as sorted KEY=VALUE lines for the edit form. Values
// are write-only: the UI shows a stable marker so an existing secret is not
// exposed in HTML, a screenshot, or a browser history/autofill artifact.
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
		fmt.Fprintf(&b, "%s=%s\n", k, redactedEnvValue)
	}
	return strings.TrimRight(b.String(), "\n")
}

// preserveRedactedEnv replaces UI markers with the existing values. Removing a
// line removes the variable; editing a value explicitly stores the new value.
func preserveRedactedEnv(submitted, existing map[string]string) map[string]string {
	if len(submitted) == 0 {
		return nil
	}
	out := make(map[string]string, len(submitted))
	for key, value := range submitted {
		if value == redactedEnvValue {
			if old, ok := existing[key]; ok {
				out[key] = old
			}
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// urlQuery escapes a value for use in a query string.
func urlQuery(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
