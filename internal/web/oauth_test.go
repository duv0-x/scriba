package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/duv0-x/scriba/internal/oauth"
)

// newOAuthServer es newAgentsServer con OAuth y un cliente registrado; devuelve sus parámetros.
func newOAuthServer(t *testing.T) (*setupEnv, url.Values) {
	t.Helper()
	e := newAgentsServer(t)
	az, err := oauth.New(e.srv.Key, e.srv.Agents, e.srv.PublicURL)
	if err != nil {
		t.Fatal(err)
	}
	e.srv.OAuth = az
	e.h = e.srv.Handler()
	mux := http.NewServeMux()
	az.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/oauth/register",
		strings.NewReader(`{"client_name":"Claude <b>","redirect_uris":["`+oauth.ClaudeCallback+`"]}`)))
	var out struct {
		ID string `json:"client_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == "" {
		t.Fatalf("registro: %d %s", rec.Code, rec.Body)
	}
	return e, url.Values{"response_type": {"code"}, "client_id": {out.ID}, "redirect_uri": {oauth.ClaudeCallback},
		"state": {"xyz"}, "code_challenge": {strings.Repeat("A", 43)}, "code_challenge_method": {"S256"}}
}

func with(v url.Values, kv ...string) url.Values {
	out := url.Values{}
	for k, x := range v {
		out[k] = x
	}
	for i := 0; i < len(kv); i += 2 {
		out.Set(kv[i], kv[i+1])
	}
	return out
}

func TestConsentimiento(t *testing.T) {
	e, p := newOAuthServer(t)
	res, body := get(t, e.h, "/oauth/authorize?"+p.Encode(), nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Claude &lt;b&gt;") || !strings.Contains(body, "claude.ai") {
		t.Fatalf("GET: %d\n%s", res.StatusCode, body)
	}
	if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("cabeceras: %v", res.Header)
	}

	approve := with(p, "agent", "claude", "decision", "approve")
	if res, _ := post(t, e.h, "/oauth/authorize", approve, map[string]string{"Sec-Fetch-Site": "cross-site"}); res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site = %d", res.StatusCode)
	}
	res, _ = post(t, e.h, "/oauth/authorize", approve, nil)
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, oauth.ClaudeCallback+"?code=") || !strings.Contains(loc, "state=xyz") {
		t.Errorf("aprobar: %d %q", res.StatusCode, loc)
	}
	res, _ = post(t, e.h, "/oauth/authorize", with(p, "decision", "deny"), nil)
	if loc := res.Header.Get("Location"); !strings.Contains(loc, "error=access_denied") {
		t.Errorf("rechazar: %q", loc)
	}
	if res, _ := post(t, e.h, "/oauth/authorize", with(approve, "agent", "Mal Nombre"), nil); res.StatusCode != http.StatusBadRequest || res.Header.Get("Location") != "" {
		t.Errorf("nombre inválido: %d", res.StatusCode)
	}
	if _, err := e.srv.Agents.Create("estatico"); err != nil {
		t.Fatal(err)
	}
	if res, _ := post(t, e.h, "/oauth/authorize", with(approve, "agent", "estatico"), nil); res.StatusCode != http.StatusConflict || res.Header.Get("Location") != "" {
		t.Errorf("nombre estático: %d", res.StatusCode)
	}
}

func TestConsentimientoInvalido(t *testing.T) {
	e, p := newOAuthServer(t)
	res, body := get(t, e.h, "/oauth/authorize?"+with(p, "redirect_uri", "https://evil.test/cb").Encode(), nil)
	if res.StatusCode != http.StatusBadRequest || res.Header.Get("Location") != "" || strings.Contains(body, "Aprobar</button>") {
		t.Errorf("redirect ajeno: %d\n%s", res.StatusCode, body)
	}
	res, _ = get(t, e.h, "/oauth/authorize?"+with(p, "code_challenge_method", "plain").Encode(), nil)
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "error=invalid_request") {
		t.Errorf("sin S256: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestAgentesMuestraConector(t *testing.T) {
	e, p := newOAuthServer(t)
	if _, err := e.srv.Agents.Grant("claude-web", p.Get("client_id")); err != nil {
		t.Fatal(err)
	}
	if _, body := get(t, e.h, "/agentes", nil); !strings.Contains(body, ">conector</span>") {
		t.Errorf("falta la marca de conector:\n%s", body)
	}
}
