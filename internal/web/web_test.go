package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/render"
	"github.com/duv0-x/scriba/internal/vault"
)

func newServer(t *testing.T) http.Handler {
	t.Helper()
	v, err := vault.Open("../vault/testdata/vault")
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(t.TempDir(), "scriba.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	if err := ix.Rebuild(v); err != nil {
		t.Fatal(err)
	}
	return (&Server{KBs: oneKB(&kbs.KB{V: v, Ix: ix})}).Handler()
}

func get(t *testing.T, h http.Handler, target string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	return rec.Result(), string(body)
}

func TestRoutes(t *testing.T) {
	h := newServer(t)
	cases := []struct {
		name, target string
		hdr          map[string]string
		status       int
		has, hasNot  []string
	}{
		{name: "nota con backlink", target: "/k/principal/n/decisions/personal/adr-001.md", status: 200,
			has: []string{"ADR-001 · Markdown plano", `href="/k/principal/n/journal/2026-11-16.md"`, "Enlazada desde · 1"}},
		{name: "wikilinks", target: "/k/principal/n/journal/2026-11-16.md", status: 200,
			has: []string{`class="wl missing"`, `class="wl" href="/k/principal/n/decisions/personal/adr-001.md"`}},
		{name: "fuera del vault codificada", target: "/k/principal/n/..%2F..%2Fetc%2Fpasswd", status: 400, hasNot: []string{"root:"}},
		{name: "directorio oculto", target: "/k/principal/n/.git/config", status: 404},
		{name: "no existe", target: "/k/principal/n/no-existe.md", status: 404, has: []string{"Esta nota no existe", "<html"}},
		{name: "búsqueda htmx", target: "/k/principal/buscar?q=configuracion", hdr: map[string]string{"HX-Request": "true"}, status: 200,
			has: []string{"<mark>"}, hasNot: []string{"<html"}},
		{name: "búsqueda página", target: "/k/principal/buscar?q=configuracion", status: 200, has: []string{"<html", "<mark>"}},
		{name: "sin resultados", target: "/k/principal/buscar?q=zzzz", hdr: map[string]string{"HX-Request": "true"}, status: 200,
			has: []string{"Sin resultados para “zzzz”."}},
		{name: "readyz", target: "/readyz", status: 200, has: []string{"ok"}},
		{name: "inicio", target: "/k/principal/", status: 200, has: []string{"Notas por día", "Actividad de la base de conocimiento"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, body := get(t, h, c.target, c.hdr)
			if res.StatusCode != c.status {
				t.Fatalf("status = %d, quiero %d\n%s", res.StatusCode, c.status, body)
			}
			for _, s := range c.has {
				if !strings.Contains(body, s) {
					t.Errorf("falta %q", s)
				}
			}
			for _, s := range c.hasNot {
				if strings.Contains(body, s) {
					t.Errorf("sobra %q", s)
				}
			}
		})
	}
}

