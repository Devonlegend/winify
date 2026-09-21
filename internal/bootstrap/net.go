package bootstrap

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// DetectPublicIP makes a best-effort lookup of this host's public address, used
// to generate sslip.io domains. It returns "" on any failure; callers treat the
// address as optional.
func DetectPublicIP(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
