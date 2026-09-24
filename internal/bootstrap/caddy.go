package bootstrap

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
)

// caddyService is the NSSM-managed service name for the reverse proxy.
const caddyService = "winify-caddy"

func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// caddyStep provisions the Caddy reverse proxy: the binary, a base Caddyfile
// and an NSSM-managed service that keeps it running. The deploy pipeline then
// adds routes through Caddy's admin API.
type caddyStep struct {
	path     string // caddy.exe destination on the target
	nssmPath string
	dir      string // holds the Caddyfile
	logDir   string
	admin    string
	source   string // local caddy.exe on the control-center host
	url      string // release zip to download when source is empty
	sha256   string
	logf     func(format string, args ...any)
}

func (s caddyStep) Name() string     { return "caddy" }
func (s caddyStep) Privileged() bool { return true }

func (s caddyStep) Check(ctx context.Context, r deployment.Runner) (bool, error) {
	script := fmt.Sprintf(
		"$exe = Test-Path -LiteralPath %s\n"+
			"$svc = Get-CimInstance Win32_Service -Filter %s -ErrorAction SilentlyContinue\n"+
			"if ($exe -and $null -ne $svc) { 'done' } else { 'pending' }\n",
		psQuote(s.path), psQuote("Name='"+caddyService+"'"))
	out, err := r.Run(ctx, script)
	if err != nil {
		return false, err
	}
	if !strings.Contains(out, "done") {
		return false, nil
	}
	if pin := strings.TrimSpace(s.sha256); pin != "" {
		decoded, err := hex.DecodeString(pin)
		if err != nil || len(decoded) != sha256.Size {
			return false, fmt.Errorf("invalid caddy sha256 %q", pin)
		}
		got, err := deployment.RemoteFileSHA256(ctx, r, s.path)
		if err != nil {
			return false, err
		}
		return strings.EqualFold(got, pin), nil
	}
	return true, nil
}

func (s caddyStep) Apply(ctx context.Context, r deployment.Runner) error {
	if err := config.ValidateCaddyAdmin(s.admin); err != nil {
		return err
	}
	data, err := s.fetch(ctx)
	if err != nil {
		return err
	}
	if pin := strings.TrimSpace(s.sha256); pin != "" && !strings.EqualFold(pin, sha256hex(data)) {
		return fmt.Errorf("caddy binary sha256 %s does not match configured bootstrap.caddy.sha256", sha256hex(data))
	}
	logf := s.logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if _, err := deployment.EnsureBinary(ctx, r, s.path, data, logf); err != nil {
		return err
	}

	configFile := filepath.Join(s.dir, "Caddyfile")
	encoded := base64.StdEncoding.EncodeToString([]byte(caddyConfigFile(s.admin)))
	write := fmt.Sprintf("New-Item -ItemType Directory -Force -Path %s | Out-Null; [IO.File]::WriteAllBytes(%s, [Convert]::FromBase64String(%s))",
		psQuote(s.dir), psQuote(configFile), psQuote(encoded))
	if _, err := r.Run(ctx, write); err != nil {
		return fmt.Errorf("write Caddyfile: %w", err)
	}

	_, err = r.Run(ctx, caddyServiceScript(s.path, s.nssmPath, s.dir, configFile, s.logDir))
	return err
}

// fetch returns the caddy.exe bytes from the local source or a downloaded
// release zip.
func (s caddyStep) fetch(ctx context.Context) ([]byte, error) {
	if src := strings.TrimSpace(s.source); src != "" {
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("read caddy source %s: %w", src, err)
		}
		return data, nil
	}
	if u := strings.TrimSpace(s.url); u != "" {
		if s.logf != nil {
			s.logf("downloading caddy from %s (this can take a minute)", u)
		}
		return downloadCaddy(ctx, u)
	}
	return nil, fmt.Errorf("caddy is enabled but bootstrap.caddy.source or bootstrap.caddy.url is not set")
}

// downloadCaddy fetches a URL with a few retries (release CDNs reset long
// connections) and returns caddy.exe, extracting it when the payload is a zip.
func downloadCaddy(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		data, err := downloadOnce(ctx, url)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 2 * time.Second):
		}
	}
	return nil, lastErr
}

// downloadOnce performs a single bounded download.
func downloadOnce(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("caddy download URL must be HTTPS")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// #nosec G107 -- the URL is operator-configured, not user input.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("download caddy: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download caddy: %w", err)
	}
	defer resp.Body.Close()
	if resp.Request != nil && resp.Request.URL != nil && resp.Request.URL.Scheme != "https" {
		return nil, fmt.Errorf("caddy download redirected to a non-HTTPS URL")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download caddy: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20))
	if err != nil {
		return nil, fmt.Errorf("download caddy: %w", err)
	}
	if len(data) >= 4 && bytes.Equal(data[:4], []byte("PK\x03\x04")) {
		return extractExe(data)
	}
	return data, nil
}

