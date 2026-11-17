package vault

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func openTestVault(t *testing.T) *Vault {
	t.Helper()
	v, err := Open("testdata/vault")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWalk(t *testing.T) {
	var got []string
	if err := openTestVault(t).Walk(func(rel string) error {
		got = append(got, rel)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"decisions/personal/adr-001.md", "journal/2026-11-16.md", "notes/bad.md", "runbooks/personal/runbook-pi.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("Walk = %v, want %v", got, want)
	}
}

func TestRead(t *testing.T) {
	v := openTestVault(t)
	tests := []struct {
		path      string
		title     string
		typ       string
		date      string
		tags      []string
		links     []string
		metaEmpty bool
	}{
		{"decisions/personal/adr-001.md", "ADR-001 · Markdown plano", "decisions", "2026-11-16", []string{"go", "sqlite"}, nil, false},
		{"journal/2026-11-16.md", "2026-11-16", "journal", "2026-11-16", nil, []string{"adr-001", "runbook-pi", "no-existe"}, true},
		{"runbooks/personal/runbook-pi.md", "Desplegar en la Pi", "runbooks", "", []string{"a", "b"}, nil, false},
		{"notes/bad.md", "bad", "note", "", nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			n, err := v.Read(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if n.Path != tt.path || n.Title != tt.title || n.Type != tt.typ || n.Date != tt.date {
				t.Errorf("got Path=%q Title=%q Type=%q Date=%q", n.Path, n.Title, n.Type, n.Date)
			}
			if !slices.Equal(n.Tags, tt.tags) {
				t.Errorf("Tags = %v, want %v", n.Tags, tt.tags)
			}
			if !slices.Equal(n.Links, tt.links) {
				t.Errorf("Links = %v, want %v", n.Links, tt.links)
			}
			if n.Meta == nil || (len(n.Meta) == 0) != tt.metaEmpty {
				t.Errorf("Meta = %v, want empty=%v", n.Meta, tt.metaEmpty)
			}
			if n.Words == 0 || n.ModTime.IsZero() {
				t.Errorf("Words=%d ModTime=%v", n.Words, n.ModTime)
			}
		})
	}
}

func TestReadBodies(t *testing.T) {
	v := openTestVault(t)
	adr, err := v.Read("decisions/personal/adr-001.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(adr.Body, "---") || !strings.Contains(adr.Body, "configuración") {
		t.Errorf("adr-001 Body = %q", adr.Body)
	}
	bad, err := v.Read("notes/bad.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bad.Body, "---") {
		t.Errorf("bad.md Body debe incluir el frontmatter: %q", bad.Body)
	}
}

func TestReadOutside(t *testing.T) {
	v := openTestVault(t)
	for _, rel := range []string{"../x.md", "/etc/passwd", "journal/../../x.md"} {
		if _, err := v.Read(rel); !errors.Is(err, ErrOutside) {
			t.Errorf("Read(%q) err = %v, want ErrOutside", rel, err)
		}
	}
}

func TestReadSymlinkOutside(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secreto.md")
	if err := os.WriteFile(outside, []byte("# secreto\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "fuera.md")); err != nil {
		t.Fatal(err)
	}
	v, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read("fuera.md"); !errors.Is(err, ErrOutside) {
		t.Fatalf("Read(fuera.md) err = %v, want ErrOutside", err)
	}
}

func TestParseFrontmatterEdgeCases(t *testing.T) {
	tests := []struct {
		name, content, body string
		tags                []string
	}{
		{"crlf", "---\r\ntags: x\r\n---\r\nhola", "hola", []string{"x"}},
		{"sin cierre", "---\ntags: x\nhola", "---\ntags: x\nhola", nil},
		{"vacío", "---\n---\nhola", "hola", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := parse("x.md", tt.content)
			if n.Body != tt.body || !slices.Equal(n.Tags, tt.tags) || n.Meta == nil {
				t.Errorf("Body=%q Tags=%v Meta=%v", n.Body, n.Tags, n.Meta)
			}
		})
	}
}

func TestSymlinkAOculto(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[core]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "ok.md"), []byte("# ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../.git/config", filepath.Join(root, "notes", "x.md")); err != nil {
		t.Fatal(err)
	}
	v, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read("notes/x.md"); !errors.Is(err, ErrOutside) {
		t.Fatalf("Read(notes/x.md) err = %v, want ErrOutside", err)
	}
	var got []string
	if err := v.Walk(func(rel string) error { got = append(got, rel); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "notes/ok.md" {
		t.Fatalf("Walk = %v, want [notes/ok.md]", got)
	}
}

func TestLinksEnTabla(t *testing.T) {
	n := parse("x.md", "| a |\n|---|\n| [[adr-001\\|ADR]] |\n")
	if len(n.Links) != 1 || n.Links[0] != "adr-001" {
		t.Fatalf("Links = %q, want [adr-001]", n.Links)
	}
}
