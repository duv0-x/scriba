package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
