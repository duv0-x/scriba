package web

import (
	"strings"
	"testing"
)

func TestThemeToggle(t *testing.T) {
	h := newServer(t)
	_, page := get(t, h, "/", nil)
	if !strings.Contains(page, `id="tema"`) {
		t.Error(`falta el botón id="tema"`)
	}
	head, _, _ := strings.Cut(page, "</head>")
	// La etiqueta literal, sin defer ni async: tiene que fijar data-theme antes del primer pintado.
	tag := `<script src="` + asset("theme.js") + `"></script>`
	if !strings.Contains(head, tag) {
		t.Errorf("theme.js no está en el <head> sin defer:\n%s", head)
	}
	_, css := get(t, h, "/static/app.css", nil)
	for _, sel := range []string{`[data-theme=dark]`, `:not([data-theme=light])`} {
		if !strings.Contains(strings.ReplaceAll(css, `"`, ""), sel) {
			t.Errorf("app.css sin el selector %s", sel)
		}
	}
}
