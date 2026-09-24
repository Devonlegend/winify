package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Devonlegend/winify/internal/bootstrap"
	"github.com/Devonlegend/winify/internal/config"
)

// WindowsOnboardParams are the operator-supplied values for onboarding a
// remote Windows server over WinRM. Password is write-only: it is handed to
// the credential store by the implementation and never rendered or logged.
type WindowsOnboardParams struct {
	Endpoint       string
	User           string
	Password       string
	ID             string
	Name           string
	Insecure       bool
	ProvisionCaddy bool
}

// WindowsOnboardFunc provisions a remote Windows server and registers it. It
// returns the registered server ID plus the bootstrap step results so the
// handler can surface the failing step.
type WindowsOnboardFunc func(ctx context.Context, params WindowsOnboardParams) (string, []bootstrap.Result, error)

// onboardTimeout bounds a synchronous onboarding run. Provisioning Caddy can
// download and upload a release, so this is deliberately generous.
const onboardTimeout = 15 * time.Minute

// handleServerOnboard is the UI flow for "Add Windows server": normalize the
// endpoint, then let the injected onboarder dial WinRM, provision the target,
// store the credential and register the server.
func (s *Server) handleServerOnboard(w http.ResponseWriter, r *http.Request) {
	if s.onboardWindows == nil {
		s.renderServers(w, r, http.StatusNotImplemented, nil, "Windows onboarding is not configured.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderServers(w, r, http.StatusBadRequest, nil, "Malformed form submission.")
		return
	}
	params := WindowsOnboardParams{
		Endpoint:       strings.TrimSpace(r.FormValue("endpoint")),
		User:           strings.TrimSpace(r.FormValue("user")),
		Password:       r.FormValue("password"),
		ID:             strings.TrimSpace(r.FormValue("id")),
		Name:           strings.TrimSpace(r.FormValue("name")),
		Insecure:       r.FormValue("insecure") != "",
		ProvisionCaddy: r.FormValue("provision_caddy") != "",
	}
	if params.User == "" || params.Password == "" {
		s.renderServers(w, r, http.StatusBadRequest, nil, "WinRM user and password are required.")
		return
	}
	endpoint, err := config.NormalizeWinRMEndpoint(params.Endpoint, params.Insecure)
	if err != nil {
		s.renderServers(w, r, http.StatusBadRequest, nil, err.Error())
		return
	}
	params.Endpoint = endpoint
	if params.ID != "" {
		if err := config.ValidateResourceID("id", params.ID); err != nil {
			s.renderServers(w, r, http.StatusBadRequest, nil, err.Error())
			return
		}
	}

	// Onboarding is a privileged, network-heavy operation; serialize it so two
	// operators cannot race provisioning the same or different hosts.
	if !s.onboardMu.TryLock() {
		s.renderServers(w, r, http.StatusConflict, nil, "Another onboarding run is already in progress.")
		return
	}
	defer s.onboardMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), onboardTimeout)
	defer cancel()
	serverID, results, err := s.onboardWindows(ctx, params)
	if err != nil {
		failedStep := ""
		for _, step := range results {
			if step.Error != "" {
				failedStep = step.Name
			}
		}
		msg := "Onboarding failed."
		if failedStep != "" {
			msg = "Onboarding failed at step " + failedStep + ": " + err.Error()
		}
		s.renderServers(w, r, http.StatusBadGateway, nil, msg)
		return
	}
	http.Redirect(w, r, "/servers?notice="+urlQuery("Onboarded "+serverID), http.StatusSeeOther)
}
