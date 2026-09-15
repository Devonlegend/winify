package config

import (
	"errors"
	"fmt"
	"os"

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
	SSHHost       string `yaml:"ssh_host" json:"ssh_host,omitempty"`
	SSHPort       int    `yaml:"ssh_port" json:"ssh_port,omitempty"`
	SSHUser       string `yaml:"ssh_user" json:"ssh_user,omitempty"`
	SSHKeyRef     string `yaml:"ssh_key_ref" json:"ssh_key_ref,omitempty"`
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
	// ContainerPort is the port the workload listens on inside the container.
	// Defaults to Port when unset; needed for prebuilt images (e.g. nginx:80).
	ContainerPort    int               `yaml:"container_port" json:"container_port,omitempty"`
	IISSite          string            `yaml:"iis_site" json:"iis_site,omitempty"`
	IISPhysicalPath  string            `yaml:"iis_physical_path" json:"iis_physical_path,omitempty"`
	IISAppPool       string            `yaml:"iis_app_pool" json:"iis_app_pool,omitempty"`
	IISService       string            `yaml:"iis_service" json:"iis_service,omitempty"`
	IISBuildCommand  string            `yaml:"iis_build_command" json:"iis_build_command,omitempty"`
	IISSourceSubdir  string            `yaml:"iis_source_subdir" json:"iis_source_subdir,omitempty"`
	Branch           string            `yaml:"branch" json:"branch,omitempty"`
	Domain           string            `yaml:"domain" json:"domain,omitempty"`
	Port             int               `yaml:"port" json:"port"`
	HealthPath       string            `yaml:"health_path" json:"health_path,omitempty"`
	WebhookSecretRef string            `yaml:"webhook_secret_ref" json:"webhook_secret_ref,omitempty"`
	Env              map[string]string `yaml:"env" json:"env,omitempty"`
	// ProjectGroup is the Coolify-style "Project" grouping; Environment is the
	// deployment environment (production, staging, ...). Both default when empty.
	ProjectGroup string `yaml:"project_group" json:"project_group,omitempty"`
	Environment  string `yaml:"environment" json:"environment,omitempty"`
	// DisableHealthCheck skips the post-deploy HTTP health check for projects
	// that have no HTTP endpoint (workers, cron jobs, ...).
	DisableHealthCheck bool `yaml:"disable_health_check" json:"disable_health_check,omitempty"`
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
