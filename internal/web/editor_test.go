package web

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestEditorGet(t *testing.T) {
	h, _ := newEditor(t, true)
	res, body := get(t, h, "/k/principal/editar/notes/hoy.md", nil)
	if res.StatusCode != 200 {
		t.Fatalf("status %d\n%s", res.StatusCode, body)
	}
	for _, s := range []string{"<textarea", "Texto original.", `name="base" value="` + hash(editNote) + `"`, "Editando", "Guardar", `href="/k/principal/n/notes/hoy.md"`} {
		if !strings.Contains(body, s) {
			t.Errorf("falta %q", s)
		}
	}
}

func TestEditorGuarda(t *testing.T) {
	h, root := newEditor(t, true)
	nuevo := "---\r\ntype: journal\r\n---\r\n\r\n# Hoy\r\n\r\nTexto nuevo.\r\n"
	res, body := post(t, h, "/k/principal/editar/notes/hoy.md", url.Values{"content": {nuevo}, "base": {hash(editNote)}}, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/k/principal/n/notes/hoy.md" {
		t.Fatalf("status %d location %q\n%s", res.StatusCode, res.Header.Get("Location"), body)
	}
	b, _ := os.ReadFile(filepath.Join(root, "notes/hoy.md"))
	if string(b) != strings.ReplaceAll(nuevo, "\r\n", "\n") {
		t.Errorf("contenido = %q", b)
	}
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%an").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "web via Scriba" {
		t.Errorf("autor del commit = %q (%v)", out, err)
	}
}

func TestEditorConflicto(t *testing.T) {
	h, root := newEditor(t, true)
	agente := editNote + "\nLínea del agente.\n"
	if err := os.WriteFile(filepath.Join(root, "notes/hoy.md"), []byte(agente), 0o644); err != nil {
		t.Fatal(err)
	}
	res, body := post(t, h, "/k/principal/editar/notes/hoy.md", url.Values{"content": {"Mi versión"}, "base": {hash(editNote)}}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status %d", res.StatusCode)
	}
	for _, s := range []string{"La nota cambió mientras la editabas", "Mi versión", `value="` + hash(agente) + `"`, "Línea del agente.", "<details"} {
		if !strings.Contains(body, s) {
			t.Errorf("falta %q", s)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "notes/hoy.md")); string(b) != agente {
		t.Errorf("el archivo cambió: %q", b)
	}
}

func TestVistaPrevia(t *testing.T) {
	h, _ := newEditor(t, true)
	res, body := post(t, h, "/k/principal/vista-previa", url.Values{"content": {"---\ntitle: x\n---\n## Hola\n\n<script>alert(1)</script>\n"}}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if !strings.Contains(body, "<h2") || strings.Contains(body, "<html") || strings.Contains(body, "<script>") || strings.Contains(body, "title: x") {
		t.Errorf("vista previa = %q", body)
	}
	res, _ = post(t, h, "/k/principal/vista-previa", url.Values{"content": {strings.Repeat("a", writer.MaxBytes+1)}}, nil)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("1 MB+: status %d", res.StatusCode)
	}
}

func TestEditorRutas(t *testing.T) {
	h, _ := newEditor(t, true)
	if res, _ := get(t, h, "/k/principal/editar/notes/no-existe.md", nil); res.StatusCode != 404 {
		t.Errorf("inexistente: %d", res.StatusCode)
	}
	if res, _ := post(t, h, "/k/principal/editar/notes/no-existe.md", url.Values{"content": {"x"}}, nil); res.StatusCode != 404 {
		t.Errorf("POST inexistente: %d", res.StatusCode)
	}
	if res, _ := get(t, h, "/k/principal/editar/..%2F..%2Fetc%2Fpasswd", nil); res.StatusCode != 400 {
		t.Errorf("fuera del vault: %d", res.StatusCode)
	}

	sin, _ := newEditor(t, false)
	if res, _ := get(t, sin, "/k/principal/editar/notes/hoy.md", nil); res.StatusCode != 404 {
		t.Errorf("sin writer GET: %d", res.StatusCode)
	}
	if res, _ := post(t, sin, "/k/principal/vista-previa", url.Values{"content": {"x"}}, nil); res.StatusCode != 404 && res.StatusCode != 405 {
		t.Errorf("sin writer vista previa: %d", res.StatusCode)
	}
}

func TestEditorConflictoReenvioYForzar(t *testing.T) {
	h, root := newEditor(t, true)
	abs := filepath.Join(root, "notes/hoy.md")
	agente := editNote + "\nLínea del agente.\n"
	if err := os.WriteFile(abs, []byte(agente), 0o644); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"content": {"Mi versión"}, "base": {hash(editNote)}}
	res, body := post(t, h, "/k/principal/editar/notes/hoy.md", form, nil)
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, `name="base" value="`+hash(editNote)+`"`) ||
		!strings.Contains(body, `name="force" value="`+hash(agente)+`"`) || !strings.Contains(body, "Guardar de todos modos") {
		t.Fatalf("conflicto: status %d\n%s", res.StatusCode, body)
	}
	// Reenviar (Guardar o ⌘S, sin force) vuelve a dar conflicto y no toca el archivo.
	if res, _ := post(t, h, "/k/principal/editar/notes/hoy.md", form, nil); res.StatusCode != http.StatusConflict {
		t.Fatalf("reenvío: status %d", res.StatusCode)
	}
	if b, _ := os.ReadFile(abs); string(b) != agente {
		t.Fatalf("el reenvío pisó al agente: %q", b)
	}
	// Forzar con un hash que ya cambió: 409 otra vez.
	otro := agente + "\nOtra más.\n"
	if err := os.WriteFile(abs, []byte(otro), 0o644); err != nil {
		t.Fatal(err)
	}
	form.Set("force", hash(agente))
	if res, _ := post(t, h, "/k/principal/editar/notes/hoy.md", form, nil); res.StatusCode != http.StatusConflict {
		t.Fatalf("forzar con hash viejo: status %d", res.StatusCode)
	}
	if b, _ := os.ReadFile(abs); string(b) != otro {
		t.Fatalf("forzar con hash viejo escribió: %q", b)
	}
	// "Guardar de todos modos" con el hash vigente escribe.
	form.Set("force", hash(otro))
	form.Set("message", strings.Repeat("ñ", 250))
	if res, body := post(t, h, "/k/principal/editar/notes/hoy.md", form, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("forzar: status %d\n%s", res.StatusCode, body)
	}
	if b, _ := os.ReadFile(abs); string(b) != "Mi versión\n" {
		t.Fatalf("forzar no escribió: %q", b)
	}
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%s").CombinedOutput()
	if err != nil || !strings.Contains(string(out), strings.Repeat("ñ", 200)) || strings.Contains(string(out), strings.Repeat("ñ", 201)) {
		t.Errorf("mensaje sin recortar a 200: %q (%v)", out, err)
	}
}
