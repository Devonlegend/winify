package assistant

import (
	"strings"

	"github.com/Devonlegend/winify/internal/config"
)

// Intent is the routing decision for a question: answer from docs, from live
// deploy history, or from live monitoring data.
type Intent string

const (
	IntentDocs   Intent = "docs"
	IntentDeploy Intent = "deploy"
	IntentServer Intent = "server"
)

// Entity is a project or server named in the question.
type Entity struct {
	Kind string // "project" | "server" | ""
	ID   string
	Name string
}

var (
	// Strong "this is a how-to" phrasing routes to docs unless a specific
	// project/server is named alongside a live-data keyword.
	usagePhrases = []string{
		"how do i", "how do we", "how to", "how can i", "what is", "what are",
		"why", "explain", "guide", "documentation", "troubleshoot", "faq",
	}
	deployKeywords = []string{
		"deploy", "deployment", "rollback", "roll back", "rolled back",
		"build", "release", "pipeline", "webhook", "commit",
	}
	monitorKeywords = []string{
		"cpu", "memory", "mem", "ram", "disk", "uptime", "load", "status",
		"down", "unreachable", "health", "metrics", "monitoring",
	}
)

// DetectIntent classifies a question and finds the project/server it refers to.
//
// The rule order matters: a named entity plus a live keyword wins, so
// "what is server-002's CPU" is live; otherwise how-to phrasing routes to docs,
// so "how do I roll back a deployment?" is a docs question even though it
// contains "deploy".
func DetectIntent(question string, projects []config.Project, servers []config.Server) (Intent, Entity) {
	q := strings.ToLower(question)
	entity := matchEntity(q, projects, servers)
	hasUsage := containsAny(q, usagePhrases)
	hasDeploy := containsAny(q, deployKeywords)
	hasMonitor := containsAny(q, monitorKeywords)

	switch {
	case entity.Kind != "" && (hasDeploy || hasMonitor):
		if hasMonitor && !hasDeploy {
			return IntentServer, entity
		}
		if hasDeploy && !hasMonitor {
			return IntentDeploy, entity
		}
		if entity.Kind == "server" {
			return IntentServer, entity
		}
		return IntentDeploy, entity
	case hasMonitor && containsAny(q, []string{"server", "servers", "monitoring", "metrics", "status", "all hosts"}):
		return IntentServer, entity
	case hasUsage:
		return IntentDocs, entity
	case hasDeploy:
		return IntentDeploy, entity
	case hasMonitor:
		return IntentServer, entity
	default:
		return IntentDocs, entity
	}
}

// matchEntity finds the longest project or server ID/name appearing in the
// question, so "server-001" wins over a shorter "server-00".
func matchEntity(q string, projects []config.Project, servers []config.Server) Entity {
	best := Entity{}
	bestLen := 0
	consider := func(kind, id, name string) {
		for _, candidate := range []string{id, name} {
			c := strings.ToLower(candidate)
			if c != "" && strings.Contains(q, c) && len(c) > bestLen {
				best = Entity{Kind: kind, ID: id, Name: name}
				bestLen = len(c)
			}
		}
	}
	for _, p := range projects {
		consider("project", p.ID, p.Name)
	}
	for _, s := range servers {
		consider("server", s.ID, s.Name)
	}
	return best
}

func containsAny(q string, terms []string) bool {
	for _, t := range terms {
		if strings.Contains(q, t) {
			return true
		}
	}
	return false
}
