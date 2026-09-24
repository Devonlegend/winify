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

func TestParsePortMapping(t *testing.T) {
	host, container, err := ParsePortMapping("8080:3000")
	if err != nil || host != 8080 || container != 3000 {
		t.Fatalf("8080:3000 = (%d, %d, %v)", host, container, err)
	}
	host, container, err = ParsePortMapping("3000")
	if err != nil || host != 3000 || container != 3000 {
		t.Fatalf("bare port = (%d, %d, %v)", host, container, err)
	}
	for _, bad := range []string{"", "abc", "0:80", "80:", "70000:80", "80:0"} {
		if _, _, err := ParsePortMapping(bad); err == nil {
			t.Errorf("ParsePortMapping(%q) succeeded, want error", bad)
		}
	}
}

func TestEffectiveHostPort(t *testing.T) {
	cases := []struct {
		name string
		p    Project
		want int
	}{
		{"mapping wins", Project{PortsMappings: []string{"8080:3000"}, PortsExposes: 3000}, 8080},
		{"exposes fallback", Project{PortsExposes: 3000}, 3000},
		{"legacy port", Project{Port: 9000}, 9000},
		{"mapping over legacy", Project{PortsMappings: []string{"7070:3000"}, Port: 9000}, 7070},
	}
	for _, c := range cases {
		if got := c.p.EffectiveHostPort(); got != c.want {
			t.Errorf("%s: EffectiveHostPort = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestValidateResourceInputs(t *testing.T) {
	if err := ValidateResourceID("id", "../escape"); err == nil {
		t.Fatal("path traversal ID was accepted")
	}
	if err := ValidateDomain("https://example.com"); err == nil {
		t.Fatal("URL was accepted as a domain")
	}
	if err := ValidateProject(Project{ID: "p-image", Name: "Image", ServerID: "s1", Source: ProjectSourceImage, Image: "registry.example:5000/team/app:tag", DisableHealthCheck: true}, Server{ID: "s1", Type: ServerTypeDocker}); err != nil {
		t.Fatalf("private registry image reference rejected: %v", err)
	}
	for _, repo := range []string{"ext::sh -c whoami", "file:///tmp/repo", "http://example.com/repo.git"} {
		if err := ValidateRepoURL(repo); err == nil {
			t.Errorf("unsafe repository URL %q was accepted", repo)
		}
	}
	if err := ValidateProject(Project{
		ID: "p1", Name: "App", ServerID: "s1", Source: ProjectSourceImage,
		Image: "nginx", DisableHealthCheck: true,
		Env: map[string]string{"BAD-KEY": "value"},
	}, Server{ID: "s1", Type: ServerTypeDocker}); err == nil {
		t.Fatal("invalid environment key was accepted")
	}
	if err := ValidateServer(Server{ID: "s1", Name: "IIS", Type: ServerTypeIIS,
		WinRMEndpoint: "http://host:5985/wsman", WinRMTransport: "basic",
		WinRMUser: "u", CredentialRef: "vault:x"}); err == nil {
		t.Fatal("Basic WinRM over HTTP was accepted")
	}
}

func TestNormalizeWinRMEndpoint(t *testing.T) {
	cases := []struct {
		raw      string
		insecure bool
		want     string
	}{
		{"win01", false, "https://win01:5986/wsman"},
		{"win01:5986", false, "https://win01:5986/wsman"},
		{"https://win01:5986/wsman", false, "https://win01:5986/wsman"},
		{"https://win01", false, "https://win01:5986/wsman"},
		{"http://win01:5985/wsman", true, "http://win01:5985/wsman"},
	}
	for _, tc := range cases {
		got, err := NormalizeWinRMEndpoint(tc.raw, tc.insecure)
		if err != nil || got != tc.want {
			t.Errorf("NormalizeWinRMEndpoint(%q, %v) = %q, %v; want %q", tc.raw, tc.insecure, got, err, tc.want)
		}
	}
	if _, err := NormalizeWinRMEndpoint("http://win01", false); err == nil {
		t.Fatal("http endpoint accepted without insecure opt-in")
	}
	if _, err := NormalizeWinRMEndpoint("", false); err == nil {
		t.Fatal("empty endpoint accepted")
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
