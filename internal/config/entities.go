package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Server is a deployment target. Type is "iis" or "docker". Exactly one of
// WinRMEndpoint or SSHHost is populated depending on Type. Credentials are
// referenced by name (credential_ref / ssh_key_ref) and resolved to decrypted
// values only inside internal/auth at connection time — never stored here.
//
// Host is the address the reverse proxy forwards to; it falls back to SSHHost,
// then to the host part of WinRMEndpoint.
type Server struct {
	ID            string `yaml:"id" json:"id"`
	Name          string `yaml:"name" json:"name"`
	Type          string `yaml:"type" json:"type"`
	Host          string `yaml:"host" json:"host,omitempty"`
	WinRMEndpoint string `yaml:"winrm_endpoint" json:"winrm_endpoint,omitempty"`
	WinRMUser     string `yaml:"winrm_user" json:"winrm_user,omitempty"`
	// WinRMTransport is "ntlm" (default) or "basic".
	WinRMTransport string `yaml:"winrm_transport" json:"winrm_transport,omitempty"`
	// WinRMInsecure skips TLS verification of the WinRM endpoint certificate.
	WinRMInsecure bool   `yaml:"winrm_insecure" json:"winrm_insecure,omitempty"`
	CredentialRef string `yaml:"credential_ref" json:"credential_ref,omitempty"`
	// NSSMPath is the permanent path to nssm.exe on a winsvc target. NSSM
	// registers itself as the service binary, so this path must not change
	// once services are installed. Defaults to
	// C:\control-center\tools\nssm.exe.
	NSSMPath string `yaml:"nssm_path" json:"nssm_path,omitempty"`
	// CaddyPath is the path to caddy.exe on a winsvc target when the project
	// requests per-target Caddy. Defaults to C:\control-center\tools\caddy.exe.
	CaddyPath string `yaml:"caddy_path" json:"caddy_path,omitempty"`
	SSHHost   string `yaml:"ssh_host" json:"ssh_host,omitempty"`
	SSHPort   int    `yaml:"ssh_port" json:"ssh_port,omitempty"`
	SSHUser   string `yaml:"ssh_user" json:"ssh_user,omitempty"`
	SSHKeyRef string `yaml:"ssh_key_ref" json:"ssh_key_ref,omitempty"`
	// PublicIP is the server's public address, used to generate a
	// <name>.<ip>.sslip.io domain when the operator has no DNS yet. Optional.
	PublicIP string `yaml:"public_ip" json:"public_ip,omitempty"`
	// Local marks the host winify itself runs on. Local targets are managed by
	// running PowerShell in-process (no WinRM, no credential).
	Local bool `yaml:"local" json:"local,omitempty"`
	// Services lists service names whose status monitoring should report:
	// systemd unit names on Linux, Windows service names on IIS targets.
	Services []string `yaml:"services" json:"services,omitempty"`
	// DiskPath is the filesystem/volume to report disk usage for. Defaults to
	// "/" on Linux and "C:" on Windows.
	DiskPath string `yaml:"disk_path" json:"disk_path,omitempty"`
}

