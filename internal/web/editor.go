package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// editorRoutes registra solo la redirección de la ruta de edición vieja.
// El editor dividido se retiró (ADR-014, scriba#55) y la ruta vieja responde 301
// al lector de la misma nota.
func (s *Server) editorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /k/{kb}/editar/{path...}", s.kb((*kbServer).editRedirect))
}

func (s *kbServer) editRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, string(noteHref(r.Context(), r.PathValue("path"))), http.StatusMovedPermanently)
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

// hash es el SHA-256 hex de s: el mismo que usa writer.WriteIfUnchanged.
func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
