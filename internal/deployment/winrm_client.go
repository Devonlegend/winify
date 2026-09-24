package deployment

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/masterzen/winrm"

	"github.com/Devonlegend/winify/internal/config"
)

// WinRMRunner runs PowerShell on a Windows host over WinRM. It can do anything
// the WinRM account can do there — stop services, modify IIS, run builds. Treat
// WinRM credentials as highly privileged.
type WinRMRunner struct {
	client *winrm.Client
}

// DialWinRM connects to a Windows target. password is the decrypted credential
// from the Phase 1 store and is never logged.
func DialWinRM(srv config.Server, password string) (*WinRMRunner, error) {
	host, port, https, err := parseWinRMEndpoint(srv.WinRMEndpoint)
	if err != nil {
		return nil, err
	}
	if !https && !srv.WinRMInsecure {
		return nil, fmt.Errorf("winrm HTTP endpoint requires winrm_insecure: true")
	}
	endpoint := winrm.NewEndpoint(host, port, https, srv.WinRMInsecure, nil, nil, nil, 0)

	params := winrm.NewParameters("PT120S", "en-US", 153600)
	// The library's default dialer has a 30s connect timeout and ignores the
	// caller's context, which would stall a monitoring poll on an unreachable
	// host. Bound the connect explicitly.
	params.Dial = (&net.Dialer{Timeout: 10 * time.Second}).Dial
	switch strings.ToLower(srv.WinRMTransport) {
	case "", "ntlm":
		params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
	case "basic":
		if !https {
			return nil, fmt.Errorf("winrm basic transport requires an HTTPS endpoint")
		}
		// The default transporter authenticates with HTTP Basic.
	default:
		return nil, fmt.Errorf("unsupported winrm transport %q", srv.WinRMTransport)
	}

	client, err := winrm.NewClientWithParameters(endpoint, srv.WinRMUser, password, params)
	if err != nil {
		return nil, fmt.Errorf("winrm client: %w", err)
	}
	return &WinRMRunner{client: client}, nil
}

// Run executes a PowerShell script and returns combined output. The WinRM
// library base64-encodes the script, so no shell quoting is required here.
func (r *WinRMRunner) Run(ctx context.Context, script string) (string, error) {
	stdout, stderr, code, err := r.client.RunPSWithContext(ctx, script)
	out := strings.TrimRight(stdout, "\r\n")
	if strings.TrimSpace(stderr) != "" {
		if out != "" {
			out += "\n"
		}
		out += strings.TrimSpace(stderr)
	}
	out = capCommandOutput(out)
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, fmt.Errorf("powershell exited with code %d", code)
	}
	return out, nil
}

// Close is a no-op: the WinRM client opens a shell per command.
func (r *WinRMRunner) Close() error { return nil }

// parseWinRMEndpoint splits a winrm_endpoint URL into host, port and scheme.
// Defaults follow WinRM convention: https on 5986, http on 5985.
func parseWinRMEndpoint(endpoint string) (host string, port int, https bool, err error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return "", 0, false, fmt.Errorf("invalid winrm_endpoint %q", endpoint)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", 0, false, fmt.Errorf("winrm endpoint %q must use http or https", endpoint)
	}
	https = u.Scheme == "https"
	port = 5985
	if https {
		port = 5986
	}
	if u.Port() != "" {
		p, perr := strconv.Atoi(u.Port())
		if perr != nil {
			return "", 0, false, fmt.Errorf("invalid winrm port in %q", endpoint)
		}
		port = p
	}
	return u.Hostname(), port, https, nil
}