// Project is a deployable unit bound to one server. WebhookSecretRef points at
// an encrypted credential used to verify inbound webhook signatures; the
// secret itself never appears here.
//
// The IIS* fields are only used when the bound server's type is "iis".
type Project struct {
	ID             string `yaml:"id" json:"id"`
	Name           string `yaml:"name" json:"name"`
	ServerID       string `yaml:"server_id" json:"server_id"`
	Strategy       string `yaml:"strategy" json:"strategy,omitempty"` // "dockerfile" or "iis"
	Source         string `yaml:"source" json:"source,omitempty"`     // dockerfile | compose | image
	RepoURL        string `yaml:"repo_url" json:"repo_url"`
	DockerfilePath string `yaml:"dockerfile_path" json:"dockerfile_path,omitempty"`
	ComposePath    string `yaml:"compose_path" json:"compose_path,omitempty"`
	Image          string `yaml:"image" json:"image,omitempty"`
	// PortsExposes is the port the workload listens on inside the container —
	// the target the reverse proxy and health check address. Required.
	PortsExposes int `yaml:"ports_exposes" json:"ports_exposes,omitempty"`
	// PortsMappings are optional "host:container" publishes on the target
	// (for example "8080:3000"). When empty the exposed port is published 1:1
	// so the central reverse proxy can reach the workload over the network.
	PortsMappings   []string `yaml:"ports_mappings" json:"ports_mappings,omitempty"`
	IISSite         string   `yaml:"iis_site" json:"iis_site,omitempty"`
	IISPhysicalPath string   `yaml:"iis_physical_path" json:"iis_physical_path,omitempty"`
	IISAppPool      string   `yaml:"iis_app_pool" json:"iis_app_pool,omitempty"`
	IISService      string   `yaml:"iis_service" json:"iis_service,omitempty"`
	IISBuildCommand string   `yaml:"iis_build_command" json:"iis_build_command,omitempty"`
	IISSourceSubdir string   `yaml:"iis_source_subdir" json:"iis_source_subdir,omitempty"`
	// Service* fields drive the winsvc target (native Windows service via NSSM).
	// ServiceName is the Windows service name; ServiceExe is the executable NSSM
	// runs (absolute path on the target); ServiceWorkDir is the install
	// directory the build output is deployed to; ServiceSourceSubdir is the
	// build-output subdirectory to copy from (default "."); ServiceLogDir
	// receives redirected stdout/stderr.
	ServiceName         string `yaml:"service_name" json:"service_name,omitempty"`
	ServiceExe          string `yaml:"service_exe" json:"service_exe,omitempty"`
	ServiceArgs         string `yaml:"service_args" json:"service_args,omitempty"`
	ServiceWorkDir      string `yaml:"service_work_dir" json:"service_work_dir,omitempty"`
	ServiceBuildCommand string `yaml:"service_build_command" json:"service_build_command,omitempty"`
	ServiceSourceSubdir string `yaml:"service_source_subdir" json:"service_source_subdir,omitempty"`
	ServiceLogDir       string `yaml:"service_log_dir" json:"service_log_dir,omitempty"`
	ServiceAccount      string `yaml:"service_account" json:"service_account,omitempty"`
	// Runtime is the toolchain the build (and, for interpreted languages, the
	// service) needs: python | node | go | dotnet. Empty means none is
	// provisioned. winify installs a missing runtime with winget/choco.
	Runtime string `yaml:"runtime" json:"runtime,omitempty"`
	// CaddyMode selects per-target Caddy behaviour: none | proxy | static.
	CaddyMode string `yaml:"caddy_mode" json:"caddy_mode,omitempty"`
	Branch    string `yaml:"branch" json:"branch,omitempty"`
	Domain    string `yaml:"domain" json:"domain,omitempty"`
	// Port is the legacy host port. It is derived from PortsMappings/PortsExposes
	// on save and kept for backwards compatibility; prefer EffectiveHostPort().
	Port             int    `yaml:"port" json:"port,omitempty"`
	HealthPath       string `yaml:"health_path" json:"health_path,omitempty"`
	WebhookSecretRef string `yaml:"webhook_secret_ref" json:"webhook_secret_ref,omitempty"`
	// Env is passed to the running workload. BuildEnv is passed only to the
	// image build (Docker build args / build-command environment).
	Env      map[string]string `yaml:"env" json:"env,omitempty"`
	BuildEnv map[string]string `yaml:"build_env" json:"build_env,omitempty"`
	// Health check timing. Zero values fall back to the global deploy config.
	HealthIntervalSeconds    int `yaml:"health_interval_seconds" json:"health_interval_seconds,omitempty"`
	HealthTimeoutSeconds     int `yaml:"health_timeout_seconds" json:"health_timeout_seconds,omitempty"`
	HealthRetries            int `yaml:"health_retries" json:"health_retries,omitempty"`
	HealthStartPeriodSeconds int `yaml:"health_start_period_seconds" json:"health_start_period_seconds,omitempty"`
	// ProjectGroup is the Coolify-style "Project" grouping; Environment is the
	// deployment environment (production, staging, ...). Both default when empty.
	ProjectGroup string `yaml:"project_group" json:"project_group,omitempty"`
	Environment  string `yaml:"environment" json:"environment,omitempty"`
	// DisableHealthCheck skips the post-deploy HTTP health check for projects
	// that have no HTTP endpoint (workers, cron jobs, ...).
	DisableHealthCheck bool `yaml:"disable_health_check" json:"disable_health_check,omitempty"`
}

// EffectiveHostPort is the host port the reverse proxy dials and the health
// check probes: the first port mapping's host port when mappings exist,
// otherwise the exposed container port (published 1:1), falling back to the
// legacy Port field.
func (p Project) EffectiveHostPort() int {
	if len(p.PortsMappings) > 0 {
		if host, _, err := ParsePortMapping(p.PortsMappings[0]); err == nil && host > 0 {
			return host
		}
	}
	if p.PortsExposes > 0 {
		return p.PortsExposes
	}
	return p.Port
}

