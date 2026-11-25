package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/oauth"
)

// consentView son los datos de /oauth/authorize; Req vacío = petición que no se puede aprobar.
type consentView struct {
	Req              oauth.AuthRequest
	Host, Agent, Msg string
}

func (s *Server) oauthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+oauth.AuthorizePath, s.authorize)
	mux.HandleFunc("POST "+oauth.AuthorizePath, s.authorize)
}

// authorize muestra el consentimiento de un conector (GET) y aplica la decisión (POST). La
// identidad del dueño la pone el proxy (Cloudflare Access); sameOrigin evita el CSRF.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("Referrer-Policy", "same-origin") // no-referrer dejaría Origin: null en el POST
	params := r.URL.Query()
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulario inválido", http.StatusBadRequest)
			return
		}
		params = r.PostForm
	}
	req, redirect, err := s.OAuth.Parse(params, oauth.BaseURL(s.PublicURL, r))
	if err != nil {
		s.renderConsent(w, r, http.StatusBadRequest, consentView{Msg: "No se puede autorizar: " + err.Error() + "."})
		return
	}
	if redirect != "" {
		http.Redirect(w, r, redirect, http.StatusSeeOther)
		return
	}
	v := consentView{Req: req, Host: redirectHost(req.RedirectURI), Agent: "claude"}
	if r.Method != http.MethodPost {
		s.renderConsent(w, r, http.StatusOK, v)
		return
	}
	if params.Get("decision") != "approve" {
		http.Redirect(w, r, s.OAuth.Deny(req), http.StatusSeeOther)
		return
	}
	v.Agent = strings.TrimSpace(params.Get("agent"))
	if !agents.ValidName(v.Agent) {
		v.Msg = "El nombre solo admite minúsculas, dígitos y guiones (hasta 40 caracteres) y debe empezar por letra o dígito."
		s.renderConsent(w, r, http.StatusBadRequest, v)
		return
	}
	static, err := s.Agents.HasStatic(v.Agent)
	if err != nil {
		serverError(w, err)
		return
	}
	if static {
		v.Msg = "Ya hay un token activo llamado «" + v.Agent + "». Elige otro nombre o revócalo en Agentes."
		s.renderConsent(w, r, http.StatusConflict, v)
		return
	}
	http.Redirect(w, r, s.OAuth.Approve(req, v.Agent), http.StatusSeeOther)
}

func (s *Server) renderConsent(w http.ResponseWriter, r *http.Request, status int, v consentView) {
	sh, err := s.shellFor(r, "")
	if err != nil {
		serverError(w, err)
		return
	}
	sh.View = "agentes"
	page(w, r, status, layout(sh, "Autorizar conector · Scriba", consentPage(v)))
}

func redirectHost(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	return u.Host
}
