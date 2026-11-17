package onboarding

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// zipEntry describe una entrada del zip de prueba; mode 0 = archivo normal.
type zipEntry struct {
	name    string
	content string
	mode    fs.FileMode
}

func makeZip(t *testing.T, entries ...zipEntry) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func importZip(t *testing.T, dir string, entries ...zipEntry) error {
	t.Helper()
	r := makeZip(t, entries...)
	_, err := ImportZip(r, r.Size(), dir)
	return err
}

// tree lista los archivos de dir (rutas relativas con "/").
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func assertTree(t *testing.T, dir string, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := tree(t, dir)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("archivos = %v, se esperaba %v", got, want)
	}
}

func TestScaffold(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	if err := Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	want := []string{"README.md"}
	for _, d := range []string{"journal", "tickets", "plans", "decisions", "runbooks", "postmortems", "references", "drafts"} {
		want = append(want, d+"/.gitkeep")
	}
	for _, tpl := range []string{"ticket", "adr", "runbook", "postmortem"} {
		want = append(want, "_templates/"+tpl+".md")
	}
	assertTree(t, dir, want...)

	data, err := os.ReadFile(filepath.Join(dir, "_templates", "adr.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"title:", "date:", "status:", "tags:"} {
		if !strings.Contains(string(data), key) {
			t.Errorf("adr.md sin %q en el frontmatter", key)
		}
	}
}

func TestScaffoldIgnoraDSStore(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".DS_Store"), nil, 0o644)
	if err := Scaffold(dir); err != nil {
		t.Fatal(err)
	}
}

func TestScaffoldDirNoVacio(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "nota.md"), []byte("x"), 0o644)
	if err := Scaffold(dir); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("err = %v, se esperaba ErrNotEmpty", err)
	}
}

func TestImportZip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vault")
	err := importZip(t, dir,
		zipEntry{name: "README.md", content: "hola"},
		zipEntry{name: "journal/", mode: fs.ModeDir | 0o755},
		zipEntry{name: "journal/2026-11-16.md", content: "# día"},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTree(t, dir, "README.md", "journal/2026-11-16.md")
	data, _ := os.ReadFile(filepath.Join(dir, "journal", "2026-11-16.md"))
	if string(data) != "# día" {
		t.Fatalf("contenido = %q", data)
	}
	info, _ := os.Stat(filepath.Join(dir, "README.md"))
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("permisos = %v", info.Mode().Perm())
	}
}

func TestImportZipAplanaRaizUnica(t *testing.T) {
	dir := t.TempDir()
	err := importZip(t, dir,
		zipEntry{name: "mi-kb/", mode: fs.ModeDir | 0o755},
		zipEntry{name: "mi-kb/README.md", content: "x"},
		zipEntry{name: "mi-kb/plans/plan.md", content: "y"},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTree(t, dir, "README.md", "plans/plan.md")
}

func TestImportZipIgnoraBasura(t *testing.T) {
	dir := t.TempDir()
	err := importZip(t, dir,
		zipEntry{name: "kb/nota.md", content: "x"},
		zipEntry{name: "kb/.DS_Store", content: "basura"},
		zipEntry{name: "__MACOSX/kb/._nota.md", content: "basura"},
		zipEntry{name: "kb/Thumbs.db", content: "basura"},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Sin la basura, todo cuelga de kb/ y se aplana.
	assertTree(t, dir, "nota.md")
}

func TestImportZipRechaza(t *testing.T) {
	cases := map[string]zipEntry{
		"zip-slip":    {name: "../x", content: "malo"},
		"slip medio":  {name: "a/../../x", content: "malo"},
		"absoluta":    {name: "/etc/x", content: "malo"},
		"backslash":   {name: `a\x`, content: "malo"},
		"symlink":     {name: "link", content: "/etc/passwd", mode: fs.ModeSymlink | 0o777},
		"dispositivo": {name: "dev", mode: fs.ModeDevice | 0o644},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "vault")
			// La entrada mala va al final: no debe escribirse nada antes de validar.
			err := importZip(t, dir, zipEntry{name: "ok.md", content: "ok"}, bad)
			if !errors.Is(err, ErrUnsafeZip) {
				t.Fatalf("err = %v, se esperaba ErrUnsafeZip", err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("se creó %s", dir)
			}
			assertTree(t, parent)
		})
	}
}

func TestImportZipLimiteDeTamano(t *testing.T) {
	old := maxZipBytes
	maxZipBytes = 1024
	defer func() { maxZipBytes = old }()

	dir := filepath.Join(t.TempDir(), "vault")
	err := importZip(t, dir,
		zipEntry{name: "a.md", content: strings.Repeat("a", 600)},
		zipEntry{name: "b.md", content: strings.Repeat("b", 600)},
	)
	if !errors.Is(err, ErrUnsafeZip) {
		t.Fatalf("err = %v, se esperaba ErrUnsafeZip", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("se creó %s", dir)
	}
}

func TestImportZipDirNoVacio(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "previa.md"), []byte("x"), 0o644)
	err := importZip(t, dir, zipEntry{name: "nota.md", content: "y"})
	if !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("err = %v, se esperaba ErrNotEmpty", err)
	}
	assertTree(t, dir, "previa.md")
}

func TestImportZipBorraSiFallaAMitad(t *testing.T) {
	dir := t.TempDir()
	// Entrada duplicada: pasa la validación pero falla al escribir (O_EXCL).
	err := importZip(t, dir,
		zipEntry{name: "a.md", content: "1"},
		zipEntry{name: "b/c.md", content: "2"},
		zipEntry{name: "a.md", content: "3"},
	)
	if err == nil {
		t.Fatal("se esperaba error por entrada duplicada")
	}
	assertTree(t, dir)
}

func TestImportZipDescartaGit(t *testing.T) {
	casos := map[string][]zipEntry{
		"raíz única": {
			{name: "kb/.git/config", content: "[core]\n\tfsmonitor = \"touch /tmp/pwned\"\n"},
			{name: "kb/.git/HEAD", content: "ref: refs/heads/main\n"},
			{name: "kb/nota.md", content: "x"},
		},
		"mayúsculas": {
			{name: ".GIT/config", content: "x"},
			{name: ".Git/hooks/pre-commit", content: "x"},
			{name: "nota.md", content: "x"},
		},
		"subdirectorio": {
			{name: "sub/.git/x", content: "x"},
			{name: "sub/.git", content: "gitdir: /tmp/otro"},
			{name: "nota.md", content: "x"},
		},
	}
	for nombre, entries := range casos {
		t.Run(nombre, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "vault")
			r := makeZip(t, entries...)
			res, err := ImportZip(r, r.Size(), dir)
			if err != nil {
				t.Fatal(err)
			}
			if !res.DroppedGit || res.Files != 1 {
				t.Fatalf("Result = %+v", res)
			}
			assertTree(t, dir, "nota.md")
		})
	}
	r := makeZip(t, zipEntry{name: "kb/nota.md", content: "x"}, zipEntry{name: "kb/.gitignore", content: "x"})
	res, err := ImportZip(r, r.Size(), filepath.Join(t.TempDir(), "v"))
	if err != nil || res.DroppedGit || res.Files != 2 {
		t.Fatalf("sin .git: %+v %v", res, err)
	}
}
