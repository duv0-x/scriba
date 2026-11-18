package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/state"
)

// agentsView son los datos de /agentes. Token solo viene relleno en la respuesta del POST que lo crea.
type agentsView struct {
	Tokens  []state.AgentToken
	Msg     string
	Created string // nombre del agente recién creado
	Token   string
	Command string // claude mcp add … listo para copiar
}

func (s *Server) agentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /agentes", func(w http.ResponseWriter, r *http.Request) {
		s.renderAgents(w, r, http.StatusOK, agentsView{})
	})
	mux.HandleFunc("POST /agentes", s.agentCreate)
	mux.HandleFunc("POST /agentes/revocar", s.agentRevoke)
}

func (s *Server) agentCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	tok, err := s.Agents.Create(name)
	switch {
	case errors.Is(err, agents.ErrInvalidName):
		s.renderAgents(w, r, http.StatusBadRequest, agentsView{Msg: "El nombre solo admite minúsculas, dígitos y guiones (hasta 40 caracteres) y debe empezar por letra o dígito."})
	case errors.Is(err, state.ErrNameTaken):
		s.renderAgents(w, r, http.StatusConflict, agentsView{Msg: "Ya hay un agente activo llamado «" + name + "». Revócalo o elige otro nombre."})
	case err != nil:
		serverError(w, err)
	default:
		w.Header().Set("Cache-Control", "no-store")
		s.renderAgents(w, r, http.StatusOK, agentsView{Created: name, Token: tok,
			Command: `claude mcp add --transport http scriba ` + s.publicURL(r) + `/mcp --header "Authorization: Bearer ` + tok + `"`})
	}
}

func (s *Server) agentRevoke(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Agents.Revoke(r.PostFormValue("name")); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/agentes", http.StatusSeeOther)
}

func (s *Server) renderAgents(w http.ResponseWriter, r *http.Request, status int, v agentsView) {
	toks, err := s.Agents.List()
	if err != nil {
		serverError(w, err)
		return
	}
	v.Tokens = toks
	sh, err := s.shellFor(r, "")
	if err != nil {
		serverError(w, err)
		return
	}
	sh.View = "agentes"
	page(w, r, status, layout(sh, "Agentes · Scriba", agentsPage(v)))
}

// publicURL es la URL base de la instancia: PublicURL o, si falta, el host de la petición.
func (s *Server) publicURL(r *http.Request) string {
	if s.PublicURL != "" {
		return strings.TrimRight(s.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ago es un relativo simple para "último uso".
func ago(t time.Time) string {
	if t.IsZero() {
		return "nunca"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "hace un momento"
	case d < time.Hour:
		return "hace " + strconv.Itoa(int(d.Minutes())) + " min"
	case d < 24*time.Hour:
		return "hace " + strconv.Itoa(int(d.Hours())) + " h"
	default:
		return "hace " + strconv.Itoa(int(d.Hours()/24)) + " d"
	}
}
