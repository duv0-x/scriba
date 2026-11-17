// Package web sirve el lector de solo lectura: inicio, notas, búsqueda y estáticos.
package web

//go:generate go run github.com/a-h/templ/cmd/templ@v0.3.1070 generate
//go:generate ../../scripts/tailwind.sh styles/input.css static/app.css

import (
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"

	"github.com/a-h/templ"

	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/render"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/vault"
)

//go:embed static
var staticFS embed.FS

// Server agrupa las dependencias del lector web.
type Server struct {
	V  *vault.Vault
	Ix *index.Index
	// PR2: pueden ser nil en los tests del lector; sin State no hay bienvenida ni conectores.
	State      *state.Store
	Key        []byte // llave de secrets
	GitHub     GitHubAPI
	Git        GitOps
	ClientID   string
	OnImported func() // tras crear/importar el vault (reindexar); puede ser nil

	mu  sync.Mutex
	dev *pendingDevice // device flow de GitHub en curso

	setupMu sync.Mutex // serializa crear base, subir zip, importar/enlazar repo, push y desconectar
}

// Handler devuelve las rutas del lector.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	files := http.StripPrefix("/static/", http.FileServerFS(noDirFS{static}))
	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /n/{path...}", s.note)
	mux.HandleFunc("GET /buscar", s.search)
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if err := s.Ix.Ping(); err != nil {
			http.Error(w, "índice no disponible", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	if s.State == nil {
		return mux
	}
	s.setupRoutes(mux)
	return sameOrigin(s.gate(mux))
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shell("")
	if err != nil {
		serverError(w, err)
		return
	}
	recent, err := s.Ix.Recent(20)
	if err != nil {
		serverError(w, err)
		return
	}
	aviso := ""
	if r.URL.Query().Get("aviso") == "historial" {
		aviso = avisoHistorial
	}
	page(w, r, http.StatusOK, layout(sh, "Scriba", homePage(recent, aviso)))
}

func (s *Server) note(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	n, err := s.readNote(p)
	switch {
	case errors.Is(err, vault.ErrOutside):
		http.Error(w, "ruta fuera del vault", http.StatusBadRequest)
		return
	case errors.Is(err, fs.ErrNotExist):
		sh, err := s.shell("")
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
	res := resolver{}
	for _, l := range outs {
		if l.Exists {
			res[l.Raw] = l.Dst
		}
	}
	body, headings, err := render.HTML(dropLeadingH1(n), res)
	if err != nil {
		serverError(w, err)
		return
	}
	back, err := s.Ix.Backlinks(n.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	sh, err := s.shell(n.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	page(w, r, http.StatusOK, layout(sh, n.Title+" · Scriba", readerPage(n, body, headings, back)))
}

// readNote solo sirve .md fuera de directorios ocultos: el vault es un repo git y .git/config no es una nota.
func (s *Server) readNote(p string) (*vault.Note, error) {
	if _, err := s.V.Abs(p); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(p, ".md") {
		return nil, fs.ErrNotExist
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") || seg == "_templates" {
			return nil, fs.ErrNotExist
		}
	}
	return s.V.Read(p)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	hits, err := s.Ix.Search(q, 20)
	if err != nil {
		serverError(w, err)
		return
	}
	if r.Header.Get("HX-Request") != "" {
		page(w, r, http.StatusOK, searchResults(q, hits))
		return
	}
	sh, err := s.shell("")
	if err != nil {
		serverError(w, err)
		return
	}
	page(w, r, http.StatusOK, layout(sh, "Buscar · Scriba", searchPage(q, hits)))
}

// shell son los datos de la sidebar, comunes a todas las páginas.
type shell struct {
	Current string
	View    string // vista de la sidebar marcada fuera del lector ("conectores", "bienvenida")
	Tree    []treeNode
}

func (s *Server) shell(current string) (shell, error) {
	refs, err := s.Ix.Refs()
	if err != nil {
		return shell{}, err
	}
	return shell{Current: current, Tree: buildTree(refs, current)}, nil
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

// noteHref arma /n/<path> escapando cada segmento.
func noteHref(p string) templ.SafeURL {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return templ.SafeURL("/n/" + strings.Join(segs, "/"))
}

func readingMinutes(words int) int {
	return max(1, int(math.Round(float64(words)/220)))
}

// resolver traduce el destino crudo de un wikilink con los Outlinks ya resueltos por el índice.
type resolver map[string]string

func (r resolver) Resolve(raw string) (string, bool) {
	dst, ok := r[strings.TrimSpace(raw)]
	return dst, ok
}

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
