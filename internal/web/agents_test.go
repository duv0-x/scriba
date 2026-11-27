package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/kbs"
)

// newAgentsServer es newSetupServer con agentes y la base ya inicializada (si no, gate manda a /bienvenida).
func newAgentsServer(t *testing.T) *setupEnv {
	t.Helper()
	e := newSetupServer(t)
	if err := e.srv.State.SetSetting("initialized", "1"); err != nil {
		t.Fatal(err)
	}
	e.srv.Agents = &agents.Store{S: e.srv.State}
	e.srv.PublicURL = "https://kb.example.com"
	e.h = e.srv.Handler()
	return e
}

var tokenRe = regexp.MustCompile(`scb_[0-9A-Za-z]{43}`)

func TestAgentes(t *testing.T) {
	e := newAgentsServer(t)
	res, body := get(t, e.h, "/agentes", nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Todavía no hay agentes") || !strings.Contains(body, `href="/agentes" class="on"`) {
		t.Fatalf("lista vacía: %d\n%s", res.StatusCode, body)
	}

	res, body = post(t, e.h, "/agentes", url.Values{"name": {"claude-code-mac"}}, nil)
	tok := tokenRe.FindString(body)
	if res.StatusCode != http.StatusOK || tok == "" {
		t.Fatalf("crear: %d sin token\n%s", res.StatusCode, body)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", res.Header.Get("Cache-Control"))
	}
	want := `claude mcp add --transport http scriba https://kb.example.com/mcp --header &#34;Authorization: Bearer ` + tok
	if !strings.Contains(body, want) || !strings.Contains(body, "no se vuelve a mostrar") {
		t.Errorf("falta el comando o el aviso:\n%s", body)
	}

	_, body = get(t, e.h, "/agentes", nil)
	if strings.Contains(body, tok) || !strings.Contains(body, "claude-code-mac") || !strings.Contains(body, "nunca") {
		t.Fatalf("el GET posterior no debe mostrar el token:\n%s", body)
	}

	if res, body := post(t, e.h, "/agentes", url.Values{"name": {"Mal Nombre"}}, nil); res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "solo admite minúsculas") {
		t.Errorf("nombre inválido: %d\n%s", res.StatusCode, body)
	}
	if res, body := post(t, e.h, "/agentes", url.Values{"name": {"claude-code-mac"}}, nil); res.StatusCode != http.StatusConflict || !strings.Contains(body, "Ya hay un agente activo") || tokenRe.MatchString(body) {
		t.Errorf("nombre repetido: %d\n%s", res.StatusCode, body)
	}

	res, _ = post(t, e.h, "/agentes/revocar", url.Values{"name": {"claude-code-mac"}}, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("revocar = %d", res.StatusCode)
	}
	if _, body := get(t, e.h, "/agentes", nil); !strings.Contains(body, "revocado") || strings.Contains(body, "Revocar</button>") {
		t.Errorf("tras revocar:\n%s", body)
	}
	if _, _, ok := e.srv.Agents.Verify(tok); ok {
		t.Error("el token revocado sigue siendo válido")
	}
}

func TestAgentesSinMismoOrigen(t *testing.T) {
	e := newAgentsServer(t)
	cross := map[string]string{"Sec-Fetch-Site": "cross-site"}
	for _, p := range []string{"/agentes", "/agentes/revocar"} {
		if res, _ := post(t, e.h, p, url.Values{"name": {"x"}}, cross); res.StatusCode != http.StatusForbidden {
			t.Errorf("%s cross-site = %d", p, res.StatusCode)
		}
	}
	if toks, _ := e.srv.Agents.List(); len(toks) != 0 {
		t.Errorf("no debe crear tokens: %v", toks)
	}
}

func TestPublicURLDesdePeticion(t *testing.T) {
	e := newAgentsServer(t)
	e.srv.PublicURL = ""
	_, body := post(t, e.h, "/agentes", url.Values{"name": {"a"}}, map[string]string{"Sec-Fetch-Site": "same-origin", "X-Forwarded-Proto": "https"})
	if !strings.Contains(body, "scriba https://example.com/mcp") {
		t.Errorf("URL deducida del host:\n%s", body)
	}
}

func TestUltimoPush(t *testing.T) {
	e := newSetupServer(t)
	connect(t, e)
	c, _, _ := e.srv.State.KBConnector("github", e.kb.Slug)
	c.Repo = "duv0-x/ia-kb"
	if err := e.srv.State.SaveKBConnector(e.kb.Slug, c); err != nil {
		t.Fatal(err)
	}
	if _, body := get(t, e.h, "/conectores", nil); !strings.Contains(body, "Último push") || !strings.Contains(body, "aún no") || strings.Contains(body, "Se activa cuando") {
		t.Fatalf("sin push:\n%s", body)
	}
	c.LastPush, c.LastError = "2026-11-16 10:00", "rechazado por el remoto"
	if err := e.srv.State.SaveKBConnector(e.kb.Slug, c); err != nil {
		t.Fatal(err)
	}
	if _, body := get(t, e.h, "/conectores", nil); !strings.Contains(body, "2026-11-16 10:00") || !strings.Contains(body, `class="bad">rechazado por el remoto`) {
		t.Errorf("con push y error:\n%s", body)
	}
}

// Al crear un token se elige la KB; la lista la muestra y una KB inexistente es 400.
func TestAgentesPorKB(t *testing.T) {
	e := newAgentsServer(t)
	reg := e.srv.KBs.(*fakeKBs)
	reg.list = append(reg.list, &kbs.KB{Slug: "trabajo", V: e.kb.V, Ix: e.kb.Ix})

	if _, body := get(t, e.h, "/agentes", nil); !strings.Contains(body, `<option value="trabajo">`) || !strings.Contains(body, `<option value="principal" selected>`) {
		t.Fatalf("selector de KB:\n%s", body)
	}
	res, body := post(t, e.h, "/agentes", url.Values{"name": {"codex"}, "kb": {"trabajo"}}, nil)
	tok := tokenRe.FindString(body)
	if res.StatusCode != http.StatusOK || tok == "" {
		t.Fatalf("crear: %d\n%s", res.StatusCode, body)
	}
	if _, kb, ok := e.srv.Agents.Verify(tok); !ok || kb != "trabajo" {
		t.Fatalf("KB del token = %q %v", kb, ok)
	}
	if _, body := get(t, e.h, "/agentes", nil); !strings.Contains(body, "<td><code>trabajo</code></td>") {
		t.Errorf("la lista no muestra la KB:\n%s", body)
	}
	if res, body := post(t, e.h, "/agentes", url.Values{"name": {"otro"}, "kb": {"nope"}}, nil); res.StatusCode != http.StatusBadRequest || tokenRe.MatchString(body) {
		t.Errorf("KB inexistente: %d", res.StatusCode)
	}
}
