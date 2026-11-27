package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/vault"
	"github.com/duv0-x/scriba/internal/writer"
)

// fakeAgents acepta tokens fijos (token → [agente, KB]) y permite revocarlos.
type fakeAgents struct {
	mu sync.Mutex
	m  map[string][2]string
}

func (f *fakeAgents) Verify(t string) (string, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.m[t]
	return a[0], a[1], ok
}

func (f *fakeAgents) revoke(t string) { f.mu.Lock(); delete(f.m, t); f.mu.Unlock() }

// fakeKBs es el Registry: KBs por slug, que se pueden quitar (borrado).
type fakeKBs struct {
	mu sync.Mutex
	m  map[string]*kbs.KB
}

func (f *fakeKBs) Get(slug string) (*kbs.KB, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.m[slug]
	return k, ok
}

func (f *fakeKBs) remove(slug string) { f.mu.Lock(); delete(f.m, slug); f.mu.Unlock() }

type env struct {
	srv    *httptest.Server
	root   string // vault de la KB "ia-kb" (tok-a, tok-b)
	rootB  string // vault de la KB "b" (tok-x)
	agents *fakeAgents
	kbs    *fakeKBs
}

func newKB(t *testing.T, slug string) *kbs.KB {
	t.Helper()
	root := t.TempDir()
	if err := gitvault.Init(root); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return &kbs.KB{Slug: slug, V: v, Ix: ix, W: writer.New(writer.Config{V: v, Ix: ix})}
}

