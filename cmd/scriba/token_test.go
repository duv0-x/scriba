package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/state"
)

// newStateDB crea una state.db vacía en dir, como la deja serve.
func newStateDB(t *testing.T, dir string) {
	t.Helper()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
}

func runToken(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := tokenCmd(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestTokenCLISinStateDB(t *testing.T) {
	dir := t.TempDir()
	code, _, errs := runToken(t, "list", "--data", dir)
	if code != 1 || !strings.Contains(errs, "no hay state.db en "+dir) {
		t.Fatalf("%d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.db")); !os.IsNotExist(err) {
		t.Fatal("token creó la base")
	}
}

func TestTokenCLI(t *testing.T) {
	dir := t.TempDir()
	newStateDB(t, dir)
	code, out, errs := runToken(t, "create", "claude", "--data", dir)
	tok := strings.TrimSpace(out)
	if code != 0 || strings.Count(out, "\n") != 1 || !strings.HasPrefix(tok, agents.Prefix) || len(tok) != len(agents.Prefix)+43 {
		t.Fatalf("create: %d %q %q", code, out, errs)
	}
	if !strings.Contains(errs, "Guárdalo ahora: no se vuelve a mostrar.") {
		t.Fatalf("falta el aviso: %q", errs)
	}
	if code, _, _ := runToken(t, "create", "--data", dir, "claude"); code != 1 {
		t.Fatalf("create repetido: %d", code)
	}

	code, out, _ = runToken(t, "list", "--data", dir)
	if code != 0 || !strings.Contains(out, "claude") || strings.Contains(out, tok) || strings.Contains(out, tok[len(agents.Prefix):]) {
		t.Fatalf("list: %d %q", code, out)
	}

	if code, _, errs := runToken(t, "revoke", "claude", "--data", dir); code != 0 {
		t.Fatalf("revoke: %d %q", code, errs)
	}
	if code, _, _ := runToken(t, "revoke", "claude", "--data", dir); code != 1 {
		t.Fatalf("revoke repetido: %d", code)
	}
	if _, out, _ := runToken(t, "list", "--data", dir); !strings.Contains(out, "sí") {
		t.Fatalf("list tras revocar: %q", out)
	}

	for _, bad := range [][]string{{}, {"create"}, {"list", "x"}, {"borrar", "x"}, {"create", "a", "b"}} {
		if code, _, _ := runToken(t, bad...); code != 2 {
			t.Errorf("%v: código %d, quería 2", bad, code)
		}
	}
}
