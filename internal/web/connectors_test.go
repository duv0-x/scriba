package web

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duv0-x/scriba/internal/github"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/secrets"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/vault"
)

// connect recorre el device flow hasta dejar el conector guardado.
func connect(t *testing.T, e *setupEnv) {
	t.Helper()
	if res, _ := post(t, e.h, "/conectores/github/iniciar", nil, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("iniciar = %d", res.StatusCode)
	}
	res, _ := get(t, e.h, "/conectores/github/estado", map[string]string{"HX-Request": "true"})
	if res.Header.Get("HX-Redirect") != "/conectores" {
		t.Fatalf("HX-Redirect = %q", res.Header.Get("HX-Redirect"))
	}
}

func TestDeviceFlow(t *testing.T) {
	e := newSetupServer(t)
	if res, _ := post(t, e.h, "/conectores/github/iniciar", nil, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("iniciar = %d", res.StatusCode)
	}
	_, body := get(t, e.h, "/conectores", nil)
	if !strings.Contains(body, "ABCD-1234") || strings.Contains(body, "DEVICE-SECRETO-123") {
		t.Fatal("la página debe tener el user_code y no el device_code")
	}
	if !strings.Contains(body, `hx-trigger="every 5s"`) || !strings.Contains(body, "scope repo") {
		t.Errorf("falta el sondeo o el aviso de scope:\n%s", body)
	}

	e.gh.pollErr = github.ErrPending
	res, body := get(t, e.h, "/conectores/github/estado", map[string]string{"HX-Request": "true"})
	if res.Header.Get("HX-Redirect") != "" || !strings.Contains(body, "ABCD-1234") || strings.Contains(body, "<html") {
		t.Fatalf("pendiente: redirect %q\n%s", res.Header.Get("HX-Redirect"), body)
	}
	e.gh.pollErr = github.ErrSlowDown
	if _, body := get(t, e.h, "/conectores/github/estado", nil); !strings.Contains(body, `hx-trigger="every 10s"`) {
		t.Errorf("slow_down debe sumar 5 s:\n%s", body)
	}

	e.gh.pollErr = nil
	res, _ = get(t, e.h, "/conectores/github/estado", nil)
	if res.Header.Get("HX-Redirect") != "/conectores" {
		t.Fatalf("éxito: HX-Redirect = %q", res.Header.Get("HX-Redirect"))
	}
	c, ok, err := e.srv.State.KBConnector("github", e.kb.Slug)
	if err != nil || !ok || c.Account != "duv0-x" {
		t.Fatalf("conector = %+v %v %v", c, ok, err)
	}
	if bytes.Contains(c.Secret, []byte(e.gh.token)) {
		t.Fatal("el secreto guardado contiene el token en claro")
	}
	if plain, err := secrets.Open(e.srv.Key, c.Secret); err != nil || string(plain) != e.gh.token {
		t.Fatalf("Open = %q %v", plain, err)
	}
	_, body = get(t, e.h, "/conectores", nil)
	if strings.Contains(body, e.gh.token) || !strings.Contains(body, "duv0-x/ia-kb") || !strings.Contains(body, "Importar este repositorio") {
		t.Errorf("conectado sin repo:\n%s", body)
	}
}

func TestDeviceFlowDenegado(t *testing.T) {
	e := newSetupServer(t)
	post(t, e.h, "/conectores/github/iniciar", nil, nil)
	e.gh.pollErr = github.ErrDenied
	_, body := get(t, e.h, "/conectores/github/estado", nil)
	if !strings.Contains(body, "Cancelaste") || !strings.Contains(body, "/conectores/github/iniciar") {
		t.Fatalf("denegado:\n%s", body)
	}
	if _, ok, _ := e.srv.State.KBConnector("github", e.kb.Slug); ok {
		t.Error("no debe guardar conector")
	}
}

func TestRepoImport(t *testing.T) {
	e := newSetupServer(t)
	connect(t, e)
	if res, _ := post(t, e.h, "/conectores/github/repo", url.Values{"kb": {e.kb.Slug}, "repo": {"x/../../y"}}, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("repo inválido = %d", res.StatusCode)
	}
	res, body := post(t, e.h, "/conectores/github/repo", url.Values{"kb": {e.kb.Slug}, "repo": {"duv0-x/ia-kb"}}, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/k/principal/" {
		t.Fatalf("status %d %q\n%s", res.StatusCode, res.Header.Get("Location"), body)
	}
	if e.git.cloneURL != "https://github.com/duv0-x/ia-kb.git" || e.git.cloneTok != e.gh.token {
		t.Fatalf("clone url %q, token por Cred: %v", e.git.cloneURL, e.git.cloneTok == e.gh.token)
	}
	if v, _, _ := e.srv.State.Setting("initialized"); v != "1" {
		t.Fatalf("initialized = %q", v)
	}
	if n, err := e.kb.Ix.NoteCount(); err != nil || n == 0 {
		t.Fatalf("índice tras importar = %d, %v", n, err)
	}
	if c, _, _ := e.srv.State.KBConnector("github", e.kb.Slug); c.Repo != "duv0-x/ia-kb" {
		t.Errorf("repo = %q", c.Repo)
	}
	if vaultEmpty(e.srv.KBs.Default().V.Root) {
		t.Error("el contenido clonado no llegó al vault")
	}
}

