package gitvault

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// commitAt hace un commit de name con fecha at; persona = autor sin " via Scriba".
func commitAt(t *testing.T, dir, name, author, msg string, persona bool, at time.Time) {
	t.Helper()
	d := at.Format(time.RFC3339)
	t.Setenv("GIT_AUTHOR_DATE", d)
	t.Setenv("GIT_COMMITTER_DATE", d)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, name, msg+"\n")
	if !persona {
		if _, ok, err := CommitAll(dir, author, msg); err != nil || !ok {
			t.Fatalf("CommitAll: ok=%v err=%v", ok, err)
		}
		return
	}
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.name="+author, "-c", "user.email=p@x", "-c", "commit.gpgsign=false", "commit", "-q", "-m", msg)
}

func TestLogVacio(t *testing.T) {
	ctx := context.Background()
	noRepo := t.TempDir()
	vacio := t.TempDir()
	if err := Init(vacio); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{noRepo, vacio} {
		if cs, err := Log(ctx, dir, time.Time{}, 0); err != nil || len(cs) != 0 {
			t.Fatalf("Log(%s) = %v %v", dir, cs, err)
		}
		if m, err := DayCounts(ctx, dir, time.Time{}); err != nil || m == nil || len(m) != 0 {
			t.Fatalf("DayCounts(%s) = %v %v", dir, m, err)
		}
	}
}

func TestLog(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.Local)
	commitAt(t, dir, "viejo.md", "x", "viejo", false, t0)
	commitAt(t, dir, "notas/año nuevo.md", "Persona", "asunto «raro» — ñ & $HOME `x`", true, t0.Add(24*time.Hour))
	commitAt(t, dir, "dir con espacios/canción.md", "x", "tercero", false, t0.Add(48*time.Hour))
	write(t, dir, "otro.md", "o\n")
	commitAt(t, dir, "a b.md", "claude", "cuarto", false, t0.Add(48*time.Hour+time.Hour))
	// Commit vacío: sin archivos.
	git(t, dir, "-c", "user.name=Persona", "-c", "user.email=p@x", "commit", "-q", "--allow-empty", "-m", "vacío")

	cs, err := Log(ctx, dir, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 5 {
		t.Fatalf("len = %d: %+v", len(cs), cs)
	}
	if cs[0].Subject != "vacío" || len(cs[0].Files) != 0 || cs[0].Agent {
		t.Fatalf("vacío = %+v", cs[0])
	}
	if c := cs[1]; c.Author != "claude" || !c.Agent || c.Subject != "cuarto" ||
		!slices.Equal(c.Files, []string{"a b.md", "otro.md"}) || !c.At.Equal(t0.Add(49*time.Hour)) || len(c.Hash) != 40 {
		t.Fatalf("cuarto = %+v", c)
	}
	if c := cs[2]; !slices.Equal(c.Files, []string{"dir con espacios/canción.md"}) {
		t.Fatalf("tercero = %+v", c)
	}
	if c := cs[3]; c.Author != "Persona" || c.Agent || c.Subject != "asunto «raro» — ñ & $HOME `x`" ||
		!slices.Equal(c.Files, []string{"notas/año nuevo.md"}) {
		t.Fatalf("persona = %+v", c)
	}
	if c := cs[4]; c.Author != "x" || !c.Agent || c.Subject != "viejo" {
		t.Fatalf("viejo = %+v", c)
	}

	if cs, err := Log(ctx, dir, time.Time{}, 2); err != nil || len(cs) != 2 || cs[1].Subject != "cuarto" {
		t.Fatalf("limit 2 = %+v %v", cs, err)
	}
	// since excluye "viejo" y "persona"; el commit vacío lleva la fecha de t.Setenv (la del cuarto).
	if cs, err := Log(ctx, dir, t0.Add(36*time.Hour), 0); err != nil || len(cs) != 3 || cs[2].Subject != "tercero" {
		t.Fatalf("since = %+v %v", cs, err)
	}

	m, err := DayCounts(ctx, dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"2026-03-10": 1, "2026-03-11": 1, "2026-03-12": 3}
	if len(m) != len(want) {
		t.Fatalf("DayCounts = %v", m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("DayCounts = %v, se esperaba %v", m, want)
		}
	}
	if m, err := DayCounts(ctx, dir, t0.Add(36*time.Hour)); err != nil || len(m) != 1 || m["2026-03-12"] != 3 {
		t.Fatalf("DayCounts since = %v %v", m, err)
	}
}
