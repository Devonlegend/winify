// Package notify fans deployment lifecycle events out to external sinks:
// a generic JSON webhook, a Slack incoming webhook and GitHub commit statuses.
// Notifications are observability, never part of the deployment's success
// path: implementations must be fast, bounded and non-fatal.
package notify

import (
	"context"
	"time"
)

// DeployEvent describes one deployment lifecycle transition.
type DeployEvent struct {
	DeploymentID int64     `json:"deployment_id"`
	ProjectID    string    `json:"project_id"`
	ProjectName  string    `json:"project_name"`
	ServerID     string    `json:"server_id"`
	Status       string    `json:"status"` // pending | success | failed
	CommitSHA    string    `json:"commit_sha,omitempty"`
	Ref          string    `json:"ref,omitempty"`
	Error        string    `json:"error,omitempty"`
	URL          string    `json:"url,omitempty"` // deployment detail page
	GitHubRepo   string    `json:"-"`             // internal routing, never serialized
	At           time.Time `json:"at"`
}

// Notifier delivers one event. Implementations return nil on success and an
// error otherwise; the deployer logs and continues either way.
type Notifier interface {
	NotifyDeploy(ctx context.Context, ev DeployEvent) error
}

// Multi fans out to several notifiers, collecting the first error.
type Multi []Notifier

func (m Multi) NotifyDeploy(ctx context.Context, ev DeployEvent) error {
	var first error
	for _, n := range m {
		if n == nil {
			continue
		}
		if err := n.NotifyDeploy(ctx, ev); err != nil && first == nil {
			first = err
		}
	}
	return first
}
