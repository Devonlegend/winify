package server

import (
	"log"
	"net/http"
	"strings"

	"github.com/Devonlegend/winify/internal/models"
)

// Shared variables are values a project's env can reference as
// {{project.KEY}} or {{environment.KEY}}, so the same secret or URL is not
// copied into every resource.
type variablesPageData struct {
	pageData
	Variables []models.SharedVariable
	Error     string
	Notice    string
}

func (s *Server) handleVariablesPage(w http.ResponseWriter, r *http.Request) {
	s.renderVariables(w, r, http.StatusOK, r.URL.Query().Get("error"))
}

func (s *Server) renderVariables(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	vars, err := s.store.ListSharedVariables(r.Context())
	if err != nil {
		log.Printf("variables: list: %v", err)
		http.Error(w, "failed to load variables", http.StatusInternalServerError)
		return
	}
	data := variablesPageData{
		pageData:  s.page(r),
		Variables: vars,
		Error:     errMsg,
		Notice:    r.URL.Query().Get("notice"),
	}
	data.Active = "variables"
	s.render(w, status, "variables", data)
}

func (s *Server) handleVariableSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderVariables(w, r, http.StatusBadRequest, "Malformed form submission.")
		return
	}
	v := models.SharedVariable{
		Scope:   strings.TrimSpace(r.FormValue("scope")),
		ScopeID: strings.TrimSpace(r.FormValue("scope_id")),
		Key:     strings.TrimSpace(r.FormValue("key")),
		Value:   r.FormValue("value"),
	}
	switch {
	case v.Scope != "project" && v.Scope != "environment":
		s.renderVariables(w, r, http.StatusBadRequest, "Scope must be project or environment.")
		return
	case v.ScopeID == "":
		s.renderVariables(w, r, http.StatusBadRequest, "Scope ID is required (project group, or group/environment).")
		return
	case v.Key == "":
		s.renderVariables(w, r, http.StatusBadRequest, "Key is required.")
		return
	}
	if err := s.store.UpsertSharedVariable(r.Context(), v); err != nil {
		log.Printf("variables: save: %v", err)
		s.renderVariables(w, r, http.StatusInternalServerError, "Failed to save variable.")
		return
	}
	http.Redirect(w, r, "/variables?notice="+urlQuery("Variable saved"), http.StatusSeeOther)
}

func (s *Server) handleVariableDelete(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.FormValue("scope"))
	scopeID := strings.TrimSpace(r.FormValue("scope_id"))
	key := strings.TrimSpace(r.FormValue("key"))
	if err := s.store.DeleteSharedVariable(r.Context(), scope, scopeID, key); err != nil {
		log.Printf("variables: delete: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/variables?notice="+urlQuery("Variable deleted"), http.StatusSeeOther)
}