// El ServeMux limpia los ".." y redirige: nunca debe servir contenido de fuera.
func TestLegacyRedirects(t *testing.T) {
	h := newServer(t)
	for _, tt := range []struct {
		method, target, want string
		code                 int
	}{
		{http.MethodGet, "/n/README.md?x=1", "/k/principal/n/README.md?x=1", http.StatusMovedPermanently},
		{http.MethodGet, "/", "/k/principal/", http.StatusMovedPermanently},
	} {
		req := httptest.NewRequest(tt.method, tt.target, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		res := rec.Result()
		if res.StatusCode != tt.code || res.Header.Get("Location") != tt.want {
			t.Errorf("%s %s = %d, Location %q; quiero %d, %q", tt.method, tt.target, res.StatusCode, res.Header.Get("Location"), tt.code, tt.want)
		}
	}
}

func TestKBSelection(t *testing.T) {
	h := newServer(t)
	if res, _ := get(t, h, "/k/noexiste/", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("KB inexistente = %d; quiero 404", res.StatusCode)
	}
	// Una petición de fondo (SSE, JSON) no cambia la última KB visitada.
	if res, _ := get(t, h, "/k/principal/api/grafo", nil); len(res.Cookies()) != 0 {
		t.Errorf("/api/grafo fijó cookies: %v", res.Cookies())
	}
	res, _ := get(t, h, "/k/principal/", map[string]string{"Accept": "text/html"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("KB existente = %d; quiero 200", res.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "scriba_kb" {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != "principal" {
		t.Errorf("cookie de KB = %v; quiero scriba_kb=principal", cookie)
	}
}

func TestSearchUsesRequestKB(t *testing.T) {
	newKB := func(slug, title, body string) *kbs.KB {
		t.Helper()
		dir := t.TempDir()
		v, err := vault.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "nota.md"), []byte("# "+title+"\n\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		ix, err := index.Open(filepath.Join(t.TempDir(), "scriba.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ix.Close() })
		if err := ix.Rebuild(v); err != nil {
			t.Fatal(err)
		}
		return &kbs.KB{Slug: slug, V: v, Ix: ix}
	}
	principal := newKB("principal", "Nota principal", "palabraunicaparabuscar")
	otra := newKB("otra", "Nota alterna", "contenido distinto")
	h := (&Server{KBs: &fakeKBs{list: []*kbs.KB{principal, otra}}}).Handler()

	_, body := get(t, h, "/k/otra/buscar?q=palabraunicaparabuscar", nil)
	if strings.Contains(body, "Nota principal") {
		t.Error("la búsqueda de otra KB devolvió la nota de principal")
	}
	_, body = get(t, h, "/k/principal/buscar?q=palabraunicaparabuscar", nil)
	if !strings.Contains(body, "Nota principal") {
		t.Error("la búsqueda de principal no devolvió su nota")
	}

	// Selector de KB: la actual en el summary y marcada; las demás con navegación completa.
	_, body = get(t, h, "/k/principal/", nil)
	for _, want := range []string{
		`<summary aria-describedby="kbsel-h"><code>principal</code>`,
		`href="/k/otra/" hx-boost="false"><code>otra</code>`,
		`href="/k/principal/" hx-boost="false" aria-current="page"`,
		`href="/kbs"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q en el selector", want)
		}
	}
}

func TestTraversalSinCodificar(t *testing.T) {
	res, body := get(t, newServer(t), "/k/principal/n/../../etc/passwd", nil)
	if res.StatusCode == http.StatusOK || strings.Contains(body, "root:") {
		t.Fatalf("status %d sirve contenido de fuera", res.StatusCode)
	}
}

func TestStatic(t *testing.T) {
	res, _ := get(t, newServer(t), "/static/app.css", nil)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("status %d, Content-Type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if res.Header.Get("Cache-Control") != "no-cache" || res.Header.Get("ETag") == "" { // sin ?v= se revalida
		t.Errorf("Cache-Control = %q", res.Header.Get("Cache-Control"))
	}
}

func TestReadingMinutes(t *testing.T) {
	for words, want := range map[int]int{0: 1, 100: 1, 330: 2, 660: 3} {
		if got := readingMinutes(words); got != want {
			t.Errorf("readingMinutes(%d) = %d, quiero %d", words, got, want)
		}
	}
}

// sideHTML recorta el HTML de la sidebar para no confundirla con el lector.
func sideHTML(t *testing.T, body string) string {
	t.Helper()
	i, j := strings.Index(body, `<aside class="side"`), strings.Index(body, "</aside>")
	if i < 0 || j < i {
		t.Fatal("no hay sidebar")
	}
	return body[i:j]
}

func TestSidebarTree(t *testing.T) {
	h := newServer(t)
	_, body := get(t, h, "/k/principal/n/decisions/personal/adr-001.md", nil)
	side := sideHTML(t, body)
	for _, s := range []string{
		`<details open><summary title="decisions">`,
		`<details open><summary title="decisions/personal">`,
		`aria-current="page" title="decisions/personal/adr-001.md">ADR-001 · Markdown plano</a>`,
		`<details><summary title="journal">`,
	} {
		if !strings.Contains(side, s) {
			t.Errorf("falta %q en la sidebar:\n%s", s, side)
		}
	}
	if strings.Contains(side, ">adr-001.md<") {
		t.Error("la sidebar muestra el nombre de archivo en vez del título")
	}

	_, body = get(t, h, "/k/principal/", nil)
	if side := sideHTML(t, body); strings.Contains(side, "<details open") || !strings.Contains(side, "<details") {
		t.Errorf("en la home todo debe ir plegado:\n%s", side)
	}
}

func TestBuildTree(t *testing.T) {
	refs := []index.NoteRef{
		{Path: "decisions/a/x.md", Title: "X"},
		{Path: "decisions/a/y.md", Title: "Y"},
		{Path: "decisions/z.md", Title: "Z"},
		{Path: "journal/2025-04-29.md", Title: "Abril"},
		{Path: "journal/2026-11-16.md", Title: "2026-11-16"},
		{Path: "journal/notas.md", Title: "Suelta", Date: "2026-11-02"},
		{Path: "journal/raro.md", Title: "Raro"},
		{Path: "otros/o.md", Title: "O"},
		{Path: "raiz.md", Title: "Raíz"},
	}
	tree := buildTree(refs, "journal/2026-11-16.md")

	var labels []string
	for _, n := range tree {
		labels = append(labels, fmt.Sprintf("%s:%d", n.Label, n.N))
	}
	// journal antes que decisions por el orden de vault.KnownTypes; notas sueltas al final.
	if got, want := strings.Join(labels, " "), "journal:4 decisions:3 otros:1 Raíz:0"; got != want {
		t.Fatalf("raíz = %q; quiero %q", got, want)
	}
	if !tree[0].Open || tree[1].Open {
		t.Error("solo journal debe ir abierto")
	}
	if d := tree[1]; d.Children[0].Label != "a" || d.Children[0].N != 2 || d.Children[1].Label != "Z" {
		t.Errorf("decisions = %+v", d.Children)
	}

	var groups []string
	for _, y := range tree[0].Children {
		if y.Label == "Sin fecha" {
			groups = append(groups, fmt.Sprintf("Sin fecha:%d", y.N))
			continue
		}
		for _, m := range y.Children {
			var notes []string
			for _, n := range m.Children {
				notes = append(notes, n.Label)
			}
			groups = append(groups, fmt.Sprintf("%s › %s:%d [%s]", y.Label, m.Label, m.N, strings.Join(notes, ", ")))
		}
	}
	want := []string{"2026 › noviembre:2 [2026-11-16, 2 · Suelta]", "2025 › abril:1 [29 · Abril]", "Sin fecha:1"}
	if !slices.Equal(groups, want) {
		t.Fatalf("journal = %q; quiero %q", groups, want)
	}
	if nov := tree[0].Children[0].Children[0]; !nov.Open || !nov.Children[0].Current {
		t.Error("la nota actual del journal debe ir marcada y su mes abierto")
	}
}

func TestDropLeadingH1(t *testing.T) {
	cases := []struct {
		n    vault.Note
		keep bool
	}{
		{vault.Note{Title: "A", Meta: map[string]any{"title": "A"}, Body: "# Otro\n\ntexto"}, true},
		{vault.Note{Title: "A", Meta: map[string]any{"title": "A"}, Body: "# A\n\ntexto"}, false},
		{vault.Note{Title: "Otro", Meta: map[string]any{}, Body: "\n# Otro\n\ntexto"}, false},
	}
	for _, c := range cases {
		if got := strings.Contains(dropLeadingH1(&c.n), "# "); got != c.keep {
			t.Errorf("dropLeadingH1(%q) conserva h1 = %v; quiero %v", c.n.Body, got, c.keep)
		}
	}
	html, _, err := render.HTML(dropLeadingH1(&cases[0].n), resolver{})
	if err != nil || !strings.Contains(string(html), "Otro") {
		t.Fatalf("render = %q, %v", html, err)
	}
}

func TestStaticNoDirs(t *testing.T) {
	h := newServer(t)
	for target, want := range map[string]int{"/static/": 404, "/static/fonts/": 404, "/static/app.css": 200} {
		if res, _ := get(t, h, target, nil); res.StatusCode != want {
			t.Errorf("GET %s = %d; quiero %d", target, res.StatusCode, want)
		}
	}
}

func TestDatedLabel(t *testing.T) {
	for _, tt := range []struct{ p, title, want string }{
		{"tickets/personal/2026-11-23-scriba-14-x.md", "scriba#14 — Navegación", "2026-11-23 · scriba#14 — Navegación"},
		{"plans/2026-11-16-plan.md", "2026-11-16 · Plan", "2026-11-16 · Plan"},
		{"runbooks/rotar-certificados.md", "Rotar certificados", "Rotar certificados"},
	} {
		if got := datedLabel(tt.p, tt.title); got != tt.want {
			t.Errorf("datedLabel(%q, %q) = %q; quiero %q", tt.p, tt.title, got, tt.want)
		}
	}
}

func TestGridResponsiveSinMinimoIntrinseco(t *testing.T) {
	_, css := get(t, newServer(t), "/static/app.css", nil)
	compact := strings.Join(strings.Fields(css), "")
	for _, want := range []string{
		`.reader{grid-template-columns:minmax(0,1fr);padding:28px20px60px}`,
		`grid-template-columns:minmax(0,1.4fr)minmax(0,1fr);`,
		`.grid2{grid-template-columns:minmax(0,1fr)}`,
	} {
		if !strings.Contains(compact, want) {
			t.Errorf("app.css sin %q", want)
		}
	}
}
