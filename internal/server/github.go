package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
)

// ConnectGitHubFunc auto-registers a project's push webhook through the
// configured GitHub App: it stores a fresh webhook secret, creates or updates
// the repository hook and records the repo on the project.
type ConnectGitHubFunc func(ctx context.Context, project config.Project, ownerRepo, baseURL string) error

var ownerRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// handleGitHubConnect links a project to a GitHub repository and registers the
// push webhook through the configured GitHub App.
func (s *Server) handleGitHubConnect(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	if s.connectGitHub == nil {
		http.Redirect(w, r, "/projects/"+projectID+"?tab=overview&error="+urlQuery("GitHub App integration is not configured"), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/projects/"+projectID+"?tab=overview&error="+urlQuery("Malformed form submission"), http.StatusSeeOther)
		return
	}
	ownerRepo := strings.TrimSpace(r.FormValue("repo"))
	if !ownerRepoPattern.MatchString(ownerRepo) {
		http.Redirect(w, r, "/projects/"+projectID+"?tab=overview&error="+urlQuery("Repository must be owner/repo"), http.StatusSeeOther)
		return
	}
	project, err := s.store.GetProject(r.Context(), projectID)
	if errors.Is(err, models.ErrNotFound) {
		http.Error(w, "unknown project", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	baseURL := strings.TrimRight(strings.TrimSpace(s.cfg.Server.PublicURL), "/")
	if baseURL == "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		baseURL = scheme + "://" + r.Host
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := s.connectGitHub(ctx, project, ownerRepo, baseURL); err != nil {
		log.Printf("github connect %s: %v", projectID, err)
		http.Redirect(w, r, "/projects/"+projectID+"?tab=overview&error="+urlQuery("GitHub webhook setup failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/projects/"+projectID+"?tab=overview&notice="+urlQuery("GitHub webhook connected for "+ownerRepo), http.StatusSeeOther)
}
