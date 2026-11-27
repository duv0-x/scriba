package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/state"
)

// postMCP envía una petición JSON-RPC a /mcp y devuelve la respuesta con su cuerpo completo.
func postMCP(t *testing.T, url, tok, session, body string) (*http.Response, []byte) {
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
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

// waitReady espera a que /readyz responda 200.
func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("el servidor no quedó listo en 5 s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestApagadoConStreamMCP comprueba que un GET /mcp abierto (stream SSE del SDK) no bloquea el apagado.
func TestApagadoConStreamMCP(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := (&agents.Store{S: st}).Create("prueba", "")
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
		done <- runServe(ctx, serveOpts{data: dir, clientID: "x", tz: "UTC", ln: ln})
	}()
	url := "http://" + ln.Addr().String() + "/mcp"

	resp, _ := postMCP(t, url, tok, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	session := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != http.StatusOK || session == "" {
		t.Fatalf("initialize: %d sesión=%q", resp.StatusCode, session)
	}
	if resp, _ := postMCP(t, url, tok, session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); resp.StatusCode/100 != 2 {
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

// TestServeLayoutViejo: --data sobre una copia del layout viejo arranca, migra y sirve
// las mismas notas y el mismo /mcp que antes.
func TestServeLayoutViejo(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vault", "notas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vault", "notas", "hola.md"), []byte("---\ntitle: Hola\n---\n\nLa zanahoria es una raíz.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// El índice viejo se descarta en la migración.
	if err := os.WriteFile(filepath.Join(dir, "scriba.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := (&agents.Store{S: st}).Create("prueba", "")
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
		done <- runServe(ctx, serveOpts{data: dir, clientID: "x", tz: "UTC", ln: ln})
	}()
	base := "http://" + ln.Addr().String()
	waitReady(t, base)

	if _, err := os.Stat(filepath.Join(dir, "kbs", "principal", "vault", "notas", "hola.md")); err != nil {
		t.Fatalf("nota migrada: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vault")); !os.IsNotExist(err) {
		t.Fatalf("el vault viejo sigue ahí: %v", err)
	}

	resp, err := http.Get(base + "/n/notas/hola.md")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "zanahoria") {
		t.Fatalf("GET /n/notas/hola.md: %d", resp.StatusCode)
	}

	url := base + "/mcp"
	resp, _ = postMCP(t, url, tok, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	session := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != http.StatusOK || session == "" {
		t.Fatalf("initialize: %d sesión=%q", resp.StatusCode, session)
	}
	if resp, _ := postMCP(t, url, tok, session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); resp.StatusCode/100 != 2 {
		t.Fatalf("initialized: %d", resp.StatusCode)
	}
	resp, body = postMCP(t, url, tok, session, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kb_search","arguments":{"query":"zanahoria"}}}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "notas/hola.md") {
		t.Fatalf("kb_search: %d %s", resp.StatusCode, body)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runServe: %v", err)
	}

	// Segunda ejecución sobre el mismo dir: no vuelve a migrar nada.
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	done2 := make(chan error, 1)
	go func() {
		done2 <- runServe(ctx2, serveOpts{data: dir, clientID: "x", tz: "UTC", ln: ln2})
	}()
	base2 := "http://" + ln2.Addr().String()
	waitReady(t, base2)
	resp, err = http.Get(base2 + "/n/notas/hola.md")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("segunda ejecución, GET nota: %d", resp.StatusCode)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "kbs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "principal" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("kbs = %v, quería solo [principal]", names)
	}
	cancel2()
	if err := <-done2; err != nil {
		t.Fatalf("runServe (2): %v", err)
	}
}

// TestServeCompatVault: --vault/--db del layout viejo migran aunque el vault no se llame "vault".
func TestServeCompatVault(t *testing.T) {
	dir := t.TempDir()
	oldVault := filepath.Join(dir, "v2")
	if err := os.MkdirAll(filepath.Join(oldVault, "notas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldVault, "notas", "hola.md"), []byte("---\ntitle: Hola\n---\n\nzanahoria\n"), 0o644); err != nil {
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
		done <- runServe(ctx, serveOpts{data: dir, oldVault: oldVault, oldIndex: filepath.Join(dir, "scriba.db"), clientID: "x", tz: "UTC", ln: ln})
	}()
	waitReady(t, "http://"+ln.Addr().String())
	if _, err := os.Stat(filepath.Join(dir, "kbs", "principal", "vault", "notas", "hola.md")); err != nil {
		t.Fatalf("nota migrada: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runServe: %v", err)
	}
}

func TestServeFlagsVacios(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{},
		{"--data", dir, "--vault", filepath.Join(dir, "vault")},
		{"--data", dir, "--db", filepath.Join(dir, "scriba.db")},
		{"--data", dir, "--listen", ""},
		{"--data", dir, "--tz", ""},
	} {
		if code := serve(args); code != 2 {
			t.Errorf("serve %v = %d, quiero 2", args, code)
		}
	}
}