// TestPushPorKB: el repo/push de GitHub es por KB; tocar el de "a" no toca el de "b".
func TestPushPorKB(t *testing.T) {
	e := newSetupServer(t)
	e.kb.Slug = "a"
	for _, kb := range []string{"a", "b"} {
		if err := e.srv.State.SaveKBConnector(kb, state.Connector{Name: connGitHub, Push: true}); err != nil {
			t.Fatal(err)
		}
	}
	if res, _ := post(t, e.h, "/conectores/github/push", url.Values{"kb": {e.kb.Slug}, "push": {"0"}}, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("push = %d", res.StatusCode)
	}
	if c, _, _ := e.srv.State.KBConnector(connGitHub, "a"); c.Push {
		t.Error("push de a sigue activo")
	}
	if c, _, _ := e.srv.State.KBConnector(connGitHub, "b"); !c.Push {
		t.Error("push de b cambió")
	}
}

func TestPushYDesconectar(t *testing.T) {
	e := newSetupServer(t)
	connect(t, e)
	post(t, e.h, "/conectores/github/push", url.Values{"kb": {e.kb.Slug}, "push": {"0"}}, nil)
	if c, _, _ := e.srv.State.KBConnector("github", e.kb.Slug); c.Push {
		t.Error("push sigue activo")
	}
	res, _ := post(t, e.h, "/conectores/github/desconectar", nil, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/conectores" {
		t.Fatalf("desconectar = %d", res.StatusCode)
	}
	if _, ok, _ := e.srv.State.KBConnector("github", e.kb.Slug); ok {
		t.Fatal("el conector sigue guardado")
	}
}

// addKB añade a e una segunda KB con una nota y su propio vault (no vacío: se enlaza, no se importa).
func addKB(t *testing.T, e *setupEnv, slug string) *kbs.KB {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "vault")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nota.md"), []byte("# Nota\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	k := &kbs.KB{Slug: slug, V: v, Ix: ix}
	f := e.srv.KBs.(*fakeKBs)
	f.list = append(f.list, k)
	return k
}

// TestConectoresPorKB: la cuenta se conecta una vez; repo y push son de cada KB.
func TestConectoresPorKB(t *testing.T) {
	e := newSetupServer(t)
	addKB(t, e, "otra")
	connect(t, e)
	e.git.isRepo = true

	_, body := get(t, e.h, "/conectores", nil)
	if !strings.Contains(body, "<code>principal</code>") || !strings.Contains(body, "<code>otra</code>") || strings.Count(body, ">Desconectar<") != 1 {
		t.Fatalf("conectores con dos KBs:\n%s", body)
	}

	if res, body := post(t, e.h, "/conectores/github/repo", url.Values{"kb": {"otra"}, "repo": {"owner/otra"}}, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("enlazar otra = %d\n%s", res.StatusCode, body)
	}
	if c, _, _ := e.srv.State.KBConnector(connGitHub, "otra"); c.Repo != "owner/otra" || c.Account != "duv0-x" {
		t.Errorf("otra = %+v", c)
	}
	if c, _, _ := e.srv.State.KBConnector(connGitHub, testKB); c.Repo != "" {
		t.Errorf("principal recibió el repo de otra: %q", c.Repo)
	}

	post(t, e.h, "/conectores/github/push", url.Values{"kb": {"otra"}, "push": {"0"}}, nil)
	if c, _, _ := e.srv.State.KBConnector(connGitHub, "otra"); c.Push {
		t.Error("push de otra sigue activo")
	}
	if c, _, _ := e.srv.State.KBConnector(connGitHub, testKB); !c.Push {
		t.Error("push de principal cambió")
	}

	for _, p := range []string{"/conectores/github/repo", "/conectores/github/push"} {
		if res, _ := post(t, e.h, p, url.Values{"kb": {"noexiste"}, "repo": {"owner/x"}, "push": {"0"}}, nil); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s con kb inexistente = %d", p, res.StatusCode)
		}
	}
}
