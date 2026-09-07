package server

import (
	"encoding/json"
	"net/http"
)

type dashboardData struct {
	DBStatus string
	Addr     string
}

// handleDashboard renders the placeholder landing page. A live dashboard
// listing servers and projects replaces it in later phases.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	if err := s.db.PingContext(r.Context()); err != nil {
		status = "unavailable"
	}
	s.render(w, http.StatusOK, dashboardData{DBStatus: status, Addr: s.cfg.Server.Addr})
}

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// handleHealthz is the liveness/readiness probe: it reports whether the
// process is up and whether it can reach SQLite right now.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{Status: "ok", Database: "ok"}
	code := http.StatusOK
	if err := s.db.PingContext(r.Context()); err != nil {
		resp.Status = "degraded"
		resp.Database = "error"
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp)
}