func setup(t *testing.T, publicURL string) *env {
	t.Helper()
	a, b := newKB(t, "ia-kb"), newKB(t, "b")
	agents := &fakeAgents{m: map[string][2]string{"tok-a": {"claude", "ia-kb"}, "tok-b": {"codex", "ia-kb"}, "tok-x": {"otro", "b"}}}
	reg := &fakeKBs{m: map[string]*kbs.KB{"ia-kb": a, "b": b}}
	h := Handler(Config{
		KBs:       reg,
		Agents:    agents,
		PublicURL: publicURL,
		Now:       func() time.Time { return time.Date(2026, 11, 16, 15, 0, 0, 0, time.UTC) },
		Version:   "test",
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{srv: srv, root: a.V.Root, rootB: b.V.Root, agents: agents, kbs: reg}
}

// bearer añade el token (y opcionalmente Host y cabeceras) a cada petición del cliente MCP.
type bearer struct {
	token string
	host  string
	hdr   map[string]string
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	if b.host != "" {
		r.Host = b.host
	}
	for k, v := range b.hdr {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (e *env) connect(t *testing.T, token string) *sdk.ClientSession {
	return e.connectWith(t, bearer{token: token})
}

func (e *env) connectWith(t *testing.T, b bearer) *sdk.ClientSession {
	t.Helper()
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: e.srv.URL, HTTPClient: &http.Client{Transport: b}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call invoca una tool y devuelve el resultado estructurado decodificado en out.
func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any, out any) *sdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return res
}

func text(r *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestUnauthorized(t *testing.T) {
	e := setup(t, "http://scriba.test/")
	for name, auth := range map[string]string{"sin token": "", "token malo": "Bearer nope", "otro esquema": "Basic tok-a"} {
		t.Run(name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", e.srv.URL, strings.NewReader(`{}`))
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var body map[string]string
			json.NewDecoder(resp.Body).Decode(&body)
			want := `Bearer resource_metadata="http://scriba.test/.well-known/oauth-protected-resource/mcp"`
			if name == "token malo" {
				want += `, error="invalid_token"`
			}
			if resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") != want || body["error"] != "token inválido" {
				t.Fatalf("status %d, www-auth %q, body %v", resp.StatusCode, resp.Header.Get("WWW-Authenticate"), body)
			}
		})
	}
}

func TestTools(t *testing.T) {
	e := setup(t, "http://scriba.test/")
	cs := e.connect(t, "tok-a")

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	slices.Sort(names)
	want := []string{"kb_append", "kb_backlinks", "kb_list", "kb_log_journal", "kb_open", "kb_read", "kb_search", "kb_write"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v", names)
	}

	content := "---\ntitle: Prueba de Scriba\ntype: decisions\n---\n\n# Prueba de Scriba\n\nLa zanahoria es naranja.\n"
	var w writeOut
	r := call(t, cs, "kb_write", map[string]any{"path": "decisions/prueba.md", "content": content}, &w)
	if r.IsError || !w.Created || w.Commit == "" || w.URL != "http://scriba.test/k/ia-kb/n/decisions/prueba.md" {
		t.Fatalf("kb_write: %+v %s", w, text(r))
	}
	if an := strings.TrimSpace(git(t, e.root, "log", "-1", "--format=%an")); an != "claude via Scriba" {
		t.Fatalf("autor = %q", an)
	}

	var s searchOut
	call(t, cs, "kb_search", map[string]any{"query": "zanahoria"}, &s)
	if len(s.Hits) != 1 || s.Hits[0].Path != "decisions/prueba.md" || strings.Contains(s.Hits[0].Snippet, "<mark>") ||
		!strings.Contains(s.Hits[0].Snippet, "zanahoria") {
		t.Fatalf("kb_search: %+v", s)
	}
	if r := call(t, cs, "kb_search", map[string]any{"query": "x", "limit": 51}, nil); !r.IsError {
		t.Fatal("limit 51 debería fallar")
	}

	var rd readOut
	call(t, cs, "kb_read", map[string]any{"path": "decisions/prueba.md"}, &rd)
	if rd.Content != content || rd.Title != "Prueba de Scriba" || rd.Type != "decisions" {
		t.Fatalf("kb_read: %+v", rd)
	}
	if r := call(t, cs, "kb_read", map[string]any{"path": "decisions/no.md"}, nil); !r.IsError || !strings.Contains(text(r), "no existe") {
		t.Fatalf("kb_read inexistente: %s", text(r))
	}

	var ls listOut
	call(t, cs, "kb_list", map[string]any{"folder": "decisions"}, &ls)
	if len(ls.Notes) != 1 || ls.Notes[0].Path != "decisions/prueba.md" {
		t.Fatalf("kb_list: %+v", ls)
	}

	var op openOut
	call(t, cs, "kb_open", map[string]any{"path": "decisions/prueba.md"}, &op)
	if op.URL != "http://scriba.test/k/ia-kb/n/decisions/prueba.md" {
		t.Fatalf("kb_open: %+v", op)
	}

	r = call(t, cs, "kb_write", map[string]any{"path": "../x.md", "content": "x"}, nil)
	if !r.IsError || !strings.Contains(text(r), "ruta no permitida") {
		t.Fatalf("kb_write ../x.md: %v %s", r.IsError, text(r))
	}

	call(t, cs, "kb_append", map[string]any{"path": "decisions/prueba.md", "content": "Ver [[prueba]]."}, &w)
	if w.Created || w.Commit == "" {
		t.Fatalf("kb_append: %+v", w)
	}

	call(t, cs, "kb_log_journal", map[string]any{"title": "Sesión", "body": "Probé el MCP."}, &w)
	if w.Path != "journal/2026-11-16.md" || !w.Created {
		t.Fatalf("kb_log_journal: %+v", w)
	}
	if b, err := os.ReadFile(filepath.Join(e.root, "journal", "2026-11-16.md")); err != nil || !strings.Contains(string(b), "## Sesión") {
		t.Fatalf("journal: %v %s", err, b)
	}
}

func TestConcurrentAgents(t *testing.T) {
	e := setup(t, "http://scriba.test/")
	sessions := map[string]*sdk.ClientSession{"claude": e.connect(t, "tok-a"), "codex": e.connect(t, "tok-b")}
	var wg sync.WaitGroup
	for agent, cs := range sessions {
		for i := range 3 {
			wg.Go(func() {
				p := "drafts/" + agent + "-" + string(rune('0'+i)) + ".md"
				res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "kb_write",
					Arguments: map[string]any{"path": p, "content": "# " + p}})
				if err != nil || res.IsError {
					t.Errorf("%s: %v %s", p, err, text(res))
				}
			})
		}
	}
	wg.Wait()
	// Cada commit debe llevar el agente dueño del archivo que crea.
	out := git(t, e.root, "log", "--name-only", "--format=%an")
	var author string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasSuffix(l, " via Scriba"):
			author = strings.TrimSuffix(l, " via Scriba")
		case strings.HasPrefix(l, "drafts/"):
			if !strings.HasPrefix(l, "drafts/"+author+"-") {
				t.Errorf("%s commiteado por %s", l, author)
			}
		}
	}
	if n := strings.Count(out, " via Scriba"); n != 6 {
		t.Fatalf("commits = %d\n%s", n, out)
	}
}

