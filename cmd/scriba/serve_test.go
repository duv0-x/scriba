package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/secrets"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/vault"
)

func TestDefaultDB(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "vault")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, arg := range []string{root, ".", "../vault"} {
		v, err := vault.Open(arg)
		if err != nil {
			t.Fatal(err)
		}
		got := defaultDB(v.Root)
		if got != filepath.Join(v.Root, "..", "scriba.db") || strings.HasPrefix(got, v.Root+string(filepath.Separator)) {
			t.Errorf("--vault %q: defaultDB = %q, quería scriba.db en el padre de %q", arg, got, v.Root)
		}
	}
}

func TestGithubCredYRecordPush(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	if c, push := githubCred(st, key); push || c.Token != "" {
		t.Fatalf("sin conector: %+v %v", c, push)
	}
	sealed, err := secrets.Seal(key, []byte("gho_x"))
	if err != nil {
		t.Fatal(err)
	}
	conn := state.Connector{Name: "github", Account: "a", Secret: sealed, Repo: "a/kb", Push: false}
	if err := st.SaveConnector(conn); err != nil {
		t.Fatal(err)
	}
	if _, push := githubCred(st, key); push {
		t.Fatal("push apagado: no debería empujar")
	}
	conn.Push = true
	if err := st.SaveConnector(conn); err != nil {
		t.Fatal(err)
	}
	if c, push := githubCred(st, key); !push || c.Token != "gho_x" {
		t.Fatalf("con conector: %+v %v", c, push)
	}

	recordPush(st, "0123456789abcdef", nil)
	recordPush(st, "", errors.New("rechazado"))
	got, _, _ := st.Connector("github")
	if !strings.HasSuffix(got.LastPush, " · 0123456") || got.LastError != "rechazado" || !got.Push || got.Repo != "a/kb" {
		t.Fatalf("tras recordPush: %+v", got)
	}

	if err := st.DeleteConnector("github"); err != nil {
		t.Fatal(err)
	}
	recordPush(st, "fedcba9876543210", nil)
	if _, ok, _ := st.Connector("github"); ok {
		t.Fatal("recordPush resucitó el conector")
	}
}

// TestApagadoConStreamMCP comprueba que un GET /mcp abierto (stream SSE del SDK) no bloquea el apagado.
func TestApagadoConStreamMCP(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := (&agents.Store{S: st}).Create("prueba")
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, serveOpts{vault: filepath.Join(dir, "vault"), db: filepath.Join(dir, "scriba.db"),
			clientID: "x", tz: "UTC", ln: ln})
	}()
	url := "http://" + ln.Addr().String() + "/mcp"

	post := func(session, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}
	resp := post("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	session := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != http.StatusOK || session == "" {
		t.Fatalf("initialize: %d sesión=%q", resp.StatusCode, session)
	}
	if resp := post(session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); resp.StatusCode/100 != 2 {
		t.Fatalf("initialized: %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", session)
	get, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer get.Body.Close()
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET /mcp: %d", get.StatusCode)
	}

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServe: %v", err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Fatalf("apagado lento: %v", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("el apagado no terminó en 3 s con un stream MCP abierto")
	}
}
