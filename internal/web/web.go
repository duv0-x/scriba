// Package web sirve el lector de solo lectura: inicio, notas, búsqueda y estáticos.
package web

//go:generate go run github.com/a-h/templ/cmd/templ@v0.3.1070 generate
//go:generate ../../scripts/tailwind.sh styles/input.css static/app.css

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/a-h/templ"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/oauth"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/vault"
)

//go:embed static
var staticFS embed.FS

// Server agrupa las dependencias del lector web.
type Server struct {
	KBs Registry // las bases de la instancia (ADR-013); nunca nil
	// PR2: pueden ser nil en los tests del lector; sin State no hay bienvenida ni conectores.
	State     *state.Store
	Key       []byte // llave de secrets
	GitHub    GitHubAPI
	Git       GitOps
	ClientID  string
	Agents    *agents.Store // nil = sin pantalla de agentes
	PublicURL string        // base para el comando de Claude Code; "" = host de la petición
	OAuth     *oauth.Server // consentimiento de conectores (ADR-010); nil = sin OAuth

	KindleEndpoint string // API de Resend para el conector Kindle; "" = la real (los tests usan otra)

	mu  sync.Mutex
	dev *pendingDevice // device flow de GitHub en curso

	setupMu sync.Mutex // serializa crear base, subir zip, importar/enlazar repo, push y desconectar
}

// Registry es lo que la web usa de kbs.Registry.
type Registry interface {
	Get(slug string) (*kbs.KB, bool)
	Default() *kbs.KB
	List() []string
	Create(slug string, fill func(vaultDir string) error) (*kbs.KB, error)
	Delete(slug string) error
}

// kbServer es el Server de la instancia con la KB que el Registry resolvió para la petición:
// los handlers de notas usan s.V, s.Ix, s.W… de esa KB y de ninguna otra (ADR-013).
type kbServer struct {
	*Server
	*kbs.KB
}

type kbCtxKey struct{}

// kbCookie recuerda la última KB visitada, para la sidebar de las páginas de instancia (/conectores, /agentes…).
const kbCookie = "scriba_kb"

// withKB deja en el contexto la KB de la petición: la de /k/{kb}/ (404 si no existe) o, fuera de /k/,
// la última visitada (cookie) o la por defecto.
func (s *Server) withKB(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var k *kbs.KB
		if rest, ok := strings.CutPrefix(r.URL.Path, "/k/"); ok {
			slug, _, _ := strings.Cut(rest, "/")
			found := false
			if k, found = s.KBs.Get(slug); !found {
				http.NotFound(w, r)
				return
			}
			// Solo las páginas (no SSE, JSON ni fragmentos): si no, la pestaña de otra KB que reconecta
			// su EventSource cambiaría la "última visitada".
			page := strings.Contains(r.Header.Get("Accept"), "text/html")
			if c, err := r.Cookie(kbCookie); page && (err != nil || c.Value != slug) {
				http.SetCookie(w, &http.Cookie{Name: kbCookie, Value: slug, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
			}
		} else {
			if c, err := r.Cookie(kbCookie); err == nil {
				k, _ = s.KBs.Get(c.Value)
			}
			if k == nil {
				k = s.KBs.Default()
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), kbCtxKey{}, k)))
	})
}

// at es el kbServer de la KB de la petición (withKB ya la resolvió).
func (s *Server) at(r *http.Request) *kbServer {
	return &kbServer{Server: s, KB: r.Context().Value(kbCtxKey{}).(*kbs.KB)}
}

// kb adapta un handler de una KB a http.HandlerFunc.
func (s *Server) kb(h func(*kbServer, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h(s.at(r), w, r) }
}

// kbBase es el prefijo /k/{slug} de la KB de la petición; "" sin KB en el contexto.
func kbBase(ctx context.Context) string {
	if k, _ := ctx.Value(kbCtxKey{}).(*kbs.KB); k != nil {
		return "/k/" + k.Slug
	}
	return ""
}

// legacy redirige una ruta de antes de ADR-013 a la misma en la KB por defecto, con su query:
// 301 para GET/HEAD y 308 para el resto, que conserva el método y el cuerpo (un POST no se vuelve GET).
func (s *Server) legacy(w http.ResponseWriter, r *http.Request) {
	u := "/k/" + s.KBs.Default().Slug + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		u += "?" + r.URL.RawQuery
	}
	code := http.StatusMovedPermanently
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		code = http.StatusPermanentRedirect
	}
	http.Redirect(w, r, u, code)
}

