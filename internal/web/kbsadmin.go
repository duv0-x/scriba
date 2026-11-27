package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/onboarding"
)

// kbsView son los datos de la pantalla de bases (/kbs).
type kbsView struct {
	KBs []kbRow
	Msg string
}

type kbRow struct {
	Slug    string
	Default bool
	Notes   int
}

const msgSlugInvalido = "El slug solo admite minúsculas, números y guiones, empieza por letra o número y tiene hasta 40 caracteres."

// kbsRoutes registra la gestión de bases: es de instancia, sin prefijo /k/.
func (s *Server) kbsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /kbs", func(w http.ResponseWriter, r *http.Request) { s.renderKBs(w, r, http.StatusOK, "") })
	mux.HandleFunc("POST /kbs", s.kbCreate)
	mux.HandleFunc("POST /kbs/borrar", s.kbDelete)
}

func (s *Server) renderKBs(w http.ResponseWriter, r *http.Request, status int, msg string) {
	v := kbsView{Msg: msg}
	def := s.KBs.Default().Slug
	for _, slug := range s.KBs.List() {
		k, ok := s.KBs.Get(slug)
		if !ok {
			continue // se borró mientras se listaba
		}
		n, err := k.Ix.NoteCount()
		if err != nil {
			serverError(w, err)
			return
		}
		v.KBs = append(v.KBs, kbRow{Slug: slug, Default: slug == def, Notes: n})
	}
	sh, err := s.at(r).shellFor(r, "")
	if err != nil {
		serverError(w, err)
		return
	}
	sh.View = "kbs"
	page(w, r, status, layout(sh, "Bases · Scriba", kbsPage(v)))
}

// kbCreate crea una KB vacía con la plantilla o desde un .zip, con el mismo onboarding que /bienvenida.
func (s *Server) kbCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, onboarding.MaxZipBytes+1<<20)
	// El form es multipart (lleva el .zip); un POST urlencoded también vale para la plantilla.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			if mbe := (*http.MaxBytesError)(nil); errors.As(err, &mbe) {
				s.renderKBs(w, r, http.StatusRequestEntityTooLarge, "Supera 200 MB.")
				return
			}
			s.renderKBs(w, r, http.StatusBadRequest, "No se pudo leer el archivo subido.")
			return
		}
		defer r.MultipartForm.RemoveAll()
	}
	slug := r.PostFormValue("slug")
	if !kbs.ValidSlug(slug) {
		s.renderKBs(w, r, http.StatusBadRequest, msgSlugInvalido)
		return
	}

	var fill func(string) error
	dropped := false
	if r.PostFormValue("origen") == "zip" {
		f, hdr, err := r.FormFile("zip")
		if err != nil {
			s.renderKBs(w, r, http.StatusBadRequest, "Elige un archivo .zip.")
			return
		}
		defer f.Close()
		fill = func(dir string) error {
			res, err := onboarding.ImportZip(f, hdr.Size, dir)
			dropped = res.DroppedGit
			return err
		}
	}

	s.setupMu.Lock()
	_, err := s.KBs.Create(slug, fill)
	s.setupMu.Unlock()
	switch {
	case errors.Is(err, kbs.ErrInvalidSlug):
		s.renderKBs(w, r, http.StatusBadRequest, msgSlugInvalido)
		return
	case errors.Is(err, kbs.ErrExists):
		s.renderKBs(w, r, http.StatusConflict, "Ya existe una base con ese slug.")
		return
	case errors.Is(err, onboarding.ErrUnsafeZip):
		reason := strings.TrimPrefix(err.Error(), onboarding.ErrUnsafeZip.Error()+": ")
		s.renderKBs(w, r, http.StatusBadRequest, "El .zip no es seguro: "+reason)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	to := "/k/" + slug + "/"
	if dropped {
		to += "?aviso=historial"
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// kbDelete borra una KB si se tecleó su slug para confirmar.
func (s *Server) kbDelete(w http.ResponseWriter, r *http.Request) {
	slug := r.PostFormValue("slug")
	if r.PostFormValue("confirm") != slug || slug == "" {
		s.renderKBs(w, r, http.StatusBadRequest, "Escribe el slug de la base para confirmar.")
		return
	}
	s.setupMu.Lock()
	err := s.KBs.Delete(slug)
	s.setupMu.Unlock()
	switch {
	case errors.Is(err, kbs.ErrDefault):
		s.renderKBs(w, r, http.StatusConflict, "La base por defecto no se puede borrar.")
		return
	case errors.Is(err, kbs.ErrNotFound):
		s.renderKBs(w, r, http.StatusNotFound, "No existe esa base.")
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/kbs", http.StatusSeeOther)
}
