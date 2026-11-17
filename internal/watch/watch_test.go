package watch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/vault"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func found(t *testing.T, ix *index.Index, q, path string) bool {
	t.Helper()
	hits, err := ix.Search(q, 0)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	return slices.ContainsFunc(hits, func(h index.Hit) bool { return h.Path == path })
}

// eventually hace poll cada 50 ms hasta 3 s.
func eventually(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timeout: %s", msg)
}

func TestRun(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "inicial.md"), "nota inicial")
	write(t, filepath.Join(root, "dir", "otra.md"), "otra nota")
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	if err := ix.Rebuild(v); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var changed []string
	onChange := func(p string) { mu.Lock(); changed = append(changed, p); mu.Unlock() }
	saw := func(p string) bool { mu.Lock(); defer mu.Unlock(); return slices.Contains(changed, p) }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, v, ix, onChange) }()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(100 * time.Millisecond) // shortcut: deja que Run registre los watches antes de escribir

	a := filepath.Join(root, "a.md")
	write(t, a, "hola mundo")
	eventually(t, "crear a.md", func() bool { return found(t, ix, "mundo", "a.md") && saw("a.md") })

	write(t, a, "adiós tierra")
	eventually(t, "modificar a.md", func() bool { return found(t, ix, "tierra", "a.md") && !found(t, ix, "mundo", "a.md") })

	write(t, filepath.Join(root, "sub", "b.md"), "pinguino")
	eventually(t, "sub/b.md", func() bool { return found(t, ix, "pinguino", "sub/b.md") })

	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	eventually(t, "borrar a.md", func() bool { return !found(t, ix, "tierra", "a.md") })

	write(t, filepath.Join(root, ".oculto", "c.md"), "secreto")
	write(t, filepath.Join(root, "x.txt"), "texto plano")
	time.Sleep(time.Second)
	if found(t, ix, "secreto", ".oculto/c.md") || found(t, ix, "texto", "x.txt") {
		t.Fatal("se indexó un archivo ignorado")
	}

	if err := os.RemoveAll(filepath.Join(root, "sub")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "borrar sub/", func() bool { return !found(t, ix, "pinguino", "sub/b.md") })

	write(t, filepath.Join(root, "docs1", "n.md"), "kiwi")
	eventually(t, "crear docs1/n.md", func() bool { return found(t, ix, "kiwi", "docs1/n.md") })
	if err := os.Rename(filepath.Join(root, "docs1"), filepath.Join(root, "docs2")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "renombrar docs1/ a docs2/", func() bool {
		hits, err := ix.Search("kiwi", 0)
		return err == nil && len(hits) == 1 && hits[0].Path == "docs2/n.md"
	})

	// Directorio que llega ya con una nota dentro (movido desde fuera del vault).
	tmp := filepath.Join(t.TempDir(), "movido")
	write(t, filepath.Join(tmp, "m.md"), "mango")
	if err := os.Rename(tmp, filepath.Join(root, "movido")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "mover movido/ con m.md", func() bool { return found(t, ix, "mango", "movido/m.md") })

	// Borrar un árbol anidado saca todas sus notas del índice.
	write(t, filepath.Join(root, "a", "b", "c.md"), "papaya")
	write(t, filepath.Join(root, "a", "d.md"), "guayaba")
	eventually(t, "crear a/b/c.md y a/d.md", func() bool {
		return found(t, ix, "papaya", "a/b/c.md") && found(t, ix, "guayaba", "a/d.md")
	})
	if err := os.RemoveAll(filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "borrar a/", func() bool { return !found(t, ix, "papaya", "a/b/c.md") && !found(t, ix, "guayaba", "a/d.md") })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		done <- nil // para el <-done del Cleanup
	case <-time.After(2 * time.Second):
		t.Fatal("Run no terminó tras cancelar")
	}
}

// Una nota indexada que pasa a ser un symlink a una zona oculta sale del índice.
func TestNotaIlegibleSaleDelIndice(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "config"), "[core] secreto")
	a := filepath.Join(root, "notes", "a.md")
	write(t, a, "nota visible")
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	if err := ix.Rebuild(v); err != nil {
		t.Fatal(err)
	}
	if !found(t, ix, "visible", "notes/a.md") {
		t.Fatal("notes/a.md no quedó indexada")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, v, ix, nil) }()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(100 * time.Millisecond) // shortcut: deja que Run registre los watches antes de tocar archivos

	// El symlink reemplaza a la nota de forma atómica: la ruta nunca deja de existir.
	tmp := filepath.Join(root, "notes", "tmp-link")
	if err := os.Symlink("../.git/config", tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, a); err != nil {
		t.Fatal(err)
	}
	eventually(t, "notes/a.md fuera del índice", func() bool { return !found(t, ix, "visible", "notes/a.md") })
	if found(t, ix, "secreto", "notes/a.md") {
		t.Fatal("se indexó el contenido de .git/config")
	}
}