// Handler devuelve las rutas del lector.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	files := http.StripPrefix("/static/", http.FileServerFS(noDirFS{static}))
	mux.HandleFunc("GET /static/{file...}", serveStatic(files))
	mux.HandleFunc("GET /k/{kb}/{$}", s.kb((*kbServer).dashboard))
	mux.HandleFunc("GET /k/{kb}/n/{path...}", s.kb((*kbServer).note))
	mux.HandleFunc("PUT /k/{kb}/n/{path...}", s.kb((*kbServer).autosave))
	mux.HandleFunc("GET /k/{kb}/buscar", s.kb((*kbServer).search))
	mux.HandleFunc("POST /k/{kb}/kindle/{path...}", s.kb((*kbServer).kindleNote))
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if err := s.KBs.Default().Ix.Ping(); err != nil {
			http.Error(w, "índice no disponible", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	s.liveRoutes(mux)
	s.graphRoutes(mux)
	s.editorRoutes(mux)
	for _, p := range []string{"/{$}", "/n/", "/buscar", "/editar/", "/grafo", "/api/grafo", "/events", "/kindle/"} {
		mux.HandleFunc(p, s.legacy)
	}
	if s.State == nil {
		return s.withKB(mux)
	}
	s.setupRoutes(mux)
	s.kbsRoutes(mux)
	if s.Agents != nil {
		s.agentRoutes(mux)
		if s.OAuth != nil {
			s.oauthRoutes(mux)
		}
	}
	return sameOrigin(s.gate(s.withKB(mux)))
}

func (s *kbServer) note(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	// El contenido crudo se lee antes que la nota: si cambia entre ambas lecturas, se marca leída
	// la versión anterior y la nota vuelve a salir sin leer (nunca al revés).
	var raw []byte
	if abs, err := s.V.Abs(p); err == nil {
		raw, _ = os.ReadFile(abs)
	}
	n, err := s.readNote(p)
	switch {
	case errors.Is(err, vault.ErrOutside):
		http.Error(w, "ruta fuera del vault", http.StatusBadRequest)
		return
	case errors.Is(err, fs.ErrNotExist):
		sh, err := s.shellFor(r, "")
		if err != nil {
			serverError(w, err)
			return
		}
		page(w, r, http.StatusNotFound, layout(sh, "Esta nota no existe · Scriba", notFoundPage(p)))
		return
	case err != nil:
		serverError(w, err)
		return
	}

	outs, err := s.Ix.Outlinks(n.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	res := resolver{m: map[string]string{}, base: "/k/" + s.Slug}
	for _, l := range outs {
		if l.Exists {
			res.m[l.Raw] = l.Dst
		}
	}
	// Datos del editor en línea (ADR-014); nil en las KB sin Writer.
	var edit *noteEdit
	if s.W != nil {
		edit = &noteEdit{Raw: string(raw), Base: hash(string(raw)), Links: res.m}
		if s.Presence != nil {
			edit.Agent, _ = s.Presence.Writing(n.Path)
		}
	}
	if r.URL.Query().Get("datos") == "1" {
		// Markdown crudo, base, enlaces y presencia, para que el editor en línea recargue la nota.
		if edit == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "esta base no se puede editar"})
			return
		}
		writeJSON(w, http.StatusOK, edit)
		return
	}
	body, headings, banner, err := s.readView(r, n, string(raw), res)
	if err != nil {
		serverError(w, err)
		return
	}
	if r.URL.Query().Get("fragmento") == "1" {
		// Solo la prosa, para la reproducción en vivo (live.js).
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(body))
		return
	}
	back, err := s.Ix.Backlinks(n.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	sh, err := s.shellFor(r, n.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	_, kindleOn, err := s.kindleConnector()
	if err != nil {
		serverError(w, err)
		return
	}
	page(w, r, http.StatusOK, layout(sh, n.Title+" · Scriba", readerPage(n, body, headings, back, banner, edit, kindleOn)))
}

// readNote solo sirve .md fuera de directorios ocultos: el vault es un repo git y .git/config no es una nota.
func (s *kbServer) readNote(p string) (*vault.Note, error) {
	if _, err := s.V.Abs(p); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(p, ".md") {
		return nil, fs.ErrNotExist
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") || strings.EqualFold(seg, "_templates") {
			return nil, fs.ErrNotExist
		}
	}
	return s.V.Read(p)
}

