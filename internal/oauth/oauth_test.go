package oauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/state"
)

const base = "https://kb.test"

var verifier = strings.Repeat("v", 50)

func challenge() string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type env struct {
	s  *Server
	h  *http.ServeMux
	ag *agents.Store
}

func setup(t *testing.T) *env {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ag := &agents.Store{S: st}
	s, err := New(bytes.Repeat([]byte{7}, 32), ag, base+"/")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	return &env{s, mux, ag}
}

func (e *env) do(t *testing.T, method, path, ctype, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	out := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s: %d %q", method, path, rec.Code, rec.Body)
	}
	return rec.Code, out
}

func (e *env) register(t *testing.T, uris ...string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"client_name": "Claude", "redirect_uris": uris})
	return e.do(t, "POST", registerPath, "application/json", string(b))
}

func (e *env) clientID(t *testing.T) string {
	t.Helper()
	code, out := e.register(t, ClaudeCallback)
	if code != http.StatusCreated {
		t.Fatalf("registro: %d %v", code, out)
	}
	return out["client_id"].(string)
}

func params(id string) url.Values {
	return url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {ClaudeCallback},
		"state": {"xyz"}, "code_challenge": {challenge()}, "code_challenge_method": {"S256"}, "resource": {base + "/mcp"}}
}

// code recorre el consentimiento aprobado y devuelve el authorization code.
func (e *env) code(t *testing.T, id, agent string) string {
	t.Helper()
	req, redirect, err := e.s.Parse(params(id), base)
	if err != nil || redirect != "" {
		t.Fatalf("parse: %q %v", redirect, err)
	}
	u, _ := url.Parse(e.s.Approve(req, agent))
	if u.Query().Get("state") != "xyz" {
		t.Fatalf("state: %s", u)
	}
	return u.Query().Get("code")
}

func (e *env) exchange(t *testing.T, id, code, ver string) (int, map[string]any) {
	t.Helper()
	return e.do(t, "POST", tokenPath, "application/x-www-form-urlencoded", url.Values{
		"grant_type": {"authorization_code"}, "client_id": {id}, "code": {code},
		"redirect_uri": {ClaudeCallback}, "code_verifier": {ver}, "resource": {base + "/mcp"}}.Encode())
}

