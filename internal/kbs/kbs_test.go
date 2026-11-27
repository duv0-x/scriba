package kbs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/secrets"
	"github.com/duv0-x/scriba/internal/state"
)

func newState(t *testing.T, dir string) (*state.Store, []byte) {
	t.Helper()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("cerrar state: %v", err)
		}
	})
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	return st, key
}

func openRegistry(t *testing.T, dir string, st *state.Store, key []byte) *Registry {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := Open(ctx, Config{Data: dir, State: st, Key: key, Loc: time.UTC})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("cerrar registry: %v", err)
		}
		cancel()
	})
	return r
}

func writeNote(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidSlug(t *testing.T) {
	for _, slug := range []string{"", "..", "a/b", "../x", "IA-KB", "-a", "a_b", "a.b", strings.Repeat("a", 41)} {
		if ValidSlug(slug) {
			t.Errorf("ValidSlug(%q) = true", slug)
		}
	}
	for _, slug := range []string{"a", "ia-kb", "0x", strings.Repeat("a", 40)} {
		if !ValidSlug(slug) {
			t.Errorf("ValidSlug(%q) = false", slug)
		}
	}
}

func TestSlugFromRepo(t *testing.T) {
	long := strings.Repeat("A", 60)
	for _, tc := range []struct {
		repo, want string
	}{
		{"duv0-x/ia-kb", "ia-kb"},
		{"duv0-x/IA_KB", "ia-kb"},
		{"o/My.Notes", "my-notes"},
		{"o/___", Fallback},
		{"", Fallback},
		{long, strings.Repeat("a", 40)},
	} {
		if got := SlugFromRepo(tc.repo); got != tc.want {
			t.Errorf("SlugFromRepo(%q) = %q, quería %q", tc.repo, got, tc.want)
		}
		if got := SlugFromRepo(tc.repo); !ValidSlug(got) {
			t.Errorf("SlugFromRepo(%q) devolvió slug inválido %q", tc.repo, got)
		}
	}
}

func TestMigracionLayout(t *testing.T) {
	dir := t.TempDir()
	writeNote(t, filepath.Join(dir, "vault"), "---\ntitle: A\n---\nquimbaya\n")
	for _, name := range []string{"scriba.db", "scriba.db-wal", "scriba.db-shm"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("índice viejo"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, key := newState(t, dir)
	sealed, err := secrets.Seal(key, []byte("gho_x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveKBConnector("", state.Connector{Name: "github", Account: "u", Secret: sealed, Repo: "duv0-x/ia-kb", Push: false}); err != nil {
		t.Fatal(err)
	}

	r := openRegistry(t, dir, st, key)
	migrated := filepath.Join(dir, "kbs", "ia-kb", "vault", "a.md")
	if _, err := os.Stat(migrated); err != nil {
		t.Fatalf("nota migrada: %v", err)
	}
	for _, p := range []string{filepath.Join(dir, "vault"), filepath.Join(dir, "scriba.db"), filepath.Join(dir, "scriba.db-wal"), filepath.Join(dir, "scriba.db-shm"), filepath.Join(dir, migrating)} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s sigue existiendo (err=%v)", p, err)
		}
	}
	if got := r.DefaultSlug(); got != "ia-kb" {
		t.Fatalf("DefaultSlug = %q", got)
	}
	if got, ok, err := st.DefaultKB(); err != nil || !ok || got != "ia-kb" {
		t.Fatalf("DefaultKB = %q, %v, %v", got, ok, err)
	}
	hits, err := r.Default().Ix.Search("quimbaya", 10)
	if err != nil || len(hits) != 1 || hits[0].Path != "a.md" {
		t.Fatalf("Search = %+v, %v", hits, err)
	}
	if c, ok, err := st.KBConnector("github", "ia-kb"); err != nil || !ok || c.Repo != "duv0-x/ia-kb" {
		t.Fatalf("KBConnector = %+v, %v, %v", c, ok, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "kbs"))
	if err != nil {
		t.Fatal(err)
	}

	r = openRegistry(t, dir, st, key)
	if got := r.DefaultSlug(); got != "ia-kb" {
		t.Fatalf("segunda apertura: DefaultSlug = %q", got)
	}
	if got := r.List(); !reflect.DeepEqual(got, []string{"ia-kb"}) {
		t.Fatalf("List = %v", got)
	}
	if _, err := os.Stat(migrated); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(filepath.Join(dir, "kbs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(entries) || after[0].Name() != entries[0].Name() {
		t.Fatalf("segunda apertura creó KBs: antes=%v después=%v", entries, after)
	}
}

func TestMigracionSinConector(t *testing.T) {
	dir := t.TempDir()
	writeNote(t, filepath.Join(dir, "vault"), "---\ntitle: A\n---\ncontenido\n")
	st, key := newState(t, dir)
	r := openRegistry(t, dir, st, key)
	if got := r.DefaultSlug(); got != Fallback {
		t.Fatalf("DefaultSlug = %q, quería %q", got, Fallback)
	}
	if _, err := os.Stat(filepath.Join(dir, "kbs", Fallback, "vault", "a.md")); err != nil {
		t.Fatal(err)
	}
}

func TestMigracionSlugError(t *testing.T) {
	dir := t.TempDir()
	oldVault := filepath.Join(dir, "vault")
	writeNote(t, oldVault, "---\ntitle: A\n---\ncontenido\n")
	wantErr := errors.New("state no disponible")
	slug, migrated, err := migrateLayout(dir, oldVault, filepath.Join(dir, "scriba.db"), func() (string, error) {
		return "", wantErr
	})
	if !errors.Is(err, wantErr) || slug != "" || migrated {
		t.Fatalf("migrateLayout = %q, %v, %v", slug, migrated, err)
	}
	if _, err := os.Stat(filepath.Join(oldVault, "a.md")); err != nil {
		t.Fatalf("se movió el vault: %v", err)
	}
	for _, name := range []string{"kbs", migrating} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s existe tras el error: %v", name, err)
		}
	}
}

func TestMigracionAMedias(t *testing.T) {
	dir := t.TempDir()
	writeNote(t, filepath.Join(dir, migrating, "ia-kb", "vault"), "---\ntitle: A\n---\ncontenido\n")
	for _, name := range []string{"scriba.db", "scriba.db-wal", "scriba.db-shm"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("índice viejo"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, key := newState(t, dir)
	r := openRegistry(t, dir, st, key)
	if _, err := os.Stat(filepath.Join(dir, "kbs", "ia-kb", "vault", "a.md")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{migrating, "scriba.db", "scriba.db-wal", "scriba.db-shm"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s sigue existiendo: %v", name, err)
		}
	}
	if got := r.DefaultSlug(); got != "ia-kb" {
		t.Fatalf("DefaultSlug = %q", got)
	}
}

func TestLayoutViejoConKbsExistente(t *testing.T) {
	dir := t.TempDir()
	oldVault := filepath.Join(dir, "vault")
	writeNote(t, oldVault, "---\ntitle: Vieja\n---\ncontenido\n")
	if err := os.MkdirAll(filepath.Join(dir, "kbs"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, key := newState(t, dir)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	openRegistry(t, dir, st, key)
	data, err := os.ReadFile(filepath.Join(oldVault, "a.md"))
	if err != nil || !strings.Contains(string(data), "contenido") {
		t.Fatalf("vault viejo modificado: %q, %v", data, err)
	}
	if !strings.Contains(logs.String(), "hay un vault del layout viejo que no se migra porque ya existe kbs/") {
		t.Fatalf("falta el aviso del vault viejo: %s", logs.String())
	}
}

func TestInstanciaNueva(t *testing.T) {
	dir := t.TempDir()
	st, key := newState(t, dir)
	r := openRegistry(t, dir, st, key)
	if got := r.List(); !reflect.DeepEqual(got, []string{Fallback}) {
		t.Fatalf("List = %v", got)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "kbs", Fallback, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("vault nuevo no está vacío: %v", entries)
	}
}

func TestAislamiento(t *testing.T) {
	dir := t.TempDir()
	st, key := newState(t, dir)
	r := openRegistry(t, dir, st, key)
	a, err := r.Create("a", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Create("b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.W.Write("agente", "notas/x.md", "---\ntitle: X\n---\nzanahoria\n", ""); err != nil {
		t.Fatal(err)
	}
	ahits, err := a.Ix.Search("zanahoria", 10)
	if err != nil || len(ahits) != 1 {
		t.Fatalf("KB a: Search = %+v, %v", ahits, err)
	}
	bhits, err := b.Ix.Search("zanahoria", 10)
	if err != nil || len(bhits) != 0 {
		t.Fatalf("KB b: Search = %+v, %v", bhits, err)
	}
	alog, err := gitvault.Log(context.Background(), a.V.Root, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	blog, err := gitvault.Log(context.Background(), b.V.Root, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !logContains(alog, "notas/x.md") {
		t.Fatalf("el log de a no contiene la escritura: %+v", alog)
	}
	if logContains(blog, "notas/x.md") {
		t.Fatalf("el log de b contiene la escritura de a: %+v", blog)
	}
	if _, err := os.Stat(filepath.Join(b.V.Root, "notas", "x.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("la nota apareció en b: %v", err)
	}
}

func logContains(commits []gitvault.Commit, path string) bool {
	for _, c := range commits {
		for _, f := range c.Files {
			if f == path {
				return true
			}
		}
	}
	return false
}

func TestCreateYDelete(t *testing.T) {
	dir := t.TempDir()
	st, key := newState(t, dir)
	r := openRegistry(t, dir, st, key)
	if _, err := r.Create("X", nil); !errors.Is(err, ErrInvalidSlug) {
		t.Fatalf("Create inválido: %v", err)
	}
	a, err := r.Create("a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create("a", nil); !errors.Is(err, ErrExists) {
		t.Fatalf("Create duplicado: %v", err)
	}
	if !gitvault.IsRepo(a.V.Root) {
		t.Fatal("la KB creada no es un repo git")
	}
	if _, err := os.Stat(filepath.Join(a.V.Root, "README.md")); err != nil {
		t.Fatalf("falta la plantilla: %v", err)
	}
	if err := r.Delete(r.DefaultSlug()); !errors.Is(err, ErrDefault) {
		t.Fatalf("Delete default: %v", err)
	}
	if err := r.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete inexistente: %v", err)
	}
	kbDir := filepath.Join(dir, "kbs", "a")
	if err := r.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(kbDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directorio borrado: %v", err)
	}
	if _, ok := r.Get("a"); ok {
		t.Fatal("Get encuentra la KB borrada")
	}
	if got := r.List(); !reflect.DeepEqual(got, []string{Fallback}) {
		t.Fatalf("List tras Delete = %v", got)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create("b", nil); err == nil {
		t.Fatal("Create después de Close abrió una KB")
	}
}

func TestDeleteEsperaEscritura(t *testing.T) {
	dir := t.TempDir()
	st, key := newState(t, dir)
	r := openRegistry(t, dir, st, key)
	a, err := r.Create("a", nil)
	if err != nil {
		t.Fatal(err)
	}

	a.gitMu.Lock()
	locked := true
	defer func() {
		if locked {
			a.gitMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- r.Delete("a") }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := r.Get("a"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Delete no retiró la KB del registry")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("Delete no esperó la escritura activa: %v", err)
	default:
	}
	a.gitMu.Unlock()
	locked = false
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Delete no terminó tras la escritura")
	}
}

func TestGithubCredYRecordPush(t *testing.T) {
	dir := t.TempDir()
	st, key := newState(t, dir)
	if c, push := githubCred(st, key, "kb"); push || c.Token != "" {
		t.Fatalf("sin conector: %+v %v", c, push)
	}
	sealed, err := secrets.Seal(key, []byte("gho_x"))
	if err != nil {
		t.Fatal(err)
	}
	conn := state.Connector{Name: "github", Account: "a", Secret: sealed, Repo: "a/kb", Push: false}
	if err := st.SaveKBConnector("kb", conn); err != nil {
		t.Fatal(err)
	}
	if _, push := githubCred(st, key, "kb"); push {
		t.Fatal("push apagado: no debería empujar")
	}
	conn.Push = true
	if err := st.SaveKBConnector("kb", conn); err != nil {
		t.Fatal(err)
	}
	if c, push := githubCred(st, key, "kb"); !push || c.Token != "gho_x" {
		t.Fatalf("con conector: %+v %v", c, push)
	}

	recordPush(st, "kb", "0123456789abcdef", nil)
	recordPush(st, "kb", "", errors.New("rechazado"))
	got, _, _ := st.KBConnector("github", "kb")
	if !strings.HasSuffix(got.LastPush, " · 0123456") || got.LastError != "rechazado" || !got.Push || got.Repo != "a/kb" {
		t.Fatalf("tras recordPush: %+v", got)
	}

	if err := st.DeleteConnector("github"); err != nil {
		t.Fatal(err)
	}
	recordPush(st, "kb", "fedcba9876543210", nil)
	if _, ok, _ := st.KBConnector("github", "kb"); ok {
		t.Fatal("recordPush resucitó el conector")
	}
}

func TestCreateFill(t *testing.T) {
	dir := t.TempDir()
	st, key := newState(t, dir)
	r := openRegistry(t, dir, st, key)

	fillOK := func(vaultDir string) error {
		if err := os.MkdirAll(vaultDir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(vaultDir, "hola.md"), []byte("# Hola\n"), 0o644)
	}
	k, err := r.Create("z", fillOK)
	if err != nil {
		t.Fatal(err)
	}
	n, err := k.Ix.NoteCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("NoteCount = %d, quería 1", n)
	}
	if !gitvault.IsRepo(k.V.Root) {
		t.Fatal("la KB creada no es un repo git")
	}

	boom := errors.New("boom")
	fillErr := func(vaultDir string) error { return boom }
	_, err = r.Create("y", fillErr)
	if !errors.Is(err, boom) {
		t.Fatalf("Create con fill erróneo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kbs", "y")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directorio y sigue existiendo: %v", err)
	}
	if _, ok := r.Get("y"); ok {
		t.Fatal("Get encuentra la KB fallida")
	}
}