func TestURLBase(t *testing.T) {
	note := "---\ntitle: U\n---\n"
	cases := []struct {
		name, publicURL, host, proto, want string
	}{
		{"host con https del proxy", "", "kb.ejemplo.test", "https", "https://kb.ejemplo.test"},
		{"host sin cabecera", "", "", "", ""}, // want = URL de httptest
		{"proto no exacto", "", "kb.ejemplo.test", "HTTPS", "http://kb.ejemplo.test"},
		{"PublicURL manda sobre el Host", "https://scriba.test/", "otro.test", "", "https://scriba.test"},
		{"host malicioso", "", "evil.test/<script>", "https", "http://127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, c.publicURL)
			if c.want == "" {
				c.want = e.srv.URL
			}
			b := bearer{token: "tok-a", host: c.host}
			if c.proto != "" {
				b.hdr = map[string]string{"X-Forwarded-Proto": c.proto}
			}
			cs := e.connectWith(t, b)
			var w writeOut
			if r := call(t, cs, "kb_write", map[string]any{"path": "a/u.md", "content": note}, &w); r.IsError {
				t.Fatal(text(r))
			}
			var op openOut
			call(t, cs, "kb_open", map[string]any{"path": "a/u.md"}, &op)
			if want := c.want + "/k/ia-kb/n/a/u.md"; w.URL != want || op.URL != want {
				t.Fatalf("kb_write %q, kb_open %q, quería %q", w.URL, op.URL, want)
			}
		})
	}
}

// rpc manda un mensaje JSON-RPC crudo al endpoint MCP.
func (e *env) rpc(t *testing.T, token, session, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
		req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// initSession abre una sesión MCP con token y devuelve su Mcp-Session-Id.
func (e *env) initSession(t *testing.T, token string) string {
	t.Helper()
	resp := e.rpc(t, token, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"crudo","version":"0"}}}`)
	sid := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != 200 || sid == "" {
		t.Fatalf("initialize: %d, sesión %q", resp.StatusCode, sid)
	}
	if r := e.rpc(t, token, sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); r.StatusCode/100 != 2 {
		t.Fatalf("initialized: %d", r.StatusCode)
	}
	return sid
}

const writeCall = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kb_write","arguments":{"path":"drafts/x.md","content":"# x"}}}`

func (e *env) assertNoWrite(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(e.root, "drafts", "x.md")); !os.IsNotExist(err) {
		t.Fatalf("se creó drafts/x.md (err %v)", err)
	}
}

