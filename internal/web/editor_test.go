package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/vault"
	"github.com/duv0-x/scriba/internal/writer"
)

const editNote = "---\ntype: journal\n---\n\n# Hoy\n\nTexto original.\n"

// newEditor arma un Server con Writer real sobre un vault git en TempDir con notes/hoy.md.
func newEditor(t *testing.T, withWriter bool) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	if err := gitvault.Init(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes/hoy.md"), []byte(editNote), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(root)
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
	k := &kbs.KB{V: v, Ix: ix}
	if withWriter {
		k.W = writer.New(writer.Config{V: v, Ix: ix})
	}
	return (&Server{KBs: oneKB(k)}).Handler(), root
}

func TestEditarRedirige(t *testing.T) {
	for _, withWriter := range []bool{true, false} {
		h, _ := newEditor(t, withWriter)
		res, _ := get(t, h, "/k/principal/editar/notes/hoy.md", nil)
		if res.StatusCode != http.StatusMovedPermanently {
			t.Errorf("withWriter=%v: status %d, quiero 301", withWriter, res.StatusCode)
		}
		if got := res.Header.Get("Location"); got != "/k/principal/n/notes/hoy.md" {
			t.Errorf("withWriter=%v: Location %q, quiero %q", withWriter, got, "/k/principal/n/notes/hoy.md")
		}
	}

	h, _ := newEditor(t, true)
	res, _ := get(t, h, "/k/principal/editar/notes/mi%20nota.md", nil)
	if res.StatusCode != http.StatusMovedPermanently {
		t.Errorf("espacio: status %d, quiero 301", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/k/principal/n/notes/mi%20nota.md" {
		t.Errorf("espacio: Location %q, quiero %q", got, "/k/principal/n/notes/mi%20nota.md")
	}

	if res, _ := post(t, h, "/k/principal/vista-previa", url.Values{"content": {"x"}}, nil); res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /vista-previa: status %d, quiero 404 o 405", res.StatusCode)
	}
	if res, _ := post(t, h, "/k/principal/editar/notes/hoy.md", url.Values{"content": {"x"}}, nil); res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /editar: status %d, quiero 405", res.StatusCode)
	}

	// Legado sin prefijo: redirige a la ruta con prefijo de KB, que a su vez redirige al lector.
	res, _ = get(t, h, "/editar/notes/hoy.md", nil)
	if res.StatusCode != http.StatusMovedPermanently && res.StatusCode != http.StatusPermanentRedirect {
		t.Errorf("legacy: status %d, quiero 301 o 308", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/k/principal/editar/notes/hoy.md" {
		t.Errorf("legacy: Location %q, quiero %q", got, "/k/principal/editar/notes/hoy.md")
	}
}
