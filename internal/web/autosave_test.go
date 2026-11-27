package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/events"
	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/vault"
	"github.com/duv0-x/scriba/internal/writer"
)

// newAutosave arma un Server con Writer (commit diferido de 1 h) y Presence sobre un vault
// git en TempDir con notes/hoy.md; es newEditor con presencia y WebIdle largo.
func newAutosave(t *testing.T) (http.Handler, string, *events.Presence) {
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
	pres := events.NewPresence(time.Minute, nil)
	k := &kbs.KB{V: v, Ix: ix, Presence: pres, W: writer.New(writer.Config{V: v, Ix: ix, WebIdle: time.Hour})}
	return (&Server{KBs: oneKB(k)}).Handler(), root, pres
}

// put manda un PUT same-origin con body y decodifica el JSON de la respuesta (vacío con 204).
func put(t *testing.T, h http.Handler, target, body string) (*http.Response, map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	var m map[string]string
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("respuesta no es JSON: %q", b)
		}
	}
	return res, m
}

func gitOut(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestAutosave200(t *testing.T) {
	h, root, _ := newAutosave(t)
	nuevo := "---\ntype: journal\n---\n\n# Hoy\n\nTexto nuevo.\n"
	res, m := put(t, h, "/k/principal/n/notes/hoy.md", `{"content":`+jsonStr(nuevo)+`,"base":"`+hash(editNote)+`"}`)
	if res.StatusCode != 200 {
		t.Fatalf("status %d: %v", res.StatusCode, m)
	}
	b, err := os.ReadFile(filepath.Join(root, "notes/hoy.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != nuevo {
		t.Errorf("contenido = %q", b)
	}
	if m["base"] != hash(string(b)) {
		t.Errorf("base = %q, quiero el hash del archivo %q", m["base"], hash(string(b)))
	}
	// El commit web es diferido: la nota sigue sin commit (sucia o untracked; el vault
	// de test no tiene commit inicial).
	if st := gitOut(t, root, "status", "--porcelain"); !strings.Contains(st, "notes") {
		t.Errorf("git status sin la nota pendiente: %q", st)
	}
	out, err := exec.Command("git", "-C", root, "log", "--oneline").CombinedOutput()
	if err == nil && strings.Contains(string(out), "web: actualiza") { // err = aún sin commits
		t.Errorf("ya hay commit web: %q", out)
	}
}

// jsonStr codifica s como literal JSON (entre comillas).
func jsonStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestAutosave409YForce(t *testing.T) {
	h, root, _ := newAutosave(t)
	abs := filepath.Join(root, "notes/hoy.md")
	agente := editNote + "\nLínea del agente.\n"
	if err := os.WriteFile(abs, []byte(agente), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"content":"Mi versión","base":"` + hash(editNote) + `"}`
	res, m := put(t, h, "/k/principal/n/notes/hoy.md", body)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status %d: %v", res.StatusCode, m)
	}
	if m["current"] != agente || m["base"] != hash(agente) {
		t.Errorf("409 = %v", m)
	}
	// Reenvío con force del hash que devolvió el 409: escribe lo mío.
	res, m = put(t, h, "/k/principal/n/notes/hoy.md", `{"content":"Mi versión","base":"`+hash(editNote)+`","force":"`+m["base"]+`"}`)
	if res.StatusCode != 200 {
		t.Fatalf("force: status %d: %v", res.StatusCode, m)
	}
	if b, _ := os.ReadFile(abs); string(b) != "Mi versión\n" {
		t.Errorf("force no escribió: %q", b)
	}
}

func TestAutosave423(t *testing.T) {
	h, root, pres := newAutosave(t)
	pres.Observe(events.Event{Kind: events.Changed, Path: "notes/hoy.md", Agent: "claude", At: time.Now()})
	res, m := put(t, h, "/k/principal/n/notes/hoy.md", `{"content":"x","base":"`+hash(editNote)+`"}`)
	if res.StatusCode != http.StatusLocked || m["agent"] != "claude" {
		t.Fatalf("status %d: %v", res.StatusCode, m)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "notes/hoy.md")); string(b) != editNote {
		t.Errorf("el archivo cambió: %q", b)
	}
}

func TestAutosave413(t *testing.T) {
	h, _, _ := newAutosave(t)
	res, m := put(t, h, "/k/principal/n/notes/hoy.md", `{"content":`+jsonStr(strings.Repeat("a", writer.MaxBytes+1))+`,"base":"`+hash(editNote)+`"}`)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d: %v", res.StatusCode, m)
	}
}

func TestAutosave400(t *testing.T) {
	h, root, _ := newAutosave(t)
	if res, _ := put(t, h, "/k/principal/n/notes/hoy.md", "{no json"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("JSON inválido: %d", res.StatusCode)
	}
	// node_modules la valida el Writer, no el lector.
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules/x.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, _ := put(t, h, "/k/principal/n/node_modules/x.md", `{"content":"x","base":""}`); res.StatusCode != http.StatusBadRequest {
		t.Errorf("node_modules: %d", res.StatusCode)
	}
	if res, _ := put(t, h, "/k/principal/n/..%2F..%2Fx.md", `{"content":"x","base":""}`); res.StatusCode != http.StatusBadRequest {
		t.Errorf("fuera del vault: %d", res.StatusCode)
	}
	if res, _ := put(t, h, "/k/principal/n/notes/no-existe.md", `{"content":"x","base":""}`); res.StatusCode != http.StatusNotFound {
		t.Errorf("inexistente: %d", res.StatusCode)
	}
}

func TestAutosaveFlush(t *testing.T) {
	h, root, _ := newAutosave(t)
	res, m := put(t, h, "/k/principal/n/notes/hoy.md?flush=1", `{"content":"uno\n","base":"`+hash(editNote)+`"}`)
	if res.StatusCode != 200 {
		t.Fatalf("status %d: %v", res.StatusCode, m)
	}
	if s := gitOut(t, root, "log", "-1", "--format=%s"); s != "web: actualiza notes/hoy.md" {
		t.Fatalf("primer commit = %q", s)
	}
	// Otro guardado y luego solo flush con el body vacío.
	res, m = put(t, h, "/k/principal/n/notes/hoy.md", `{"content":"dos\n","base":"`+m["base"]+`"}`)
	if res.StatusCode != 200 {
		t.Fatalf("segundo guardado: status %d: %v", res.StatusCode, m)
	}
	res, _ = put(t, h, "/k/principal/n/notes/hoy.md?flush=1", "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("flush vacío: status %d", res.StatusCode)
	}
	if n := gitOut(t, root, "log", "--oneline"); len(strings.Split(n, "\n")) != 2 {
		t.Fatalf("commits = %q, quiero 2", n)
	}
}

func TestLectorIncrustaEdicion(t *testing.T) {
	h, _, pres := newAutosave(t)
	_, body := get(t, h, "/k/principal/n/notes/hoy.md", nil)
	marker := `<script id="nota-editable" type="application/json">`
	if !strings.Contains(body, marker) {
		t.Fatalf("sin nota-editable:\n%s", body)
	}
	i := strings.Index(body, marker) + len(marker)
	j := strings.Index(body[i:], "</script>")
	if j < 0 {
		t.Fatal("nota-editable sin cerrar")
	}
	var edit struct {
		Raw   string `json:"raw"`
		Base  string `json:"base"`
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal([]byte(body[i:i+j]), &edit); err != nil {
		t.Fatalf("JSON incrustado inválido: %v", err)
	}
	if edit.Raw != editNote || edit.Base != hash(editNote) || edit.Agent != "" {
		t.Errorf("edit = %+v", edit)
	}

	// Sin Writer no hay datos de edición.
	sin, _ := newEditor(t, false)
	if _, body := get(t, sin, "/k/principal/n/notes/hoy.md", nil); strings.Contains(body, "nota-editable") {
		t.Error("sin Writer no debe incrustar nota-editable")
	}

	// Con presencia de un agente, va en el JSON.
	pres.Observe(events.Event{Kind: events.Changed, Path: "notes/hoy.md", Agent: "claude", At: time.Now()})
	_, body = get(t, h, "/k/principal/n/notes/hoy.md", nil)
	i = strings.Index(body, marker)
	if i < 0 {
		t.Fatal("sin nota-editable con presencia")
	}
	i += len(marker)
	j = strings.Index(body[i:], "</script>")
	if err := json.Unmarshal([]byte(body[i:i+j]), &edit); err != nil {
		t.Fatalf("JSON incrustado inválido: %v", err)
	}
	if edit.Agent != "claude" {
		t.Errorf("agent = %q, quiero claude", edit.Agent)
	}
}

func TestAutosaveSinWriter(t *testing.T) {
	h, _ := newEditor(t, false)
	res, m := put(t, h, "/k/principal/n/notes/hoy.md", `{"content":"x","base":""}`)
	if res.StatusCode != http.StatusNotFound || m["error"] != "esta base no se puede editar" {
		t.Fatalf("status %d: %v", res.StatusCode, m)
	}
}
