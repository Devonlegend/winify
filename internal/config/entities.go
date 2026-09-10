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
	ID            string `yaml:"id"`
	Name          string `yaml:"name"`
	Type          string `yaml:"type"`
	Host          string `yaml:"host"`
	WinRMEndpoint string `yaml:"winrm_endpoint"`
	WinRMUser     string `yaml:"winrm_user"`
	// WinRMTransport is "ntlm" (default) or "basic".
	WinRMTransport string `yaml:"winrm_transport"`
	// WinRMInsecure skips TLS verification of the WinRM endpoint certificate.
	WinRMInsecure bool   `yaml:"winrm_insecure"`
	CredentialRef string `yaml:"credential_ref"`
	SSHHost       string `yaml:"ssh_host"`
	SSHPort       int    `yaml:"ssh_port"`
	SSHUser       string `yaml:"ssh_user"`
	SSHKeyRef     string `yaml:"ssh_key_ref"`
	// Services lists service names whose status monitoring should report:
	// systemd unit names on Linux, Windows service names on IIS targets.
	Services []string `yaml:"services"`
	// DiskPath is the filesystem/volume to report disk usage for. Defaults to
	// "/" on Linux and "C:" on Windows.
	DiskPath string `yaml:"disk_path"`
}

// Project is a deployable unit bound to one server. WebhookSecretRef points at
// an encrypted credential used to verify inbound webhook signatures; the
// secret itself never appears here.
//
// The IIS* fields are only used when the bound server's type is "iis".
type Project struct {
	ID               string            `yaml:"id"`
	Name             string            `yaml:"name"`
	ServerID         string            `yaml:"server_id"`
	Strategy         string            `yaml:"strategy"` // "dockerfile" or "iis"
	RepoURL          string            `yaml:"repo_url"`
	DockerfilePath   string            `yaml:"dockerfile_path"`
	IISSite          string            `yaml:"iis_site"`
	IISPhysicalPath  string            `yaml:"iis_physical_path"`
	IISAppPool       string            `yaml:"iis_app_pool"`
	IISService       string            `yaml:"iis_service"`
	IISBuildCommand  string            `yaml:"iis_build_command"`
	IISSourceSubdir  string            `yaml:"iis_source_subdir"`
	Branch           string            `yaml:"branch"`
	Domain           string            `yaml:"domain"`
	Port             int               `yaml:"port"`
	HealthPath       string            `yaml:"health_path"`
	WebhookSecretRef string            `yaml:"webhook_secret_ref"`
	Env              map[string]string `yaml:"env"`
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
