package web

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/vault"
)

func apiGraph(t *testing.T, target string) graphJSON {
	t.Helper()
	res, body := get(t, newServer(t), target, nil)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d\n%s", res.StatusCode, body)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	var g graphJSON
	if err := json.Unmarshal([]byte(body), &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func ids(g graphJSON) []string {
	var out []string
	for _, n := range g.Nodes {
		out = append(out, n.ID)
	}
	slices.Sort(out)
	return out
}

func TestGraphAPI(t *testing.T) {
	g := apiGraph(t, "/k/principal/api/grafo")
	j := "journal/2026-11-16.md"
	want := []graphEdge{{j, "decisions/personal/adr-001.md"}, {j, "runbooks/personal/runbook-pi.md"}}
	// adr-001 aparece dos veces en el journal ([[adr-001]] y [[adr-001#Contexto]]): una sola arista.
	if !slices.Equal(g.Edges, want) {
		t.Errorf("edges = %v, quiero %v", g.Edges, want)
	}
	deg := map[string]int{}
	for _, n := range g.Nodes {
		deg[n.ID] = n.Degree
		if strings.Contains(n.ID, "no-existe") {
			t.Errorf("nodo sin resolver: %s", n.ID)
		}
	}
	if deg[j] != 2 || deg["decisions/personal/adr-001.md"] != 1 || deg["notes/bad.md"] != 0 {
		t.Errorf("degree = %v", deg)
	}
}

func TestGraphAPICentro(t *testing.T) {
	g := apiGraph(t, "/k/principal/api/grafo?centro=decisions%2Fpersonal%2Fadr-001.md")
	if got := ids(g); !slices.Equal(got, []string{"decisions/personal/adr-001.md", "journal/2026-11-16.md"}) {
		t.Errorf("nodos = %v", got)
	}
	if len(g.Edges) != 1 {
		t.Errorf("edges = %v", g.Edges)
	}
	if res, _ := get(t, newServer(t), "/k/principal/api/grafo?centro=no-existe.md", nil); res.StatusCode != 404 {
		t.Errorf("centro inexistente: status = %d", res.StatusCode)
	}
}

func TestGraphPages(t *testing.T) {
	h := newServer(t)
	cases := []struct {
		target string
		has    []string
	}{
		{"/k/principal/grafo", []string{`<canvas`, `data-grafo="/k/principal/api/grafo"`, `name="tipo" value="decisions"`, "Mostrar huérfanas",
			`class="menu-button graph-toggle" popovertarget="graph-filters"`,
			`aria-controls="graph-filters" aria-label="Abrir filtros del grafo"`,
			`class="gfilters" id="graph-filters" popover="auto"`,
			"Tamaño de nodos", "Grosor de enlaces", "Distancia", "3 notas · 2 enlaces", `href="/k/principal/grafo" class="on"`, "/static/graph.js"}},
		{"/k/principal/n/decisions/personal/adr-001.md", []string{"Grafo local", "Abrir grafo",
			`data-grafo="/k/principal/api/grafo?centro=decisions%2Fpersonal%2Fadr-001.md"`, "/static/graph.js"}},
	}
	for _, c := range cases {
		res, body := get(t, h, c.target, nil)
		if res.StatusCode != 200 {
			t.Fatalf("%s: status = %d", c.target, res.StatusCode)
		}
		for _, s := range c.has {
			if !strings.Contains(body, s) {
				t.Errorf("%s: falta %q", c.target, s)
			}
		}
	}
}

// Una nota que un agente crea después de cachear el grafo se encuentra como centro sin esperar el TTL.
func TestGraphAPICentroNotaNueva(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("../vault/testdata/vault")); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(dir)
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
	h := (&Server{KBs: oneKB(&kbs.KB{V: v, Ix: ix})}).Handler()
	get(t, h, "/k/principal/api/grafo", nil) // llena la caché

	p := "drafts/nueva.md"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, p), []byte("# Nueva\n\nVer [[adr-001]].\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := v.Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Upsert(n); err != nil {
		t.Fatal(err)
	}
	res, body := get(t, h, "/k/principal/api/grafo?centro="+url.QueryEscape(p), nil)
	if res.StatusCode != 200 || !strings.Contains(body, "decisions/personal/adr-001.md") {
		t.Fatalf("status = %d\n%s", res.StatusCode, body)
	}
}

func TestStaticVersionado(t *testing.T) {
	h := newServer(t)
	u := asset("graph.js")
	res, _ := get(t, h, u, nil)
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("%s: status %d, Cache-Control %q", u, res.StatusCode, res.Header.Get("Cache-Control"))
	}
	res, _ = get(t, h, "/static/graph.js?v=viejo", nil)
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("versión vieja: Cache-Control %q", cc)
	}
	res, _ = get(t, h, "/static/graph.js", map[string]string{"If-None-Match": res.Header.Get("ETag")})
	if res.StatusCode != 304 {
		t.Errorf("revalidación con ETag: status %d", res.StatusCode)
	}
}
