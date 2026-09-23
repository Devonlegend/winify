package bootstrap

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// PublicIP is the outcome of a public-address lookup.
type PublicIP struct {
	IP     string // detected public IPv4, empty when none
	OK     bool   // true when IP is a usable public address
	Reason string // why it is unusable, when !OK
}

// publicIPEchoURL is the endpoint used to learn the egress address. It is a
// variable so a test can point it at a local server.
var publicIPEchoURL = "https://ipv4.icanhazip.com"

// DetectPublicIP looks up this host's public IPv4 address, used to generate
// sslip.io domains.
//
// The request is bound to the default-route interface, so a VPN or overlay on
// another interface (NordVPN, Tailscale, ...) cannot supply a misleading
// answer. The result is validated: private, CGNAT, loopback and link-local
// addresses are rejected with a reason instead of being handed out.
func DetectPublicIP(ctx context.Context) PublicIP {
	local, err := defaultRouteLocalIP()
	if err != nil {
		return PublicIP{Reason: fmt.Sprintf("could not determine the default interface: %v", err)}
	}
	ip, err := lookupPublicIPv4(ctx, local)
	if err != nil {
		return PublicIP{Reason: err.Error()}
	}
	if reason := publicIPv4Reason(ip); reason != "" {
		return PublicIP{IP: ip, Reason: reason}
	}
	return PublicIP{IP: ip, OK: true}
}

// defaultRouteLocalIP returns the local IPv4 the OS would use to reach the
// internet. Dialing UDP sends no packets; it only asks the routing table.
func defaultRouteLocalIP() (net.IP, error) {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return nil, fmt.Errorf("no local address")
	}
	return addr.IP, nil
}

// lookupPublicIPv4 queries the echo service from the given local address.
func lookupPublicIPv4(ctx context.Context, local net.IP) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: local}}
	client := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicIPEchoURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("public IP lookup failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("public IP lookup status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(b))
	if ip == "" {
		return "", fmt.Errorf("public IP lookup returned nothing")
	}
	return ip, nil
}

// cgnat is 100.64.0.0/10, the carrier-grade NAT range (used by Tailscale and
// many ISPs). net.IP.IsPrivate does not cover it.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// publicIPv4Reason returns "" when ipStr is a usable public IPv4, or a short
// explanation why it is not.
func publicIPv4Reason(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil || ip.To4() == nil {
		return "not an IPv4 address"
	}
	switch {
	case ip.IsLoopback():
		return "loopback address"
	case ip.IsLinkLocalUnicast():
		return "link-local address"
	case ip.IsPrivate():
		return "private address (the host is behind NAT)"
	case ip.IsUnspecified(), ip.IsMulticast():
		return "unusable address"
	}
	if cgnat.Contains(ip) {
		return "carrier-grade NAT address (not publicly reachable)"
	}
	return ""
}