// ParsePortMapping parses a "host:container" mapping. A bare port means the
// host and container ports are the same.
func ParsePortMapping(s string) (host, container int, err error) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) == 1 {
		container, err = strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || container < 1 || container > 65535 {
			return 0, 0, fmt.Errorf("invalid port mapping %q", s)
		}
		return container, container, nil
	}
	host, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || host < 1 || host > 65535 {
		return 0, 0, fmt.Errorf("invalid port mapping %q", s)
	}
	container, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || container < 1 || container > 65535 {
		return 0, 0, fmt.Errorf("invalid port mapping %q", s)
	}
	return host, container, nil
}

var (
	resourceIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	envKeyPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	domainLabelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

// ValidateResourceID keeps IDs safe for URLs, log labels, and target paths.
// IDs are used to construct deployment directories, so accepting path
// separators or dot segments here would allow a project write outside its
// configured work root.
func ValidateResourceID(kind, id string) error {
	if !resourceIDPattern.MatchString(id) {
		return fmt.Errorf("%s must contain only letters, numbers, '.', '_' or '-', and must start with a letter or number", kind)
	}
	return nil
}

// ValidateDomain accepts DNS hostnames used by the Caddy route registrar. It
// intentionally rejects URLs, paths, ports, whitespace, and userinfo so a
// configured domain cannot alter the admin API request or create an invalid
// route matcher.
func ValidateDomain(domain string) error {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil
	}
	if len(domain) > 253 || strings.ContainsAny(domain, "/:@ \t\r\n") {
		return fmt.Errorf("domain must be a hostname without scheme, path, port, or whitespace")
	}
	domain = strings.TrimSuffix(domain, ".")
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain must contain at least two labels")
	}
	for _, label := range labels {
		if !domainLabelPattern.MatchString(label) {
			return fmt.Errorf("domain contains an invalid label %q", label)
		}
	}
	return nil
}

func validateEnvMap(field string, env map[string]string) error {
	for key := range env {
		if !envKeyPattern.MatchString(key) {
			return fmt.Errorf("%s contains invalid environment key %q", field, key)
		}
	}
	return nil
}

func validateRepoURL(repoURL string) error {
	if repoURL == "" {
		return nil
	}
	u, err := url.Parse(repoURL)
	if err == nil && u.User != nil {
		return fmt.Errorf("repo_url must not contain embedded credentials")
	}
	return nil
}

// ValidateServer validates a server before it is persisted or dialed. Keeping
// this in config makes the UI, API, YAML seed, and deployment factory agree on
// the same safety rules.
func ValidateServer(srv Server) error {
	if err := ValidateResourceID("id", srv.ID); err != nil {
		return err
	}
	if strings.TrimSpace(srv.Name) == "" {
		return errors.New("name is required")
	}
	if srv.SSHPort < 0 || srv.SSHPort > 65535 {
		return errors.New("ssh_port must be between 1 and 65535")
	}
	if srv.WinRMTransport != "" && srv.WinRMTransport != "ntlm" && srv.WinRMTransport != "basic" {
		return errors.New("winrm_transport must be ntlm or basic")
	}
	if srv.WinRMEndpoint != "" {
		u, err := url.Parse(srv.WinRMEndpoint)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("winrm_endpoint must be an http or https URL")
		}
		if srv.WinRMTransport == "basic" && u.Scheme != "https" {
			return errors.New("winrm_transport=basic requires an https winrm_endpoint")
		}
	}
	switch srv.Type {
	case ServerTypeDocker:
		if srv.SSHHost == "" {
			return errors.New("ssh_host is required for a docker server")
		}
		if srv.SSHUser == "" {
			return errors.New("ssh_user is required for a docker server")
		}
		if srv.SSHKeyRef == "" {
			return errors.New("ssh_key_ref is required for a docker server")
		}
	case ServerTypeIIS, ServerTypeWindowsService:
		if srv.Local {
			return nil
		}
		if srv.WinRMEndpoint == "" {
			return fmt.Errorf("winrm_endpoint is required for a %s server", srv.Type)
		}
		if srv.WinRMUser == "" {
			return fmt.Errorf("winrm_user is required for a %s server", srv.Type)
		}
		if srv.CredentialRef == "" {
			return fmt.Errorf("credential_ref is required for a %s server", srv.Type)
		}
	default:
		return fmt.Errorf("type must be %q, %q or %q", ServerTypeDocker, ServerTypeIIS, ServerTypeWindowsService)
	}
	return nil
}

