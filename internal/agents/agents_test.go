package agents

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/state"
)

func TestGenerate(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		tok, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != len(Prefix)+43 || !strings.HasPrefix(tok, Prefix) {
			t.Fatalf("formato: %q", tok)
		}
		for _, c := range tok[len(Prefix):] {
			if !strings.ContainsRune(alphabet, c) {
				t.Fatalf("carácter fuera de base62 en %q", tok)
			}
		}
		if seen[tok] {
			t.Fatalf("token repetido: %q", tok)
		}
		seen[tok] = true
	}
}

func TestValidName(t *testing.T) {
	for n, want := range map[string]bool{"claude": true, "claude-code-2": true, "a": true, "": false, "-x": false,
		"Claude": false, "a_b": false, strings.Repeat("a", 40): true, strings.Repeat("a", 41): false} {
		if ValidName(n) != want {
			t.Errorf("ValidName(%q) != %v", n, want)
		}
	}
}

func TestVerify(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := Store{S: st}
	if _, err := s.Create("Mal Nombre"); err == nil {
		t.Fatal("aceptó un nombre inválido")
	}
	tok, err := s.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := s.Verify(tok); !ok || name != "claude" {
		t.Fatalf("Verify ok = %q %v", name, ok)
	}
	l, _ := s.List()
	if len(l) != 1 || time.Since(l[0].LastUsed) > time.Minute {
		t.Fatalf("last_used no se actualizó: %+v", l)
	}
	otro, _ := Generate()
	for _, bad := range []string{otro, tok[len(Prefix):], "xxx_" + tok[len(Prefix):], tok + "a", ""} {
		if _, ok := s.Verify(bad); ok {
			t.Errorf("Verify aceptó %q", bad)
		}
	}

	// El token en claro no queda en la base (ni en el WAL).
	st.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "state.db*"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(tok)) || bytes.Contains(b, []byte(tok[len(Prefix):])) {
			t.Fatalf("token en claro en %s", f)
		}
	}
	st, err = state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s = Store{S: st}

	if ok, err := s.Revoke("claude"); !ok || err != nil {
		t.Fatalf("Revoke: %v %v", ok, err)
	}
	if _, ok := s.Verify(tok); ok {
		t.Fatal("Verify aceptó un token revocado")
	}
}
