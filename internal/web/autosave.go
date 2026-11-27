package web

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/duv0-x/scriba/internal/vault"
	"github.com/duv0-x/scriba/internal/writer"
)

// maxAutosave acota el body JSON del autosave: JSON puede escapar cada byte de control
// como \u00XX (6 bytes), más base y force.
const maxAutosave = 6*writer.MaxBytes + 4096

type autosaveReq struct {
	Content string `json:"content"`
	Base    string `json:"base"`
	Force   string `json:"force"`
}

// noteEdit es lo que el lector incrusta en una nota editable para el editor en línea (ADR-014).
type noteEdit struct {
	Raw   string            `json:"raw"`   // Markdown crudo
	Base  string            `json:"base"`  // hash(Raw), para el primer PUT
	Links map[string]string `json:"links"` // wikilink crudo → ruta resuelta (Outlinks)
	Agent string            `json:"agent"` // agente con presencia al abrir ("" = nadie)
}

// autosave guarda la nota desde el editor en línea (ADR-014): PUT /k/{kb}/n/{path}.
// ?flush=1 hace además el commit web pendiente de la ruta; con el body vacío, solo eso (204).
func (s *kbServer) autosave(w http.ResponseWriter, r *http.Request) {
	if s.W == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "esta base no se puede editar"})
		return
	}
	p := r.PathValue("path")
	flush := r.URL.Query().Get("flush") == "1"
	r.Body = http.MaxBytesReader(w, r.Body, maxAutosave)
	b, err := io.ReadAll(r.Body)
	var mb *http.MaxBytesError
	if errors.As(err, &mb) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "la nota supera 1 MB"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cuerpo ilegible"})
		return
	}
	if flush && len(b) == 0 {
		if err := s.W.Flush(p); err != nil {
			slog.Error("web: commit del flush", "path", p, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudo hacer el commit"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req autosaveReq
	if err := json.Unmarshal(b, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
		return
	}
	// Misma validación de ruta que el lector.
	if _, err := s.readNote(p); err != nil {
		switch {
		case errors.Is(err, vault.ErrOutside):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ruta fuera del vault"})
		case errors.Is(err, fs.ErrNotExist):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "esta nota no existe"})
		default:
			serverError(w, err)
		}
		return
	}
	if s.Presence != nil {
		if a, ok := s.Presence.Writing(p); ok {
			writeJSON(w, http.StatusLocked, map[string]string{"agent": a})
			return
		}
	}
	content := normalize(req.Content)
	want := req.Base
	if req.Force != "" {
		want = req.Force
	}
	res, err := s.W.Autosave(p, content, want)
	switch {
	case errors.Is(err, writer.ErrConflict):
		abs, aerr := s.V.Abs(p)
		if aerr != nil {
			serverError(w, aerr)
			return
		}
		cur, cerr := os.ReadFile(abs)
		if cerr != nil {
			serverError(w, cerr)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]string{"current": string(cur), "base": hash(string(cur))})
		return
	case errors.Is(err, writer.ErrTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "la nota supera 1 MB"})
	case errors.Is(err, writer.ErrInvalidPath):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "esta ruta no se puede editar desde la web"})
	case err != nil && strings.Contains(err.Error(), "UTF-8"):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "el contenido no es UTF-8 válido"})
	case err != nil:
		slog.Error("web: autosave", "path", p, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "error interno"})
	}
	if err != nil {
		return
	}
	if flush {
		// El archivo ya se guardó: un fallo del commit no es un error para el cliente.
		if err := s.W.Flush(p); err != nil {
			slog.Warn("web: commit del flush", "path", p, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"base": res.Hash})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
