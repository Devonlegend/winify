package config

import (
	"errors"
	"fmt"
	"os"
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
