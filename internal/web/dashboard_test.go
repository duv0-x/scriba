package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/vault"
)

// dashVault crea un vault en TempDir (con git si withGit) y devuelve el handler y los bytes en disco.
func dashVault(t *testing.T, withGit bool) (*Server, int) {
	t.Helper()
	dir := t.TempDir()
	today := time.Now().Format(time.DateOnly)
	files := map[string]string{
		"journal/hoy.md": "---\ndate: " + today + "\n---\n# Hoy\n\nuno dos tres [[otra]] [[falta]]\n",
		"docs/otra.md":   "# Otra\n\ncuatro cinco seis\n",
	}
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if withGit {
		if err := gitvault.Init(dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ agent, verb, rel string }{{"claude", "crea", "journal/hoy.md"}, {"opencode", "actualiza", "docs/otra.md"}} {
		write(c.rel, files[c.rel])
		if !withGit {
			continue
		}
		if _, _, err := gitvault.CommitAll(dir, c.agent, c.agent+": "+c.verb+" "+c.rel); err != nil {
			t.Fatal(err)
		}
	}
	size := 0
	for _, c := range files {
		size += len(c)
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
	return &Server{V: v, Ix: ix}, size
}

func TestDashboard(t *testing.T) {
	s, size := dashVault(t, true)
	res, body := get(t, s.Handler(), "/", nil)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d\n%s", res.StatusCode, body)
	}
	today := time.Now().Format(time.DateOnly)
	want := []string{
		"Actividad de la base de conocimiento",
		// tiles
		`<div class="tile-k">Notas</div><div class="tile-v">2 <span class="tile-pill">+2 esta semana</span>`,
		`<div class="tile-k">Palabras</div><div class="tile-v">12 </div>`,
		`<div class="tile-k">Indexadas</div><div class="tile-v">2 <small>/ 2</small>`,
		`<div class="tile-k">Disco</div><div class="tile-v">` + itoa(size) + ` <small>B</small>`,
		// heatmap: hoy hubo 2 commits; la fecha de journal/hoy.md no se suma encima
		`title="` + today + ` · 2"`,
		"2 contribuciones en el último año",
		// actividad reciente
		`<span class="who agent">opencode</span> <span class="verb">actualizó</span>`,
		`<a href="/n/docs/otra.md">Otra</a>`,
		`<span class="who agent">claude</span> <span class="verb">creó</span>`,
		`<a href="/n/journal/hoy.md">Hoy</a>`,
		// almacenamiento
		"docs/</span>", "journal/</span>",
		"<dt>Wikilinks resueltos</dt><dd>1 / 2</dd>",
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("falta %q", w)
		}
	}
	// 53 columnas × 7 días; los días futuros de la última semana van invisibles.
	if n := strings.Count(body, `<i data-l="`) - 5 + strings.Count(body, `<i class="fut">`); n != heatWeeks*7 {
		t.Errorf("celdas del heatmap = %d, quiero %d", n, heatWeeks*7)
	}
	if strings.Contains(body, "role=\"status\"") {
		t.Error("aviso sin ?aviso=historial")
	}
	_, body = get(t, s.Handler(), "/?aviso=historial", nil)
	if !strings.Contains(body, avisoHistorial) {
		t.Error("falta el aviso de historial")
	}
}

func TestHeatmapRegionTeclado(t *testing.T) {
	s, _ := dashVault(t, true)
	_, body := get(t, s.Handler(), "/", nil)
	for _, want := range []string{
		`class="heat-wrap" tabindex="0" role="region" aria-label="Historial anual de notas"`,
		`class="heat-inner"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard sin %q", want)
		}
	}
}

func TestDashboardSinGit(t *testing.T) {
	s, _ := dashVault(t, false)
	res, body := get(t, s.Handler(), "/", nil)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d\n%s", res.StatusCode, body)
	}
	for _, w := range []string{"Todavía no hay commits en la base.", "1 contribución en el último año", "+1 esta semana"} {
		if !strings.Contains(body, w) {
			t.Errorf("falta %q", w)
		}
	}
}

func TestDashFormats(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.Local)
	cases := [][2]string{
		{miles(1284), "1.284"},
		{miles(int64(1234567)), "1.234.567"},
		{humanBytes(43_200_000), "41,2 MB"},
		{humanBytes(2048), "2,0 KB"},
		{wordsTile(184_213).Value, "184"},
		{wordsTile(1500).Value, "1,5"},
		{hace(now.Add(-4*time.Minute), now), "hace 4 min"},
		{hace(now.Add(-24*time.Hour), now), "ayer"},
		{hace(now.Add(-72*time.Hour), now), "hace 3 d"},
		{verbOf("claude: crea journal/x.md"), "creó"},
		{verbOf("importa historial"), "importa historial"},
	}
	for _, c := range cases {
		if got, want := c[0], c[1]; got != want {
			t.Errorf("got %q, quiero %q", got, want)
		}
	}
	if heatLevel(0) != 0 || heatLevel(2) != 1 || heatLevel(5) != 2 || heatLevel(9) != 3 || heatLevel(10) != 4 {
		t.Error("umbrales del heatmap")
	}
}

func itoa(n int) string { return miles(n) }
