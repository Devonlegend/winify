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
// It tries each physical interface, not just the default route, because a VPN
// or overlay that happens to be the default route (NordVPN, Tailscale, ...)
// would otherwise report the tunnel's address instead of this host's. The
// result is validated: private, CGNAT, loopback and link-local addresses are
// rejected with a reason instead of being handed out.
func DetectPublicIP(ctx context.Context) PublicIP {
	candidates, err := publicIPCandidates()
	if err != nil {
		return PublicIP{Reason: err.Error()}
	}
	if len(candidates) == 0 {
		return PublicIP{Reason: "no usable network interface (set public_ip manually)"}
	}
	reason := "public IP lookup failed"
	for _, local := range candidates {
		ip, err := lookupPublicIPv4(ctx, local)
		if err != nil {
			reason = err.Error()
			continue
		}
		if r := publicIPv4Reason(ip); r != "" {
			reason = r
			continue
		}
		return PublicIP{IP: ip, OK: true}
	}
	return PublicIP{Reason: reason}
}

// publicIPCandidates returns local IPv4 addresses that can reach the internet,
// with the default-route interface ordered first. VPN/overlay and virtual
// adapters are skipped so a tunnel cannot supply a misleading answer.
func publicIPCandidates() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}
	defaultIP, _ := defaultRouteLocalIP() // best effort; may be nil
	var candidates []net.IP
	seen := make(map[string]bool)
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isVirtualInterface(ifi.Name) {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if seen[ip.String()] {
				continue
			}
			seen[ip.String()] = true
			candidates = append(candidates, ip)
		}
	}
	if idx := indexOfIP(candidates, defaultIP); idx > 0 {
		def := candidates[idx]
		candidates = append([]net.IP{def}, append(candidates[:idx], candidates[idx+1:]...)...)
	}
	return candidates, nil
}

// indexOfIP returns the position of ip in list, or -1 when absent or nil.
func indexOfIP(list []net.IP, ip net.IP) int {
	if ip == nil {
		return -1
	}
	for i, candidate := range list {
		if candidate.Equal(ip) {
			return i
		}
	}
	return -1
}

// virtualInterfaceMarkers are substrings of adapter names that are VPNs,
// overlays or virtual switches rather than a physical internet interface.
var virtualInterfaceMarkers = []string{
	"vpn", "nordlynx", "wireguard", "wintun", "openvpn", "tailscale",
	"zerotier", "hamachi", "mullvad", "proton", "surfshark", "windscribe",
	"expressvpn", "hyper-v", "vethernet", "default switch", "wsl", "vmware",
	"virtualbox", "docker", "veth", "br-", "virbr", "cni", "tun", "tap",
	"loopback", "bluetooth", "teredo", "isatap",
}

// isVirtualInterface reports whether name belongs to a VPN, overlay or virtual
// adapter. Physical adapters ("Wi-Fi", "Ethernet", "en0", ...) are not matched.
func isVirtualInterface(name string) bool {
	n := strings.ToLower(name)
	for _, marker := range virtualInterfaceMarkers {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
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
