package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadServers(t *testing.T) {
	p := writeTemp(t, "servers.yaml", `
servers:
  - id: server-001
    name: "Production IIS Server"
    type: iis
    winrm_endpoint: "https://10.0.0.5:5986/wsman"
    credential_ref: "vault:server-001-winrm"
  - id: server-002
    name: "Docker Host"
    type: docker
    ssh_host: "10.0.0.9"
    ssh_user: "deploy"
    ssh_key_ref: "vault:server-002-ssh"
`)
	servers, err := LoadServers(p)
	if err != nil {
		t.Fatalf("LoadServers: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(servers))
	}
	iis := servers[0]
	if iis.ID != "server-001" || iis.Type != "iis" || iis.WinRMEndpoint != "https://10.0.0.5:5986/wsman" {
		t.Errorf("iis server = %+v", iis)
	}
	if iis.CredentialRef != "vault:server-001-winrm" {
		t.Errorf("credential_ref = %q", iis.CredentialRef)
	}
	docker := servers[1]
	if docker.SSHHost != "10.0.0.9" || docker.SSHUser != "deploy" || docker.SSHKeyRef != "vault:server-002-ssh" {
		t.Errorf("docker server = %+v", docker)
	}
}

func TestLoadProjects(t *testing.T) {
	p := writeTemp(t, "projects.yaml", `
projects:
  - id: proj-001
    name: "Storefront API"
    server_id: server-002
    strategy: dockerfile
    repo_url: "https://github.com/acme/storefront"
    dockerfile_path: "deploy/Dockerfile"
  - id: proj-002
    name: "Legacy Portal"
    server_id: server-001
    strategy: iis
    iis_site: "Default Web Site/portal"
`)
	projects, err := LoadProjects(p)
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2", len(projects))
	}
	if projects[0].Strategy != "dockerfile" || projects[0].DockerfilePath != "deploy/Dockerfile" {
		t.Errorf("project[0] = %+v", projects[0])
	}
	if projects[1].IISSite != "Default Web Site/portal" {
		t.Errorf("project[1] = %+v", projects[1])
	}
}

func TestLoadEntitiesMissingFilesAreEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	servers, err := LoadServers(missing)
	if err != nil || len(servers) != 0 {
		t.Fatalf("LoadServers missing = (%v, %v), want empty, nil", servers, err)
	}
	projects, err := LoadProjects(missing)
	if err != nil || len(projects) != 0 {
		t.Fatalf("LoadProjects missing = (%v, %v), want empty, nil", projects, err)
	}
}
