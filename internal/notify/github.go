package notify

import (
	"context"
	"fmt"
	"strings"

	"github.com/Devonlegend/winify/internal/githubapp"
)

// GitHubNotifier posts commit statuses (the green/red checks on commits and
// PRs) through the configured GitHub App. Projects without a linked repo are
// skipped, as are events without a full-length commit SHA.
type GitHubNotifier struct {
	AppID          string
	InstallationID int64
	// PrivateKey resolves the App's PEM private key (decrypted from the
	// credential store) on demand.
	PrivateKey func(ctx context.Context) (string, error)
	APIURL     string
	// Context is the status check name shown in GitHub. Default "winify/deploy".
	Context string
}

// NotifyDeploy maps winify statuses to GitHub states and posts the status.
func (n *GitHubNotifier) NotifyDeploy(ctx context.Context, ev DeployEvent) error {
	if ev.GitHubRepo == "" || len(ev.CommitSHA) < 7 || !isHex(ev.CommitSHA) {
		return nil
	}
	state := map[string]string{
		"pending": "pending",
		"success": "success",
		"failed":  "failure",
	}[ev.Status]
	if state == "" {
		return nil
	}
	if n.PrivateKey == nil || n.AppID == "" || n.InstallationID == 0 {
		return fmt.Errorf("github notifier is not configured")
	}

	pemKey, err := n.PrivateKey(ctx)
	if err != nil {
		return fmt.Errorf("resolve github app key: %w", err)
	}
	client := githubapp.NewClient(n.APIURL)
	token, err := client.InstallationToken(ctx, n.AppID, pemKey, n.InstallationID)
	if err != nil {
		return err
	}

	statusCtx := n.Context
	if statusCtx == "" {
		statusCtx = "winify/deploy"
	}
	description := "winify deployment " + ev.Status
	if ev.Status == "failed" && ev.Error != "" {
		description = strings.TrimSpace(ev.Error)
		if len(description) > 140 {
			description = description[:137] + "..."
		}
	}
	return client.SetCommitStatus(ctx, token, ev.GitHubRepo, ev.CommitSHA, githubapp.CommitStatus{
		State:       state,
		Context:     statusCtx,
		Description: description,
		TargetURL:   ev.URL,
	})
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
