package bootstrap

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/tunnel"
)

// TunnelOptions configures the tunnel bootstrap step. Config carries the
// resolved provider API token (a secret) and must not be logged.
type TunnelOptions struct {
	Config   tunnel.Config
	Path     string // connector binary destination on the target
	NSSMPath string
	Dir      string // holds the connector token file
	LogDir   string
	Service  string
	Source   string // local connector binary on the control-center host
	URL      string // download URL when Source is empty
	SHA256   string
	Logf     func(format string, args ...any)
}

// tunnelStep installs the connector binary as a service and provisions a public
// ingress tunnel (wildcard host -> local Caddy) for a host that cannot accept
// inbound connections. Provisioning is done through the provider API in-process;
// the binary and service are handled over the deployment runner.
type tunnelStep struct{ opt TunnelOptions }

func (s tunnelStep) Name() string     { return "tunnel" }
func (s tunnelStep) Privileged() bool { return true }

func (s tunnelStep) service() string {
	if strings.TrimSpace(s.opt.Service) == "" {
		return "winify-tunnel"
	}
	return s.opt.Service
}

func (s tunnelStep) tokenFile() string { return filepath.Join(s.opt.Dir, "token") }

func (s tunnelStep) Check(ctx context.Context, r deployment.Runner) (bool, error) {
	script := fmt.Sprintf(
		"$exe = Test-Path -LiteralPath %s\n"+
			"$svc = Get-CimInstance Win32_Service -Filter %s -ErrorAction SilentlyContinue\n"+
			"$tok = Test-Path -LiteralPath %s\n"+
			"if ($exe -and $null -ne $svc -and $tok) { 'done' } else { 'pending' }\n",
		psQuote(s.opt.Path), psQuote("Name='"+s.service()+"'"), psQuote(s.tokenFile()))
	out, err := r.Run(ctx, script)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "done"), nil
}

func (s tunnelStep) Apply(ctx context.Context, r deployment.Runner) error {
	logf := s.opt.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	data, err := s.fetch(ctx)
	if err != nil {
		return err
	}
	if pin := strings.TrimSpace(s.opt.SHA256); pin != "" && !strings.EqualFold(pin, sha256hex(data)) {
		return fmt.Errorf("connector binary sha256 %s does not match configured tunnel.connector_sha256", sha256hex(data))
	}
	if _, err := deployment.EnsureBinary(ctx, r, s.opt.Path, data, logf); err != nil {
		return err
	}

	// Provider call: create/reuse the tunnel, point the wildcard host at Caddy,
	// and create the wildcard DNS record. The returned token is a secret.
	res, err := tunnel.Provision(ctx, s.opt.Config, logf)
	if err != nil {
		return err
	}
	logf("tunnel: %s -> %s", res.Hostname, s.opt.Config.EffectiveOrigin())

	encoded := base64.StdEncoding.EncodeToString([]byte(res.ConnectToken))
	write := fmt.Sprintf("New-Item -ItemType Directory -Force -Path %s | Out-Null; [IO.File]::WriteAllBytes(%s, [Convert]::FromBase64String(%s))",
		psQuote(s.opt.Dir), psQuote(s.tokenFile()), psQuote(encoded))
	if _, err := r.Run(ctx, write); err != nil {
		return fmt.Errorf("write tunnel token: %w", err)
	}

	_, err = r.Run(ctx, tunnelServiceScript(s.opt.Path, s.opt.NSSMPath, s.opt.Dir, s.tokenFile(), s.opt.LogDir, s.service()))
	return err
}

// fetch returns the connector binary bytes from the local source or a download.
func (s tunnelStep) fetch(ctx context.Context) ([]byte, error) {
	if src := strings.TrimSpace(s.opt.Source); src != "" {
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("read connector source %s: %w", src, err)
		}
		return data, nil
	}
	if u := strings.TrimSpace(s.opt.URL); u != "" {
		return downloadOnce(ctx, u, "cloudflared")
	}
	return nil, fmt.Errorf("tunnel is enabled but tunnel.connector_source or tunnel.connector_url is not set")
}

// tunnelServiceScript installs/updates and starts the connector service via NSSM
// running `cloudflared tunnel run --token-file <file>`.
func tunnelServiceScript(exe, nssmPath, dir, tokenFile, logDir, service string) string {
	args := fmt.Sprintf("tunnel run --token-file \"%s\"", tokenFile)
	guard := func(action string) string {
		return fmt.Sprintf("; if ($LASTEXITCODE -ne 0) { throw %s }", psQuote("nssm "+action+" failed"))
	}
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$nssm = %s\n", psQuote(nssmPath))
	fmt.Fprintf(&b, "if (-not (Get-Service -Name %s -ErrorAction SilentlyContinue)) {\n", psQuote(service))
	fmt.Fprintf(&b, "  & $nssm install %s %s%s\n", psQuote(service), psQuote(exe), guard("install"))
	b.WriteString("}\n")
	fmt.Fprintf(&b, "& $nssm set %s Application %s%s\n", psQuote(service), psQuote(exe), guard("set Application"))
	fmt.Fprintf(&b, "& $nssm set %s AppParameters %s%s\n", psQuote(service), psQuote(args), guard("set AppParameters"))
	fmt.Fprintf(&b, "& $nssm set %s AppDirectory %s%s\n", psQuote(service), psQuote(dir), guard("set AppDirectory"))
	if logDir != "" {
		fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", psQuote(logDir))
		fmt.Fprintf(&b, "& $nssm set %s AppStdout %s%s\n", psQuote(service), psQuote(filepath.Join(logDir, "tunnel.out.log")), guard("set AppStdout"))
		fmt.Fprintf(&b, "& $nssm set %s AppStderr %s%s\n", psQuote(service), psQuote(filepath.Join(logDir, "tunnel.err.log")), guard("set AppStderr"))
	} else {
		fmt.Fprintf(&b, "& $nssm reset %s AppStdout%s\n", psQuote(service), guard("reset AppStdout"))
		fmt.Fprintf(&b, "& $nssm reset %s AppStderr%s\n", psQuote(service), guard("reset AppStderr"))
	}
	fmt.Fprintf(&b, "& $nssm set %s AppExit Default Restart%s\n", psQuote(service), guard("set AppExit"))
	fmt.Fprintf(&b, "& $nssm set %s Start SERVICE_AUTO_START%s\n", psQuote(service), guard("set Start"))
	fmt.Fprintf(&b, "if ((Get-Service -Name %s).Status -ne 'Running') { & $nssm start %s | Out-Null%s }\n",
		psQuote(service), psQuote(service), guard("start"))
	return b.String()
}