// extractExe returns the first .exe inside a zip archive.
func extractExe(zipData []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("read caddy zip: %w", err)
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".exe") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", f.Name, err)
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("no .exe found in caddy archive")
}

// caddyConfigFile renders a minimal Caddyfile: just the admin API, so the
// deploy pipeline can add routes through it.
func caddyConfigFile(admin string) string {
	if strings.TrimSpace(admin) == "" {
		admin = "127.0.0.1:2019"
	}
	return fmt.Sprintf("{\n\tadmin %s\n}\n", admin)
}

// caddyServiceScript installs/updates and starts the Caddy service via NSSM.
func caddyServiceScript(caddyPath, nssmPath, dir, configFile, logDir string) string {
	args := fmt.Sprintf("run --config %s --adapter caddyfile", configFile)
	guard := func(action string) string {
		return fmt.Sprintf("; if ($LASTEXITCODE -ne 0) { throw %s }", psQuote("nssm "+action+" failed"))
	}
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$nssm = %s\n", psQuote(nssmPath))
	fmt.Fprintf(&b, "if (-not (Get-Service -Name %s -ErrorAction SilentlyContinue)) {\n", psQuote(caddyService))
	fmt.Fprintf(&b, "  & $nssm install %s %s%s\n", psQuote(caddyService), psQuote(caddyPath), guard("install"))
	b.WriteString("}\n")
	fmt.Fprintf(&b, "& $nssm set %s Application %s%s\n", psQuote(caddyService), psQuote(caddyPath), guard("set Application"))
	fmt.Fprintf(&b, "& $nssm set %s AppParameters %s%s\n", psQuote(caddyService), psQuote(args), guard("set AppParameters"))
	fmt.Fprintf(&b, "& $nssm set %s AppDirectory %s%s\n", psQuote(caddyService), psQuote(dir), guard("set AppDirectory"))
	if logDir != "" {
		fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", psQuote(logDir))
		fmt.Fprintf(&b, "& $nssm set %s AppStdout %s%s\n", psQuote(caddyService), psQuote(filepath.Join(logDir, "caddy.out.log")), guard("set AppStdout"))
		fmt.Fprintf(&b, "& $nssm set %s AppStderr %s%s\n", psQuote(caddyService), psQuote(filepath.Join(logDir, "caddy.err.log")), guard("set AppStderr"))
	} else {
		fmt.Fprintf(&b, "& $nssm reset %s AppStdout%s\n", psQuote(caddyService), guard("reset AppStdout"))
		fmt.Fprintf(&b, "& $nssm reset %s AppStderr%s\n", psQuote(caddyService), guard("reset AppStderr"))
	}
	fmt.Fprintf(&b, "& $nssm set %s AppExit Default Restart%s\n", psQuote(caddyService), guard("set AppExit"))
	fmt.Fprintf(&b, "& $nssm set %s Start SERVICE_AUTO_START%s\n", psQuote(caddyService), guard("set Start"))
	fmt.Fprintf(&b, "if ((Get-Service -Name %s).Status -ne 'Running') { & $nssm start %s | Out-Null%s }\n",
		psQuote(caddyService), psQuote(caddyService), guard("start"))
	return b.String()
}

// firewallStep opens inbound TCP ports for the reverse proxy.
type firewallStep struct{ ports []int }

func (s firewallStep) Name() string     { return "firewall" }
func (s firewallStep) Privileged() bool { return true }

func firewallRuleName(port int) string { return fmt.Sprintf("winify-caddy-%d", port) }

func (s firewallStep) Check(ctx context.Context, r deployment.Runner) (bool, error) {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n$ok=$true\n")
	for _, p := range s.ports {
		fmt.Fprintf(&b, "if (-not (Get-NetFirewallRule -DisplayName %s -ErrorAction SilentlyContinue)) { $ok=$false }\n", psQuote(firewallRuleName(p)))
	}
	b.WriteString("if ($ok) { 'done' } else { 'pending' }\n")
	out, err := r.Run(ctx, b.String())
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "done"), nil
}

func (s firewallStep) Apply(ctx context.Context, r deployment.Runner) error {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	for _, p := range s.ports {
		name := psQuote(firewallRuleName(p))
		fmt.Fprintf(&b, "if (-not (Get-NetFirewallRule -DisplayName %s -ErrorAction SilentlyContinue)) { New-NetFirewallRule -DisplayName %s -Direction Inbound -Protocol TCP -LocalPort %d -Action Allow | Out-Null }\n",
			name, name, p)
	}
	_, err := r.Run(ctx, b.String())
	return err
}
