package index

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/vault"
)

const adr = "decisions/personal/adr-001.md"

func setup(t *testing.T) *Index {
	t.Helper()
	ix, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	v, err := vault.Open("../vault/testdata/vault")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Rebuild(v); err != nil {
		t.Fatal(err)
	}
	return ix
}

func hitPaths(t *testing.T, ix *Index, q string) []string {
	t.Helper()
	hits, err := ix.Search(q, 0)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	var out []string
	for _, h := range hits {
		out = append(out, h.Path)
	}
	return out
}

func backPaths(t *testing.T, ix *Index, p string) []string {
	t.Helper()
	refs, err := ix.Backlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range refs {
		out = append(out, r.Path)
	}
	return out
}

func TestIndex(t *testing.T) {
	ix := setup(t)

	if n, err := ix.NoteCount(); err != nil || n != 4 {
		t.Fatalf("NoteCount = %d, %v; want 4", n, err)
	}

	hits, err := ix.Search("configuracion", 0)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(hits, func(h Hit) bool { return h.Path == adr })
	if i < 0 || !strings.Contains(hits[i].Snippet, "<mark>") {
		t.Fatalf("Search(configuracion) = %+v", hits)
	}

	if b := backPaths(t, ix, adr); !slices.Contains(b, "journal/2026-11-16.md") {
		t.Fatalf("Backlinks = %v", b)
	}

	out, err := ix.Outlinks("journal/2026-11-16.md")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Link{
		"adr-001":    {Raw: "adr-001", Dst: adr, Exists: true},
		"runbook-pi": {Raw: "runbook-pi", Dst: "runbooks/personal/runbook-pi.md", Exists: true},
		"no-existe":  {Raw: "no-existe"},
	}
	for _, l := range out {
		if w, ok := want[l.Raw]; ok {
			if l != w {
				t.Errorf("Outlink %q = %+v; want %+v", l.Raw, l, w)
			}
			delete(want, l.Raw)
		}
	}
	if len(want) > 0 {
		t.Errorf("faltan outlinks: %v", want)
	}

	for _, q := range []string{`ab"c*(`, "", "()", "@ #", "AND OR NOT"} {
		hitPaths(t, ix, q)
	}

	counts, err := ix.Counts()
	if err != nil {
		t.Fatal(err)
	}
	for typ, n := range map[string]int{"decisions": 1, "journal": 1, "runbooks": 1, "note": 1} {
		if counts[typ] != n {
			t.Errorf("Counts[%s] = %d; want %d (%v)", typ, counts[typ], n, counts)
		}
	}
}

func TestUpsertDelete(t *testing.T) {
	ix := setup(t)
	n := &vault.Note{Path: "tmp/nueva.md", Title: "Nueva", Type: "note", Body: "zanahoria morada",
		Links: []string{"adr-001"}, ModTime: time.Now()}
	if err := ix.Upsert(n); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(hitPaths(t, ix, "zanahoria"), n.Path) || !slices.Contains(backPaths(t, ix, adr), n.Path) {
		t.Fatal("la nota nueva no aparece tras Upsert")
	}
	if err := ix.Delete(n.Path); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(hitPaths(t, ix, "zanahoria"), n.Path) || slices.Contains(backPaths(t, ix, adr), n.Path) {
		t.Fatal("la nota sigue apareciendo tras Delete")
	}
}

func TestSnippetEscaped(t *testing.T) {
	ix := setup(t)
	n := &vault.Note{Path: "tmp/xss.md", Title: "XSS", Type: "note", Body: "buscame <script>alert(1)</script>", ModTime: time.Now()}
	if err := ix.Upsert(n); err != nil {
		t.Fatal(err)
	}
	hits, err := ix.Search("buscame", 0)
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits = %+v, %v", hits, err)
	}
	s := hits[0].Snippet
	if strings.Contains(s, "<script>") || !strings.Contains(s, "&lt;script&gt;") || !strings.Contains(s, "<mark>buscame</mark>") {
		t.Fatalf("snippet sin escapar: %q", s)
	}
}

func TestOpenTwice(t *testing.T) {
	p := filepath.Join(t.TempDir(), "index.db")
	for range 2 {
		ix, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		ix.Close()
	}
}

func TestRefs(t *testing.T) {
	refs, err := setup(t).Refs()
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, r := range refs {
		paths = append(paths, r.Path)
	}
	if len(refs) != 4 || !slices.IsSorted(paths) {
		t.Fatalf("Refs = %v", paths)
	}
	want := NoteRef{Path: adr, Title: "ADR-001 · Markdown plano", Type: "decisions", Date: "2026-11-16"}
	if i := slices.Index(paths, adr); i < 0 || refs[i] != want {
		t.Fatalf("Refs[%s] = %+v; want %+v", adr, refs, want)
	}
}
