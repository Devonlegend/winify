package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/Devonlegend/winify/internal/auth"
)

// pageData is embedded by every page so the layout can render the current user
// and highlight the active tab. User is empty on the login page, which hides
// the nav.
type pageData struct {
	User   string
	Active string
}

func (s *Server) page(r *http.Request) pageData {
	user, _ := auth.UserFromContext(r.Context())
	return pageData{User: user}
}

// ---- Auth ----

type loginData struct {
	User  string
	Error string
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "login", loginData{})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "login", loginData{Error: "Malformed form submission."})
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := s.auth.Authenticate(r.Context(), username, password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		// Deliberately vague: do not reveal whether the user exists.
		s.render(w, http.StatusUnauthorized, "login", loginData{Error: "Invalid username or password."})
		return
	}
	if err != nil {
		log.Printf("login: %v", err) // err has no credentials
		s.render(w, http.StatusInternalServerError, "login", loginData{Error: "Sign-in failed. Try again."})
		return
	}

	if err := s.auth.StartSession(r.Context(), w, user.ID); err != nil {
		log.Printf("start session: %v", err)
		s.render(w, http.StatusInternalServerError, "login", loginData{Error: "Sign-in failed. Try again."})
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.EndSession(r.Context(), w, r); err != nil {
		log.Printf("logout: %v", err)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---- Dashboard shell ----

type dashboardData struct {
	pageData
	DBStatus     string
	ServerCount  int
	ProjectCount int
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := dashboardData{pageData: s.page(r), DBStatus: "ok"}
	data.Active = "deployment"

	if err := s.store.DB().PingContext(r.Context()); err != nil {
		data.DBStatus = "unavailable"
	}
	if servers, err := s.store.ListServers(r.Context()); err == nil {
		data.ServerCount = len(servers)
	}
	if projects, err := s.store.ListProjects(r.Context()); err == nil {
		data.ProjectCount = len(projects)
	}
	s.render(w, http.StatusOK, "dashboard", data)
}

type sectionData struct {
	pageData
}

func (s *Server) handleDeployment(w http.ResponseWriter, r *http.Request) {
	data := sectionData{pageData: s.page(r)}
	data.Active = "deployment"
	s.render(w, http.StatusOK, "deployment", data)
}

func (s *Server) handleMonitoring(w http.ResponseWriter, r *http.Request) {
	data := sectionData{pageData: s.page(r)}
	data.Active = "monitoring"
	s.render(w, http.StatusOK, "monitoring", data)
}

func (s *Server) handleAssistant(w http.ResponseWriter, r *http.Request) {
	data := sectionData{pageData: s.page(r)}
	data.Active = "assistant"
	s.render(w, http.StatusOK, "assistant", data)
}

// ---- Health ----

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// handleHealthz is the liveness/readiness probe: it reports whether the
// process is up and whether it can reach SQLite right now. It is public and
// carries no secrets.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{Status: "ok", Database: "ok"}
	code := http.StatusOK
	if err := s.store.DB().PingContext(r.Context()); err != nil {
		resp.Status = "degraded"
		resp.Database = "error"
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp)
}