// ValidateProject validates a project against its selected target before it
// is persisted. It intentionally lives beside the entity definitions so YAML
// imports and API writes cannot bypass the UI's checks.
func ValidateProject(p Project, srv Server) error {
	if err := ValidateResourceID("id", p.ID); err != nil {
		return err
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(p.ServerID) == "" {
		return errors.New("server is required")
	}
	if err := ValidateDomain(p.Domain); err != nil {
		return err
	}
	if err := validateRepoURL(p.RepoURL); err != nil {
		return err
	}
	if err := validateEnvMap("env", p.Env); err != nil {
		return err
	}
	if err := validateEnvMap("build_env", p.BuildEnv); err != nil {
		return err
	}
	for _, mapping := range p.PortsMappings {
		if _, _, err := ParsePortMapping(mapping); err != nil {
			return err
		}
	}
	if p.PortsExposes < 0 || (p.PortsExposes > 0 && p.PortsExposes > 65535) {
		return errors.New("ports_exposes must be between 1 and 65535")
	}
	port := p.EffectiveHostPort()
	if port < 0 || port > 65535 {
		return errors.New("ports_exposes must be between 1 and 65535")
	}
	if port == 0 && (!p.DisableHealthCheck || p.Domain != "") {
		return errors.New("ports_exposes must be between 1 and 65535 unless health check is disabled and no domain is configured")
	}

	switch srv.Type {
	case ServerTypeIIS:
		if p.RepoURL == "" {
			return errors.New("repo_url is required")
		}
		if p.IISPhysicalPath == "" {
			return errors.New("iis_physical_path is required for an IIS project")
		}
		if p.IISAppPool == "" {
			return errors.New("iis_app_pool is required for an IIS project")
		}
		if len(p.Env) > 0 {
			return errors.New("runtime env is not supported for IIS projects; configure the application through web.config or the build")
		}
	case ServerTypeWindowsService:
		if p.RepoURL == "" {
			return errors.New("repo_url is required for a Windows service project")
		}
		staticOnly := p.ServiceExe == "" && p.CaddyMode == CaddyModeStatic
		if !staticOnly && p.ServiceName == "" {
			return errors.New("service_name is required for a Windows service project")
		}
		if !staticOnly && p.ServiceExe == "" {
			return errors.New("service_exe is required for a Windows service project")
		}
		if p.ServiceWorkDir == "" {
			return errors.New("service_work_dir is required for a Windows service project")
		}
		switch p.CaddyMode {
		case "", CaddyModeNone, CaddyModeStatic:
		case CaddyModeProxy:
			return fmt.Errorf("caddy_mode=%q is not supported yet", CaddyModeProxy)
		default:
			return fmt.Errorf("caddy_mode must be %q or %q", CaddyModeNone, CaddyModeStatic)
		}
	default:
		switch p.Source {
		case ProjectSourceImage:
			if strings.TrimSpace(p.Image) == "" {
				return errors.New("image is required for the image deploy source")
			}
		case ProjectSourceCompose:
			if p.RepoURL == "" {
				return errors.New("repo_url is required for the compose deploy source")
			}
		case "", ProjectSourceDockerfile:
			if p.RepoURL == "" {
				return errors.New("repo_url is required for the dockerfile deploy source")
			}
		default:
			return errors.New("source must be dockerfile, compose or image")
		}
	}
	if p.Runtime != "" && p.Runtime != "python" && p.Runtime != "node" && p.Runtime != "go" && p.Runtime != "dotnet" {
		return fmt.Errorf("runtime must be python, node, go, or dotnet")
	}
	return nil
}

type serversFile struct {
	Servers []Server `yaml:"servers"`
}

type projectsFile struct {
	Projects []Project `yaml:"projects"`
}

// LoadServers reads servers.yaml. A missing file yields an empty list, not an
// error, so the server can boot before any target is configured.
func LoadServers(path string) ([]Server, error) {
	var f serversFile
	if err := decodeFile(path, &f); err != nil {
		return nil, err
	}
	return f.Servers, nil
}

// LoadProjects reads projects.yaml with the same missing-file semantics.
func LoadProjects(path string) ([]Project, error) {
	var f projectsFile
	if err := decodeFile(path, &f); err != nil {
		return nil, err
	}
	return f.Projects, nil
}

func decodeFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
