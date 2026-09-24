package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/Devonlegend/winify/internal/bootstrap"
	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/models"
)

// setupStep is one bootstrap step's recorded state.
type setupStep struct {
	Name  string
	Done  bool
	Error string
}

type setupPageData struct {
	pageData
	Available   bool
	Steps       []setupStep
	LocalServer bool
	Error       string
	Notice      string
}

// handleSetupPage shows the recorded bootstrap progress and the local-target
// action. Applying bootstrap steps is done by the `winify bootstrap` command
// (elevated) or the Run button; creating the local target needs no elevation.
func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	data := setupPageData{pageData: s.page(r)}
	data.Active = "setup"
	data.Error = r.URL.Query().Get("error")
	data.Notice = r.URL.Query().Get("notice")

	if _, err := s.store.GetServer(r.Context(), "local"); err == nil {
		data.LocalServer = true
	} else if !errors.Is(err, models.ErrNotFound) {
		log.Printf("setup: get local server: %v", err)
	}

	if s.bootstrap == nil {
		s.render(w, http.StatusOK, "setup", data)
		return
	}
	statuses, err := s.bootstrap.Status(r.Context())
	if err != nil {
		log.Printf("setup: status: %v", err)
		data.Error = "Failed to read setup status."
		s.render(w, http.StatusInternalServerError, "setup", data)
		return
	}
	data.Available = true
	for _, st := range statuses {
		data.Steps = append(data.Steps, setupStep{Name: st.Name, Done: st.Done, Error: st.Error})
	}
	s.render(w, http.StatusOK, "setup", data)
}

// handleSetupRun applies bootstrap now when already elevated, or relaunches it
// elevated (UAC) when running interactively without rights.
func (s *Server) handleSetupRun(w http.ResponseWriter, r *http.Request) {
	if s.bootstrap == nil {
		http.Redirect(w, r, "/setup?error="+urlQuery("Bootstrap is not enabled"), http.StatusSeeOther)
		return
	}
	elevatedCheck := s.bootstrapElevated
	if elevatedCheck == nil {
		elevatedCheck = func(ctx context.Context) (bool, error) {
			return bootstrap.IsElevated(ctx, deployment.NewLocalRunner())
		}
	}
	elevated, err := elevatedCheck(r.Context())
	if err != nil {
		http.Redirect(w, r, "/setup?error="+urlQuery("Cannot check elevation: "+err.Error()), http.StatusSeeOther)
		return
	}
	if elevated {
		if s.bootstrapRun == nil {
			http.Redirect(w, r, "/setup?error="+urlQuery("Bootstrap runner is unavailable"), http.StatusSeeOther)
			return
		}
		go func() {
			if err := s.bootstrapRun(context.Background()); err != nil {
				log.Printf("setup: bootstrap: %v", err)
			}
		}()
		http.Redirect(w, r, "/setup?notice="+urlQuery("Bootstrap started; refresh to see progress"), http.StatusSeeOther)
		return
	}
	if s.bootstrapElevate == nil {
		http.Redirect(w, r, "/setup?error="+urlQuery("Run `winify bootstrap` from an elevated shell"), http.StatusSeeOther)
		return
	}
	if err := s.bootstrapElevate(); err != nil {
		http.Redirect(w, r, "/setup?error="+urlQuery("Elevation failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/setup?notice="+urlQuery("Approve the UAC prompt to run bootstrap"), http.StatusSeeOther)
}

// handleSetupLocalTarget creates the local winsvc server for this machine. It
// runs PowerShell in-process, so no WinRM credential is needed.
func (s *Server) handleSetupLocalTarget(w http.ResponseWriter, r *http.Request) {
	root := s.cfg.Bootstrap.InstallDir
	if root == "" {
		root = bootstrap.DefaultRoot()
	}
	name := "This machine"
	if h, err := os.Hostname(); err == nil && h != "" {
		name = h
	}
	paths := bootstrap.DefaultPaths(root)
	srv := config.Server{
		ID:        "local",
		Name:      name,
		Type:      config.ServerTypeWindowsService,
		Local:     true,
		NSSMPath:  paths.NSSM,
		CaddyPath: paths.Caddy,
		Host:      "127.0.0.1",
		PublicIP:  strings.TrimSpace(r.FormValue("public_ip")),
	}
	if err := s.store.UpsertServer(r.Context(), srv); err != nil {
		log.Printf("setup: local target server: %v", err)
		http.Redirect(w, r, "/setup?error="+urlQuery("Failed to create the local target."), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/setup?notice="+urlQuery("Local target created."), http.StatusSeeOther)
}
