package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/duv0-x/scriba/internal/render"
	"github.com/duv0-x/scriba/internal/vault"
)

const heartbeat = 25 * time.Second

// liveRoutes registra el tiempo real (PR3b, T2).
func (s *Server) liveRoutes(mux *http.ServeMux) {
	if s.Bus != nil {
		mux.HandleFunc("GET /events", s.events)
	}
}

type liveEvent struct {
	Path  string `json:"path"`
	Agent string `json:"agent"`
	Kind  string `json:"kind"`
}

type livePresence struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
}

// events es el stream SSE: "changed" por cada cambio y "presence" al conectar y tras cada evento.
// Nunca envía contenido de notas.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	ch, cancel := s.Bus.Subscribe(32)
	defer cancel()
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // el stream dura más que cualquier WriteTimeout
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(name string, v any) bool {
		b, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	presence := func() bool {
		out := []livePresence{}
		if s.Presence != nil {
			for _, a := range s.Presence.Active() {
				out = append(out, livePresence{Agent: a.Agent, Path: a.Path})
			}
		}
		return send("presence", out)
	}
	if !presence() {
		return
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case e, ok := <-ch:
			if !ok || !send("changed", liveEvent{Path: e.Path, Agent: e.Agent, Kind: string(e.Kind)}) || !presence() {
				return
			}
		}
	}
}

const (
	bannerChanged = "Cambios desde tu última lectura, marcados al margen"
	bannerNew     = "Nota nueva desde tu última visita"
)

// readView renderiza el cuerpo de la nota para user y la marca como leída: si cambió desde su
// última lectura, resalta con .chg los bloques nuevos. Sin State, render normal sin banner.
func (s *Server) readView(r *http.Request, n *vault.Note, raw string, res resolver) (template.HTML, []render.Heading, string, error) {
	md := dropLeadingH1(n)
	if s.State == nil {
		body, hs, err := render.HTML(md, res)
		return body, hs, "", err
	}
	u, now, sum := user(r), time.Now(), hash(raw)
	var old map[string]bool
	banner := ""
	last, ok, err := s.State.LastRead(u, n.Path)
	if err != nil {
		slog.Warn("web: última lectura", "err", err)
	}
	switch {
	case err != nil:
	case ok && last.Hash != sum:
		banner, old = bannerChanged, map[string]bool{}
		for _, b := range mdBlocks(snapshotBody(n, last.Content)) {
			old[b] = true
		}
	case !ok:
		if base, err := s.State.Baseline(u, now); err == nil && n.ModTime.After(base) {
			banner = bannerNew
		}
	}
	if err := s.State.MarkRead(u, n.Path, sum, raw, now); err != nil {
		slog.Warn("web: marcar leída", "err", err)
	}
	if old == nil {
		body, hs, err := render.HTML(md, res)
		return body, hs, banner, err
	}
	body, hs, err := renderDiff(md, old, res)
	return body, hs, banner, err
}

// snapshotBody aplica a una lectura guardada la misma transformación que readView al contenido
// actual (sin frontmatter ni H1 repetido), para que mdBlocks compare bloques equivalentes.
// shortcut: el frontmatter se quita con stripFrontmatter (no valida el YAML como vault); con un
// YAML inválido el bloque viejo sale distinto y se marca como cambio. Si molesta, exportar vault.parse.
func snapshotBody(n *vault.Note, content string) string {
	old := *n
	old.Body = stripFrontmatter(normalize(content))
	return dropLeadingH1(&old)
}

// renderDiff renderiza md bloque a bloque y envuelve en <div class="chg"> los que no están en old.
// shortcut: cada bloque se renderiza aparte, así que las definiciones de enlaces por referencia,
// los footnotes y la deduplicación de ids de headings no cruzan bloques, y una lista "suelta"
// (ítems separados por línea en blanco) sale como varias listas; solo afecta a la vista con
// cambios. Si molesta, marcar los bloques sobre el AST de goldmark dentro de internal/render.
func renderDiff(md string, old map[string]bool, res resolver) (template.HTML, []render.Heading, error) {
	var b strings.Builder
	var heads []render.Heading
	for _, blk := range mdBlocks(md) {
		h, hs, err := render.HTML(blk, res)
		if err != nil {
			return "", nil, err
		}
		heads = append(heads, hs...)
		if old[blk] {
			b.WriteString(string(h))
		} else {
			b.WriteString(`<div class="chg">` + string(h) + `</div>`)
		}
	}
	return template.HTML(b.String()), heads, nil
}

// mdBlocks parte Markdown en bloques separados por línea en blanco, sin cortar code fences ni
// continuaciones indentadas (párrafos dentro de un ítem de lista). Cada bloque va recortado.
func mdBlocks(md string) []string {
	var out, cur []string
	fence, blank := "", false
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.TrimSpace(strings.Join(cur, "\n")))
			cur = nil
		}
	}
	for _, l := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if fence != "" {
			cur = append(cur, l)
			if strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]) == "" {
				fence = ""
			}
			continue
		}
		if t == "" {
			blank = len(cur) > 0
			continue
		}
		if blank {
			if l[0] == ' ' || l[0] == '\t' {
				cur = append(cur, "")
			} else {
				flush()
			}
			blank = false
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
		}
		cur = append(cur, l)
	}
	flush()
	return out
}

// markTree marca en el árbol las notas sin leer de user y las que un agente está escribiendo.
// shortcut: lee y hashea cada nota con lectura registrada y hace os.Stat del resto (NoteRef no
// trae mtime); vale para ~500 notas, para más exponer mtime y hash en el índice.
func (s *Server) markTree(r *http.Request, tree []treeNode) {
	writing := map[string]bool{}
	if s.Presence != nil {
		for _, a := range s.Presence.Active() {
			writing[a.Path] = true
		}
	}
	var hashes map[string]string
	var base time.Time
	// Con el vault vacío no se fija el baseline: si no, todo lo importado después saldría sin leer.
	if s.State != nil && len(tree) > 0 {
		u := user(r)
		var err error
		if hashes, err = s.State.ReadHashes(u); err != nil {
			slog.Warn("web: lecturas", "err", err)
			hashes = nil
		} else if base, err = s.State.Baseline(u, time.Now()); err != nil {
			slog.Warn("web: baseline", "err", err)
			hashes = nil
		}
	}
	var walk func([]treeNode)
	walk = func(ns []treeNode) {
		for i := range ns {
			n := &ns[i]
			if n.Dir {
				walk(n.Children)
				continue
			}
			n.Writing = writing[n.Path]
			if hashes != nil && !n.Current {
				n.Unread = s.unread(n.Path, hashes, base)
			}
		}
	}
	walk(tree)
}

func (s *Server) unread(p string, hashes map[string]string, base time.Time) bool {
	abs, err := s.V.Abs(p)
	if err != nil {
		return false
	}
	if h, ok := hashes[p]; ok {
		data, err := os.ReadFile(abs)
		return err == nil && hash(string(data)) != h
	}
	st, err := os.Stat(abs)
	return err == nil && st.ModTime().After(base)
}
