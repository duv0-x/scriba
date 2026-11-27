package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/a-h/templ"

	"github.com/duv0-x/scriba/internal/render"
	"github.com/duv0-x/scriba/internal/vault"
	"github.com/duv0-x/scriba/internal/writer"
)

// editorAgent es el autor de las escrituras hechas desde el editor web.
const editorAgent = "web"

// maxForm acota el cuerpo de los POST del editor: el contenido (1 MB) codificado en el
// formulario puede ocupar hasta 3 veces más, más base y mensaje.
const maxForm = 4 * writer.MaxBytes

// maxMessage es el largo máximo (en caracteres) del mensaje de commit del editor.
const maxMessage = 200

// editorRoutes registra el editor web; la KB puede no tener Writer y se comprueba por petición.
func (s *Server) editorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /k/{kb}/editar/{path...}", s.kb((*kbServer).editForm))
	mux.HandleFunc("POST /k/{kb}/editar/{path...}", s.kb((*kbServer).editSave))
	mux.HandleFunc("POST /k/{kb}/vista-previa", s.kb((*kbServer).preview))
}

// editorView son los datos de la página del editor.
type editorView struct {
	Path, Content, Base, Message string
	Preview                      template.HTML
	Agent                        string // agente que escribió la nota hace poco ("" = nadie)
	Alert                        string
	Current                      string // contenido actual del archivo cuando hay conflicto
	Force                        string // hash de Current: "Guardar de todos modos" lo reenvía como force
	Dirty                        bool   // el textarea trae cambios sin guardar (tras un conflicto o error)
}

func (s *kbServer) editForm(w http.ResponseWriter, r *http.Request) {
	if s.W == nil {
		http.NotFound(w, r)
		return
	}
	p := r.PathValue("path")
	raw, ok := s.loadRaw(w, r, p)
	if !ok {
		return
	}
	s.renderEditor(w, r, http.StatusOK, editorView{Path: p, Content: raw, Base: hash(raw)})
}

func (s *kbServer) editSave(w http.ResponseWriter, r *http.Request) {
	if s.W == nil {
		http.NotFound(w, r)
		return
	}
	p := r.PathValue("path")
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulario inválido o demasiado grande", http.StatusRequestEntityTooLarge)
		return
	}
	raw, ok := s.loadRaw(w, r, p)
	if !ok {
		return
	}
	v := editorView{
		Path:    p,
		Content: normalize(r.PostFormValue("content")),
		Base:    r.PostFormValue("base"),
		Message: truncRunes(strings.TrimSpace(r.PostFormValue("message")), maxMessage),
		Dirty:   true,
	}
	// base sigue siendo el hash que el editor cargó: reenviar tras un conflicto vuelve a dar
	// conflicto. Solo el botón "Guardar de todos modos" manda force con el hash que vio el usuario.
	want := v.Base
	if f := r.PostFormValue("force"); f != "" {
		want = f
	}
	if hash(raw) != want {
		s.conflict(w, r, v, raw)
		return
	}
	if _, err := s.W.WriteIfUnchanged(editorAgent, p, v.Content, strings.ReplaceAll(v.Message, "\n", " "), want); err != nil {
		switch {
		case errors.Is(err, writer.ErrConflict):
			// Un agente escribió entre la comprobación de arriba y el guardado.
			now, ok := s.loadRaw(w, r, p)
			if !ok {
				return
			}
			s.conflict(w, r, v, now)
			return
		case errors.Is(err, writer.ErrTooLarge):
			v.Alert = "La nota supera el tamaño máximo de 1 MB."
		case errors.Is(err, writer.ErrInvalidPath):
			v.Alert = "Esta ruta no se puede editar desde la web."
		case strings.Contains(err.Error(), "UTF-8"):
			v.Alert = "El contenido no es texto UTF-8 válido."
		default:
			serverError(w, err)
			return
		}
		s.renderEditor(w, r, http.StatusBadRequest, v)
		return
	}
	http.Redirect(w, r, string(noteHref(r.Context(), p)), http.StatusSeeOther)
}

