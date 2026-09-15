package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/models"
)

// handleDeployStart starts a deploy and returns its id as JSON, for the
// dashboard's live deploy drawer. The form-based route remains for no-JS use.
func (s *Server) handleDeployStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.store.GetProject(ctx, chi.URLParam(r, "projectID"))
	if err != nil {
		apiError(w, http.StatusNotFound, "unknown project")
		return
	}
	srv, err := s.store.GetServer(ctx, project.ServerID)
	if err != nil {
		apiError(w, http.StatusBadRequest, "project has no valid server")
		return
	}
	branch := project.Branch
	if branch == "" {
		branch = "main"
	}
	id, err := s.deployer.Trigger(ctx, project, srv, "manual", "", "refs/heads/"+branch)
	if err == deployment.ErrDeployInProgress {
		apiError(w, http.StatusConflict, "a deployment is already in progress")
		return
	}
	if err != nil {
		log.Printf("deploy start: %v", err)
		apiError(w, http.StatusInternalServerError, "failed to start deployment")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"deployment_id": id})
}

// handleDeploymentStream streams a deployment's log and status as
// Server-Sent Events until it reaches a terminal state.
func (s *Server) handleDeploymentStream(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid deployment id")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx := r.Context()
	sent := 0
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	for {
		dep, err := s.store.GetDeployment(ctx, id)
		if err != nil {
			writeSSE(w, flusher, "status", map[string]string{"status": "unknown", "error": "deployment not found"})
			return
		}
		if len(dep.Log) > sent {
			writeSSE(w, flusher, "log", map[string]string{"chunk": dep.Log[sent:]})
			sent = len(dep.Log)
		}
		if dep.Status == models.DeploySuccess || dep.Status == models.DeployFailed {
			writeSSE(w, flusher, "status", map[string]string{"status": dep.Status, "error": dep.Error})
			return
		}
		writeSSE(w, flusher, "status", map[string]string{"status": dep.Status})

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}
