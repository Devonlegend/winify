package assistant

import (
	"testing"

	"github.com/Devonlegend/winify/internal/config"
)

func TestDetectIntent(t *testing.T) {
	projects := []config.Project{
		{ID: "proj-001", Name: "Storefront"},
		{ID: "proj-002", Name: "Legacy Portal"},
	}
	servers := []config.Server{
		{ID: "server-002", Name: "Docker Host"},
		{ID: "server-001", Name: "Production IIS Server"},
	}

	cases := []struct {
		question string
		want     Intent
		entity   string // expected entity id, "" if none
	}{
		// Platform usage -> docs, even though it mentions "roll back"/"deploy".
		{"how do I roll back a deployment?", IntentDocs, ""},
		{"how do I add a new server?", IntentDocs, ""},
		{"why is my IIS deploy failing?", IntentDocs, ""},
		{"what is a webhook secret?", IntentDocs, ""},
		// Live deploy status -> deploy, bound to the named project.
		{"did the last deploy to proj-001 succeed?", IntentDeploy, "proj-001"},
		{"did the deploy to server-001 succeed?", IntentDeploy, "server-001"},
		{"show me the latest deployment", IntentDeploy, ""},
		// Live monitoring -> server.
		{"what's server-002's CPU right now", IntentServer, "server-002"},
		{"what is server-001's memory usage", IntentServer, "server-001"},
		{"what is the status of my servers", IntentServer, ""},
		{"is anything down", IntentServer, ""},
	}

	for _, tc := range cases {
		t.Run(tc.question, func(t *testing.T) {
			intent, entity := DetectIntent(tc.question, projects, servers)
			if intent != tc.want {
				t.Errorf("intent = %q, want %q", intent, tc.want)
			}
			if entity.ID != tc.entity {
				t.Errorf("entity = %q, want %q", entity.ID, tc.entity)
			}
		})
	}
}