// conflict responde 409 conservando el base enviado y ofreciendo forzar contra current.
func (s *kbServer) conflict(w http.ResponseWriter, r *http.Request, v editorView, current string) {
	v.Current, v.Force = current, hash(current)
	v.Alert = "La nota cambió mientras la editabas. Revisa el contenido actual y guarda de todos modos o recarga y vuelve a aplicar tus cambios."
	s.renderEditor(w, r, http.StatusConflict, v)
}

// preview devuelve solo la prosa renderizada (sin layout) para el panel de vista previa.
func (s *kbServer) preview(w http.ResponseWriter, r *http.Request) {
	if s.W == nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulario inválido o demasiado grande", http.StatusRequestEntityTooLarge)
		return
	}
	content := normalize(r.PostFormValue("content"))
	if len(content) > writer.MaxBytes {
		http.Error(w, "la nota supera 1 MB", http.StatusRequestEntityTooLarge)
		return
	}
	body, err := s.renderBody(r.PostFormValue("path"), content)
	if err != nil {
		serverError(w, err)
		return
	}
	page(w, r, http.StatusOK, templ.Raw(string(body)))
}

// loadRaw valida la ruta como el lector y devuelve el Markdown crudo; si falla ya respondió.
func (s *kbServer) loadRaw(w http.ResponseWriter, r *http.Request, p string) (string, bool) {
	_, err := s.readNote(p)
	if err == nil {
		var abs string
		if abs, err = s.V.Abs(p); err == nil {
			var b []byte
			b, err = os.ReadFile(abs)
			if err == nil {
				return string(b), true
			}
		}
	}
	switch {
	case errors.Is(err, vault.ErrOutside):
		http.Error(w, "ruta fuera del vault", http.StatusBadRequest)
	case errors.Is(err, fs.ErrNotExist):
		sh, err := s.shellFor(r, "")
		if err != nil {
			serverError(w, err)
			return "", false
		}
		page(w, r, http.StatusNotFound, layout(sh, "Esta nota no existe · Scriba", notFoundPage(p)))
	default:
		serverError(w, err)
	}
	return "", false
}

func (s *kbServer) renderEditor(w http.ResponseWriter, r *http.Request, status int, v editorView) {
	body, err := s.renderBody(v.Path, v.Content)
	if err != nil {
		serverError(w, err)
		return
	}
	v.Preview = body
	if s.Presence != nil {
		v.Agent, _ = s.Presence.Writing(v.Path)
	}
	sh, err := s.shellFor(r, v.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	sh.View = "editor" // sin navegación parcial: conserva el aviso de cambios sin guardar (ADR-009)
	page(w, r, status, layout(sh, "Editando "+v.Path+" · Scriba", editorPage(v)))
}

// renderBody renderiza content como el lector; los wikilinks se resuelven con los enlaces ya
// indexados de la nota p (los nuevos aparecen como inexistentes hasta guardar).
func (s *kbServer) renderBody(p, content string) (template.HTML, error) {
	res := resolver{m: map[string]string{}, base: "/k/" + s.Slug}
	if p != "" {
		outs, err := s.Ix.Outlinks(p)
		if err != nil {
			return "", err
		}
		for _, l := range outs {
			if l.Exists {
				res.m[l.Raw] = l.Dst
			}
		}
	}
	body, _, err := render.HTML(stripFrontmatter(content), res)
	return body, err
}

// stripFrontmatter quita el bloque YAML inicial (--- ... ---) para la vista previa.
// shortcut: no valida el YAML como vault.splitFrontmatter; si es inválido se oculta igual.
func stripFrontmatter(c string) string {
	if !strings.HasPrefix(c, "---\n") {
		return c
	}
	rest := c[4:]
	if strings.HasPrefix(rest, "---\n") {
		return rest[4:]
	}
	if i := strings.Index(rest, "\n---\n"); i >= 0 {
		return rest[i+5:]
	}
	if strings.HasSuffix(rest, "\n---") {
		return ""
	}
	return c
}

func normalize(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func truncRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// hash es el SHA-256 hex de s: el mismo que usa writer.WriteIfUnchanged.
func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// editHref arma /k/{kb}/editar/<path> escapando cada segmento, como noteHref.
func editHref(ctx context.Context, p string) templ.SafeURL {
	return templ.SafeURL(kbBase(ctx) + "/editar" + strings.TrimPrefix(string(noteHref(ctx, p)), kbBase(ctx)+"/n"))
}