func (s *kbServer) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	hits, err := s.Ix.Search(q, 20)
	if err != nil {
		serverError(w, err)
		return
	}
	// Solo la búsqueda en vivo pide el parcial; Enter en el form llega boosteado y espera la página (ADR-009).
	if r.Header.Get("HX-Request") != "" && r.Header.Get("HX-Boosted") == "" {
		page(w, r, http.StatusOK, searchResults(q, hits))
		return
	}
	sh, err := s.shellFor(r, "")
	if err != nil {
		serverError(w, err)
		return
	}
	page(w, r, http.StatusOK, layout(sh, "Buscar · Scriba", searchPage(q, hits)))
}

// shell son los datos de la sidebar, comunes a todas las páginas.
type shell struct {
	Current string
	View    string // vista de la sidebar marcada fuera del lector ("conectores", "agentes", "bienvenida")
	Agents  bool   // hay pantalla de agentes
	Tree    []treeNode
	KB      string   // slug de la KB de la sidebar
	KBs     []string // todas, para el selector
}

// shellFor es shell con los puntos de "sin leer" y de agente escribiendo del usuario de la petición.
func (s *kbServer) shellFor(r *http.Request, current string) (shell, error) {
	sh, err := s.shell(current)
	if err == nil {
		s.markTree(r, sh.Tree)
	}
	return sh, err
}

func (s *kbServer) shell(current string) (shell, error) {
	refs, err := s.Ix.Refs()
	if err != nil {
		return shell{}, err
	}
	return shell{Current: current, Agents: s.Agents != nil, Tree: buildTree(refs, current), KB: s.Slug, KBs: s.KBs.List()}, nil
}

var singulars = map[string]string{
	"journal": "Journal", "tickets": "Ticket", "plans": "Plan", "decisions": "Decisión",
	"runbooks": "Runbook", "postmortems": "Postmortem", "references": "Referencia", "processes": "Proceso",
	"onboarding": "Onboarding", "domains": "Dominio", "services": "Servicio", "clients": "Cliente",
	"drafts": "Borrador", "learning": "Aprendizaje", "pocs": "PoC", "docs": "Documento", "note": "Nota",
}

func typeLabel(t string) string {
	if l, ok := singulars[t]; ok {
		return l
	}
	return "Nota"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// noteHref arma /k/{kb}/n/<path> escapando cada segmento.
func noteHref(ctx context.Context, p string) templ.SafeURL {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return templ.SafeURL(kbBase(ctx) + "/n/" + strings.Join(segs, "/"))
}

func readingMinutes(words int) int {
	return max(1, int(math.Round(float64(words)/220)))
}

// resolver traduce el destino crudo de un wikilink con los Outlinks ya resueltos por el índice;
// base es el prefijo /k/{kb} de los enlaces, para que no salten a otra KB.
type resolver struct {
	m    map[string]string
	base string
}

func (r resolver) Resolve(raw string) (string, bool) {
	dst, ok := r.m[strings.TrimSpace(raw)]
	return dst, ok
}

func (r resolver) LinkBase() string { return r.base }

func page(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		slog.Error("web: render", "err", err)
	}
}

func serverError(w http.ResponseWriter, err error) {
	slog.Error("web", "err", err)
	http.Error(w, "error interno", http.StatusInternalServerError)
}

// dropLeadingH1 quita el "# Título" inicial del cuerpo cuando repite el título que el lector ya
// muestra arriba: si coincide con n.Title, o si no hay title en el frontmatter (salió de ese h1).
func dropLeadingH1(n *vault.Note) string {
	rest := strings.TrimLeft(n.Body, " \t\r\n")
	if !strings.HasPrefix(rest, "# ") {
		return n.Body
	}
	h1, after, _ := strings.Cut(rest, "\n")
	if _, ok := n.Meta["title"]; ok && strings.TrimSpace(h1[2:]) != n.Title {
		return n.Body
	}
	return after
}

// noDirFS niega los directorios para que /static/ no los liste (404).
type noDirFS struct{ fs.FS }

func (f noDirFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	if st, err := file.Stat(); err != nil || st.IsDir() {
		file.Close()
		return nil, fs.ErrNotExist
	}
	return file, nil
}

// dirOf es la carpeta de una nota para mostrarla bajo su título ("" si está en la raíz).
func dirOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// user identifica a la persona que lee: el correo que inyecta Cloudflare Access o "default" en local.
func user(r *http.Request) string {
	if u := strings.TrimSpace(r.Header.Get("Cf-Access-Authenticated-User-Email")); u != "" {
		return u
	}
	return "default"
}