func (e *env) refresh(t *testing.T, id, tok string) (int, map[string]any) {
	t.Helper()
	return e.do(t, "POST", tokenPath, "application/x-www-form-urlencoded", url.Values{
		"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {tok}}.Encode())
}

func wantError(t *testing.T, what string, code int, out map[string]any, want string) {
	t.Helper()
	if code/100 != 4 || out["error"] != want {
		t.Errorf("%s: %d %v, quería %s", what, code, out, want)
	}
}

func TestDiscovery(t *testing.T) {
	e := setup(t)
	for _, p := range []string{resourcePath, resourcePath + "/mcp"} {
		code, out := e.do(t, "GET", p, "", "")
		if code != 200 || out["resource"] != base+"/mcp" || out["authorization_servers"].([]any)[0] != base {
			t.Errorf("%s: %d %v", p, code, out)
		}
	}
	code, out := e.do(t, "GET", serverPath, "", "")
	if code != 200 || out["issuer"] != base || out["authorization_endpoint"] != base+AuthorizePath ||
		out["token_endpoint"] != base+tokenPath || out["registration_endpoint"] != base+registerPath ||
		out["code_challenge_methods_supported"].([]any)[0] != "S256" {
		t.Errorf("metadata: %d %v", code, out)
	}
}

func TestRegistro(t *testing.T) {
	e := setup(t)
	for _, uri := range []string{"https://evil.test/cb", "http://localhost.evil.test/cb", "http://u@localhost/cb", "http://localhost/cb#x", ""} {
		code, out := e.register(t, uri)
		wantError(t, uri, code, out, "invalid_redirect_uri")
	}
	for _, uri := range []string{ClaudeCallback, "http://localhost:3118/callback", "http://127.0.0.1/callback", "http://[::1]:9/cb"} {
		if code, out := e.register(t, uri); code != http.StatusCreated {
			t.Errorf("%s: %d %v", uri, code, out)
		}
	}
	_, out := e.register(t, "https://evil.test/cb", ClaudeCallback)
	if got := out["redirect_uris"].([]any); len(got) != 1 || got[0] != ClaudeCallback {
		t.Errorf("recorte: %v", got)
	}
}

func TestParseInvalido(t *testing.T) {
	e := setup(t)
	id := e.clientID(t)
	for name, change := range map[string][2]string{
		"cliente": {"client_id", "inventado"}, "redirect": {"redirect_uri", "https://evil.test/cb"},
		"loopback no registrado": {"redirect_uri", "http://localhost/cb"},
	} {
		v := params(id)
		v.Set(change[0], change[1])
		if _, redirect, err := e.s.Parse(v, base); err == nil || redirect != "" {
			t.Errorf("%s: redirect=%q err=%v", name, redirect, err)
		}
	}
	for want, change := range map[string][2]string{
		"unsupported_response_type": {"response_type", "token"}, "invalid_request": {"code_challenge_method", "plain"},
		"invalid_target": {"resource", "https://otro.test/mcp"},
	} {
		v := params(id)
		v.Set(change[0], change[1])
		if _, redirect, err := e.s.Parse(v, base); err != nil || !strings.Contains(redirect, "error="+want) {
			t.Errorf("%s: redirect=%q err=%v", want, redirect, err)
		}
	}
	if !matches("http://localhost/callback", "http://localhost:5000/callback") || matches("http://localhost/callback", "http://localhost:5000/otro") {
		t.Error("matches en loopback")
	}
}

func TestFlujoPKCEYRotacion(t *testing.T) {
	e := setup(t)
	id := e.clientID(t)

	code := e.code(t, id, "claude")
	status, out := e.exchange(t, id, code, strings.Repeat("x", 50))
	wantError(t, "verifier malo", status, out, "invalid_grant")
	status, out = e.exchange(t, id, code, verifier)
	wantError(t, "code ya consumido", status, out, "invalid_grant")

	status, first := e.exchange(t, id, e.code(t, id, "claude"), verifier)
	if status != 200 || first["token_type"] != "Bearer" || first["expires_in"] != float64(3600) {
		t.Fatalf("token: %d %v", status, first)
	}
	if name, ok := e.ag.Verify(first["access_token"].(string)); !ok || name != "claude" {
		t.Fatalf("verify: %q %v", name, ok)
	}

	status, out = e.refresh(t, e.clientID(t), first["refresh_token"].(string))
	wantError(t, "refresh de otro cliente", status, out, "invalid_grant")
	status, second := e.refresh(t, id, first["refresh_token"].(string))
	if status != 200 || second["refresh_token"] == first["refresh_token"] {
		t.Fatalf("refresh: %d %v", status, second)
	}
	if _, ok := e.ag.Verify(first["access_token"].(string)); ok {
		t.Error("el access anterior sigue valiendo")
	}
	if _, ok := e.ag.Verify(second["access_token"].(string)); !ok {
		t.Error("el access nuevo no vale")
	}

	status, out = e.refresh(t, id, first["refresh_token"].(string))
	wantError(t, "reutilización", status, out, "invalid_grant")
	if _, ok := e.ag.Verify(second["access_token"].(string)); ok {
		t.Error("la reutilización no revocó la concesión")
	}
	status, out = e.refresh(t, id, second["refresh_token"].(string))
	wantError(t, "tras revocar", status, out, "invalid_grant")
}

func TestRevocacionYReconexion(t *testing.T) {
	e := setup(t)
	id := e.clientID(t)
	_, first := e.exchange(t, id, e.code(t, id, "claude"), verifier)
	_, second := e.exchange(t, id, e.code(t, id, "claude"), verifier) // reconectar reemplaza
	if _, ok := e.ag.Verify(first["access_token"].(string)); ok {
		t.Error("la concesión reemplazada sigue valiendo")
	}
	if ok, err := e.ag.Revoke("claude"); !ok || err != nil {
		t.Fatalf("revocar: %v %v", ok, err)
	}
	if _, ok := e.ag.Verify(second["access_token"].(string)); ok {
		t.Error("revocado y sigue valiendo")
	}
	status, out := e.refresh(t, id, second["refresh_token"].(string))
	wantError(t, "refresh revocado", status, out, "invalid_grant")
}

func TestNoPisaTokenEstatico(t *testing.T) {
	e := setup(t)
	static, err := e.ag.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	id := e.clientID(t)
	status, out := e.exchange(t, id, e.code(t, id, "claude"), verifier)
	wantError(t, "nombre estático", status, out, "invalid_grant")
	if _, ok := e.ag.Verify(static); !ok {
		t.Error("el token estático dejó de valer")
	}
}

func TestCodeCaducadoYClienteMalo(t *testing.T) {
	e := setup(t)
	id := e.clientID(t)
	e.s.now = func() time.Time { return time.Now().Add(-codeTTL - time.Second) }
	code := e.code(t, id, "claude")
	e.s.now = time.Now
	status, out := e.exchange(t, id, code, verifier)
	wantError(t, "code caducado", status, out, "invalid_grant")
	status, out = e.exchange(t, "inventado", e.code(t, id, "claude"), verifier)
	wantError(t, "cliente malo", status, out, "invalid_client")
}
