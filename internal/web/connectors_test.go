package web

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/duv0-x/scriba/internal/github"
	"github.com/duv0-x/scriba/internal/secrets"
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
	c, ok, err := e.srv.State.Connector("github")
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
	if _, ok, _ := e.srv.State.Connector("github"); ok {
		t.Error("no debe guardar conector")
	}
}

func TestRepoImport(t *testing.T) {
	e := newSetupServer(t)
	connect(t, e)
	if res, _ := post(t, e.h, "/conectores/github/repo", url.Values{"repo": {"x/../../y"}}, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("repo inválido = %d", res.StatusCode)
	}
	res, body := post(t, e.h, "/conectores/github/repo", url.Values{"repo": {"duv0-x/ia-kb"}}, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Fatalf("status %d %q\n%s", res.StatusCode, res.Header.Get("Location"), body)
	}
	if e.git.cloneURL != "https://github.com/duv0-x/ia-kb.git" || e.git.cloneTok != e.gh.token {
		t.Fatalf("clone url %q, token por Cred: %v", e.git.cloneURL, e.git.cloneTok == e.gh.token)
	}
	if v, _, _ := e.srv.State.Setting("initialized"); v != "1" || e.imported != 1 {
		t.Fatalf("initialized = %q, OnImported = %d", v, e.imported)
	}
	if c, _, _ := e.srv.State.Connector("github"); c.Repo != "duv0-x/ia-kb" {
		t.Errorf("repo = %q", c.Repo)
	}
	if e.srv.vaultEmpty() {
		t.Error("el contenido clonado no llegó al vault")
	}
}

func TestPushYDesconectar(t *testing.T) {
	e := newSetupServer(t)
	connect(t, e)
	post(t, e.h, "/conectores/github/push", url.Values{"push": {"0"}}, nil)
	if c, _, _ := e.srv.State.Connector("github"); c.Push {
		t.Error("push sigue activo")
	}
	res, _ := post(t, e.h, "/conectores/github/desconectar", nil, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/conectores" {
		t.Fatalf("desconectar = %d", res.StatusCode)
	}
	if _, ok, _ := e.srv.State.Connector("github"); ok {
		t.Fatal("el conector sigue guardado")
	}
}
