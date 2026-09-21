package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/detect"
)

// detectTimeout bounds one detection run: connect, shallow clone and scan.
const detectTimeout = 3 * time.Minute

// handleProjectDetect inspects a repository on the selected target and returns
// a proposed build+run plan as JSON, so the wizard can prefill its fields.
//
// The repository is shallow-cloned on the target (so private repositories use
// the target's own git credentials) and scanned by the pure detect package. The
// scratch clone is replaced on each run, so detection is idempotent.
func (s *Server) handleProjectDetect(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed form submission"})
		return
	}
	repoURL := strings.TrimSpace(r.FormValue("repo_url"))
	serverID := strings.TrimSpace(r.FormValue("server_id"))
	branch := strings.TrimSpace(r.FormValue("branch"))
	if repoURL == "" || serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo_url and server_id are required"})
		return
	}
	if s.newRunner == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "detection is not available"})
		return
	}
	srv, err := s.store.GetServer(r.Context(), serverID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "select a valid server"})
		return
	}
	// Detection reads Windows paths on the target; Docker/Linux targets keep the
	// supplied-Dockerfile flow.
	if srv.Type != config.ServerTypeWindowsService && srv.Type != config.ServerTypeIIS {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "detection is only supported on Windows service or IIS targets"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), detectTimeout)
	defer cancel()

	runner, err := s.newRunner(ctx, srv, s.secrets)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer runner.Close()

	dir := detectWorkDir(s.cfg, serverID)
	if err := deployment.CloneShallow(ctx, runner, dir, repoURL, branch, nil); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	tree, err := deployment.NewRepoTree(ctx, runner, dir)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	// An explicit language overrides auto-detection (the wizard's dropdown).
	plan := detect.Detect(tree)
	if lang := detect.Language(strings.TrimSpace(r.FormValue("language"))); lang != "" && lang != "auto" {
		plan = detect.DetectAs(tree, lang)
	}
	if plan == nil {
		writeJSON(w, http.StatusOK, map[string]any{"detected": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"detected": true, "plan": plan})
}

// detectWorkDir is the scratch directory for detection clones on a target.
func detectWorkDir(cfg config.Config, serverID string) string {
	base := strings.TrimRight(cfg.Deploy.IISWorkDir, `\/`)
	return base + `\detect\` + sanitizeSegment(serverID)
}

// sanitizeSegment keeps a path segment safe: letters, digits, dash, underscore
// and dot survive; everything else becomes a dash.
func sanitizeSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}
