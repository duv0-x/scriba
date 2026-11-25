package web

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"

	"github.com/duv0-x/scriba/internal/kindle"
	"github.com/duv0-x/scriba/internal/render"
	"github.com/duv0-x/scriba/internal/secrets"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/vault"
)

// connKindle reutiliza la tabla connectors: Account = correo de la Kindle, Repo = remitente,
// Secret = API key de Resend sellada (ADR-006).
const connKindle = "kindle"

// kindleView son los datos de la tarjeta Kindle; nunca lleva la API key.
type kindleView struct {
	To, From string
	Active   bool
}

// welcomeEPUB es el cuerpo del envío de prueba.
const welcomeEPUB template.HTML = `<p>Si lees esto en tu Kindle, el conector de Scriba funciona: Resend aceptó la API key y Amazon aprobó el remitente.</p><p>Desde ahora puedes mandar cualquier nota con el botón «Enviar a Kindle» del lector.</p>`

// kindleConnector devuelve el conector Kindle y si está activo (los tres datos presentes), sin descifrar la key.
func (s *Server) kindleConnector() (state.Connector, bool, error) {
	if s.State == nil {
		return state.Connector{}, false, nil
	}
	c, ok, err := s.State.Connector(connKindle)
	return c, ok && c.Account != "" && c.Repo != "" && len(c.Secret) > 0, err
}

// kindleConfig descifra la key solo en memoria; no debe salir del handler que envía.
func (s *Server) kindleConfig() (kindle.Config, bool, error) {
	c, ok, err := s.kindleConnector()
	if err != nil || !ok {
		return kindle.Config{}, false, err
	}
	key, err := secrets.Open(s.Key, c.Secret)
	if err != nil {
		return kindle.Config{}, false, fmt.Errorf("descifrar conector kindle: %w", err)
	}
	return kindle.Config{To: c.Account, From: c.Repo, APIKey: string(key), Endpoint: s.KindleEndpoint}, true, nil
}

// validKindle acepta solo direcciones de Send to Kindle (@kindle.com o @free.kindle.com).
func validKindle(to string) bool {
	a, err := mail.ParseAddress(to)
	if err != nil || a.Name != "" || a.Address != to {
		return false
	}
	return strings.HasSuffix(to, "@kindle.com") || strings.HasSuffix(to, "@free.kindle.com")
}

func (s *Server) kindleSave(w http.ResponseWriter, r *http.Request) {
	to := strings.ToLower(strings.TrimSpace(r.PostFormValue("to")))
	from := strings.TrimSpace(r.PostFormValue("from"))
	key := strings.TrimSpace(r.PostFormValue("key"))
	if !validKindle(to) {
		s.renderConnectors(w, r, http.StatusBadRequest, "El correo de la Kindle debe terminar en @kindle.com o @free.kindle.com.")
		return
	}
	if _, err := mail.ParseAddress(from); err != nil {
		s.renderConnectors(w, r, http.StatusBadRequest, "El remitente debe tener la forma Nombre <correo@dominio>.")
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	old, active, err := s.kindleConnector()
	if err != nil {
		serverError(w, err)
		return
	}
	sealed := old.Secret // key vacía = se conserva la guardada
	switch {
	case key != "":
		if sealed, err = secrets.Seal(s.Key, []byte(key)); err != nil {
			serverError(w, err)
			return
		}
	case !active:
		s.renderConnectors(w, r, http.StatusBadRequest, "Falta la API key de Resend.")
		return
	}
	if err := s.State.SaveConnector(state.Connector{Name: connKindle, Account: to, Repo: from, Secret: sealed}); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/conectores", http.StatusSeeOther)
}

func (s *Server) kindleTest(w http.ResponseWriter, r *http.Request) {
	s.kindleSend(w, r, kindle.Note{Title: "Bienvenida a Scriba", Path: "Conectores › Kindle", Body: welcomeEPUB})
}

func (s *Server) kindleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if err := s.State.DeleteConnector(connKindle); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/conectores", http.StatusSeeOther)
}

// kindleNote manda una nota a la Kindle: la ruta solo acepta POST y solo notas que existan.
func (s *Server) kindleNote(w http.ResponseWriter, r *http.Request) {
	n, err := s.readNote(r.PathValue("path"))
	switch {
	case errors.Is(err, vault.ErrOutside):
		http.Error(w, "ruta fuera del vault", http.StatusBadRequest)
		return
	case errors.Is(err, fs.ErrNotExist):
		http.Error(w, "esta nota no existe", http.StatusNotFound)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	// Sin resolver: kindle convierte en texto plano todo enlace que no sea http(s) o mailto.
	body, _, err := render.HTML(dropLeadingH1(n), resolver{})
	if err != nil {
		serverError(w, err)
		return
	}
	s.kindleSend(w, r, kindle.Note{Title: n.Title, Date: n.Date, Path: n.Path, Body: body})
}

// kindleSend responde siempre 200 con el estado que reemplaza al botón vía htmx.
func (s *Server) kindleSend(w http.ResponseWriter, r *http.Request, n kindle.Note) {
	cfg, ok, err := s.kindleConfig()
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok {
		page(w, r, http.StatusOK, kindleResult("El conector Kindle no está configurado."))
		return
	}
	if err := kindle.Send(r.Context(), n, cfg); err != nil {
		slog.Warn("web: enviar a kindle", "path", n.Path, "err", err)
		page(w, r, http.StatusOK, kindleResult(err.Error()))
		return
	}
	page(w, r, http.StatusOK, kindleResult(""))
}

// kindleAction es la URL del POST de envío de la nota p.
func kindleAction(p string) string {
	return "/kindle/" + strings.TrimPrefix(string(noteHref(p)), "/n/")
}
