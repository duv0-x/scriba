package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestViewCmd(t *testing.T) {
	const root = "../../internal/vault/testdata/vault"
	cases := []struct {
		name string
		args []string
		code int
		out  string
	}{
		{"normal", []string{"journal/2026-11-16.md", "--vault", root, "--style", "notty", "--width", "60"}, 0, "⟦ADR-001 · Markdown plano⟧"},
		{"inexistente", []string{"no/existe.md", "--vault", root}, 1, ""},
		{"fuera del vault", []string{"../x.md", "--vault", root}, 1, ""},
		{"sin --vault", []string{"journal/2026-11-16.md"}, 2, ""},
		{"sin ruta", []string{"--vault", root}, 2, ""},
		{"estilo inválido", []string{"journal/2026-11-16.md", "--vault", root, "--style", "rosa"}, 2, ""},
		{"--follow sin tty", []string{"journal/2026-11-16.md", "--vault", root, "--follow"}, 2, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := viewCmd(c.args, &stdout, &stderr); got != c.code {
				t.Fatalf("exit %d, quería %d; stderr: %s", got, c.code, stderr.String())
			}
			if c.code != 0 && stderr.Len() == 0 {
				t.Error("un error debe explicarse en stderr")
			}
			if !strings.Contains(stdout.String(), c.out) {
				t.Errorf("falta %q en:\n%s", c.out, stdout.String())
			}
		})
	}
}

func TestWatchFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "n.md")
	if err := os.WriteFile(f, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan struct{}, 10)
	done := make(chan error)
	go func() { done <- watchFile(ctx, f, func() { called <- struct{}{} }) }()
	time.Sleep(100 * time.Millisecond) // que el watcher quede registrado
	// Un archivo vecino no repinta; varias escrituras seguidas se agrupan en un repintado.
	_ = os.WriteFile(filepath.Join(dir, "otro.md"), []byte("x"), 0o644)
	for range 3 {
		_ = os.WriteFile(f, []byte("b"), 0o644)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("no se repintó al cambiar el archivo")
	}
	time.Sleep(300 * time.Millisecond)
	if len(called) != 0 {
		t.Errorf("%d repintados de más: el debounce no agrupó", len(called))
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