func TestSessionHijack(t *testing.T) {
	e := setup(t, "http://scriba.test/")
	sid := e.initSession(t, "tok-a")
	// B tiene un token válido pero la sesión es de A: el SDK la rechaza.
	if resp := e.rpc(t, "tok-b", sid, writeCall); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, quería 403", resp.StatusCode)
	}
	e.assertNoWrite(t)
	// Control: con el token de A la misma llamada sí escribe.
	resp := e.rpc(t, "tok-a", sid, writeCall)
	body, _ := io.ReadAll(resp.Body) // la respuesta (SSE) termina tras el resultado de la tool
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"commit"`) {
		t.Fatalf("control: status %d, %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(e.root, "drafts", "x.md")); err != nil {
		t.Fatalf("control: %v", err)
	}
}

func TestRevokedMidSession(t *testing.T) {
	e := setup(t, "http://scriba.test/")
	sid := e.initSession(t, "tok-a")
	e.agents.revoke("tok-a")
	if resp := e.rpc(t, "tok-a", sid, writeCall); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, quería 401", resp.StatusCode)
	}
	e.assertNoWrite(t)
}

// Un token solo ve y escribe su KB: kb_search, kb_list y kb_read no cruzan, y kb_write va a su vault.
func TestAislamientoEntreKBs(t *testing.T) {
	e := setup(t, "http://scriba.test/")
	a, x := e.connect(t, "tok-a"), e.connect(t, "tok-x")
	note := func(title string) string {
		return "---\ntitle: " + title + "\ntype: decision\n---\n\npalabraunica " + title + "\n"
	}
	var wa, wb writeOut
	call(t, a, "kb_write", map[string]any{"path": "solo-a.md", "content": note("A")}, &wa)
	call(t, x, "kb_write", map[string]any{"path": "solo-b.md", "content": note("B")}, &wb)
	if wa.URL != "http://scriba.test/k/ia-kb/n/solo-a.md" || wb.URL != "http://scriba.test/k/b/n/solo-b.md" {
		t.Fatalf("URLs: %q %q", wa.URL, wb.URL)
	}
	if _, err := os.Stat(filepath.Join(e.root, "solo-a.md")); err != nil {
		t.Fatalf("kb_write de a no quedó en su vault: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "solo-b.md")); !os.IsNotExist(err) {
		t.Fatalf("kb_write de b quedó en el vault de a: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.rootB, "solo-b.md")); err != nil {
		t.Fatalf("kb_write de b no quedó en su vault: %v", err)
	}
	for _, c := range []struct {
		cs         *sdk.ClientSession
		own, other string
	}{{a, "solo-a.md", "solo-b.md"}, {x, "solo-b.md", "solo-a.md"}} {
		var so searchOut
		call(t, c.cs, "kb_search", map[string]any{"query": "palabraunica"}, &so)
		if len(so.Hits) != 1 || so.Hits[0].Path != c.own {
			t.Fatalf("kb_search: %+v (quería solo %s)", so.Hits, c.own)
		}
		var lo listOut
		call(t, c.cs, "kb_list", nil, &lo)
		if len(lo.Notes) != 1 || lo.Notes[0].Path != c.own {
			t.Fatalf("kb_list: %+v (quería solo %s)", lo.Notes, c.own)
		}
		if r := call(t, c.cs, "kb_read", map[string]any{"path": c.other}, nil); !r.IsError {
			t.Fatalf("kb_read leyó %s de la otra KB", c.other)
		}
	}
}

// Al borrar una KB, sus tokens responden 401 aunque tengan una sesión abierta.
func TestKBBorrada(t *testing.T) {
	e := setup(t, "http://scriba.test/")
	sid := e.initSession(t, "tok-x")
	e.kbs.remove("b")
	if resp := e.rpc(t, "tok-x", sid, writeCall); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, quería 401", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.rootB, "drafts", "x.md")); !os.IsNotExist(err) {
		t.Fatalf("escribió en la KB borrada: %v", err)
	}
}

func TestReadTemplate(t *testing.T) {
	e := setup(t, "http://scriba.test/")
	cs := e.connect(t, "tok-a")
	tpl := filepath.Join(e.root, "_templates")
	if err := os.MkdirAll(filepath.Join(tpl, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(tpl, "adr.md"), []byte("# ADR-NNN\n"), 0o644)
	os.WriteFile(filepath.Join(tpl, "sub", "x.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(e.root, "fuera.md"), []byte("secreto"), 0o644)
	os.Symlink(filepath.Join(e.root, "fuera.md"), filepath.Join(tpl, "link.md"))

	var rd readOut
	if r := call(t, cs, "kb_read", map[string]any{"path": "_templates/adr.md"}, &rd); r.IsError || rd.Content != "# ADR-NNN\n" {
		t.Fatalf("kb_read plantilla: %+v %s", rd, text(r))
	}
	for _, p := range []string{"_templates/sub/x.md", "_templates/../fuera.md", "_templates/.oculto.md", "_templates/link.md", "_templates/no.md", "_templates/adr.txt"} {
		if r := call(t, cs, "kb_read", map[string]any{"path": p}, nil); !r.IsError {
			t.Fatalf("kb_read %s debería fallar", p)
		}
	}
	if r := call(t, cs, "kb_write", map[string]any{"path": "_templates/adr.md", "content": "x"}, nil); !r.IsError {
		t.Fatal("kb_write en _templates debería fallar")
	}
}
