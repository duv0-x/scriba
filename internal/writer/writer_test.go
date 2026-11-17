package writer

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/vault"
)

type fakeAct struct {
	mu      sync.Mutex
	actions []string
}

func (f *fakeAct) AddActivity(ts time.Time, agent, action, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, agent+" "+action+" "+path)
	return nil
}

type env struct {
	w      *Writer
	root   string
	ix     *index.Index
	act    *fakeAct
	pushes atomic.Int32
}

func setup(t *testing.T, withGit bool) *env {
	t.Helper()
	root := t.TempDir()
	if withGit {
		if err := gitvault.Init(root); err != nil {
			t.Fatal(err)
		}
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
	e := &env{root: root, ix: ix, act: &fakeAct{}}
	e.w = New(Config{V: v, Ix: ix, Act: e.act, Loc: time.UTC, Push: func() { e.pushes.Add(1) }})
	return e
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWriteCreaYReemplaza(t *testing.T) {
	e := setup(t, true)
	res, err := e.w.Write("claude", "notas/zanahoria.md", "# Zanahoria\n\nTexto sobre hortalizas", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Commit == "" || res.Path != "notas/zanahoria.md" {
		t.Fatalf("res=%+v", res)
	}
	if got := read(t, e.root, "notas/zanahoria.md"); got != "# Zanahoria\n\nTexto sobre hortalizas\n" {
		t.Fatalf("contenido: %q", got)
	}
	if info, _ := os.Stat(filepath.Join(e.root, "notas/zanahoria.md")); info.Mode().Perm() != 0o644 {
		t.Fatalf("permisos: %v", info.Mode())
	}
	if got := gitOut(t, e.root, "log", "-1", "--format=%an|%s"); got != "claude via Scriba|claude: crea notas/zanahoria.md" {
		t.Fatalf("log: %q", got)
	}
	hits, err := e.ix.Search("hortalizas", 10)
	if err != nil || len(hits) != 1 || hits[0].Path != "notas/zanahoria.md" {
		t.Fatalf("search: %v %v", hits, err)
	}

	res, err = e.w.Write("claude", "notas/zanahoria.md", "# Zanahoria\n\notro texto\n\n\n", "mensaje propio")
	if err != nil || res.Created || res.Commit == "" {
		t.Fatalf("reemplazo: %+v %v", res, err)
	}
	if got := read(t, e.root, "notas/zanahoria.md"); got != "# Zanahoria\n\notro texto\n" {
		t.Fatalf("contenido: %q", got)
	}
	if got := gitOut(t, e.root, "log", "-1", "--format=%s"); got != "mensaje propio" {
		t.Fatalf("mensaje: %q", got)
	}
	if hits, _ := e.ix.Search("hortalizas", 10); len(hits) != 0 {
		t.Fatalf("el índice conserva el texto viejo: %v", hits)
	}
	if n := e.pushes.Load(); n != 2 {
		t.Fatalf("pushes=%d", n)
	}

	// Mismo contenido: sin commit y sin push.
	res, err = e.w.Write("claude", "notas/zanahoria.md", "# Zanahoria\n\notro texto\n", "")
	if err != nil || res.Commit != "" {
		t.Fatalf("sin cambios: %+v %v", res, err)
	}
	if n := e.pushes.Load(); n != 2 {
		t.Fatalf("push sin commit: %d", n)
	}
	want := []string{"claude create notas/zanahoria.md", "claude write notas/zanahoria.md", "claude write notas/zanahoria.md"}
	if fmt.Sprint(e.act.actions) != fmt.Sprint(want) {
		t.Fatalf("actividad: %v", e.act.actions)
	}
}

func TestAppend(t *testing.T) {
	e := setup(t, true)
	res, err := e.w.Append("a", "nueva.md", "uno")
	if err != nil || !res.Created {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := e.w.Append("a", "nueva.md", "dos\n"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, e.root, "nueva.md"); got != "uno\n\ndos\n" {
		t.Fatalf("contenido: %q", got)
	}
	if got := gitOut(t, e.root, "rev-list", "--count", "HEAD"); got != "2" {
		t.Fatalf("commits: %s", got)
	}
}

func TestLogJournal(t *testing.T) {
	e := setup(t, true)
	bogota := time.FixedZone("COT", -5*3600)
	e.w.cfg.Loc = bogota
	// 02:00 UTC del 17 = 21:00 del 16 en Bogotá.
	now := time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)
	if _, err := e.w.LogJournal("a", "Primera", "cuerpo uno", now); err != nil {
		t.Fatal(err)
	}
	res, err := e.w.LogJournal("b", "Segunda", "cuerpo dos", now.Add(time.Minute))
	if err != nil || res.Created || res.Path != "journal/2026-11-16.md" {
		t.Fatalf("%+v %v", res, err)
	}
	want := "---\ndate: 2026-11-16\ntype: journal\n---\n\n# 2026-11-16\n\n## Primera\n\ncuerpo uno\n\n## Segunda\n\ncuerpo dos\n"
	if got := read(t, e.root, "journal/2026-11-16.md"); got != want {
		t.Fatalf("journal:\n%q\nesperado:\n%q", got, want)
	}
	if _, err := e.w.LogJournal("a", "con\nsalto", "x", now); err == nil {
		t.Fatal("título con salto de línea aceptado")
	}
}

func TestRutasInvalidas(t *testing.T) {
	e := setup(t, true)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(e.root, "fuga")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../x.md", ".git/config.md", "a/.oculto/b.md", "_templates/t.md", "_TEMPLATES/x.md", "a/Node_Modules/b.md", "x.txt",
		`a\b.md`, "", "/abs.md", "a//b.md", "./a.md", "fuga/x.md", strings.Repeat("a", 256) + ".md"} {
		if _, err := e.w.Write("a", p, "x", ""); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%q: err=%v", p, err)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("se escribió fuera del vault: %v", entries)
	}
}

func TestContenidoInvalido(t *testing.T) {
	e := setup(t, true)
	if _, err := e.w.Write("a", "x.md", strings.Repeat("a", MaxBytes+1), ""); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("grande: %v", err)
	}
	if _, err := e.w.Write("a", "x.md", "a\xffb", ""); err == nil {
		t.Fatal("UTF-8 inválido aceptado")
	}
	// Append que haría crecer la nota por encima del máximo.
	if _, err := e.w.Write("a", "y.md", strings.Repeat("a", MaxBytes-10), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.w.Append("a", "y.md", strings.Repeat("b", 100)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("append grande: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "x.md")); !os.IsNotExist(err) {
		t.Fatal("x.md no debería existir")
	}
}

func TestConcurrente(t *testing.T) {
	e := setup(t, true)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := strings.Repeat(fmt.Sprintf("línea %d\n", i), 200)
			if _, err := e.w.Write(fmt.Sprintf("ag%d", i), fmt.Sprintf("c/n%d.md", i), body, ""); err != nil {
				errs <- err
			}
			if _, err := e.w.Append("ag", "c/comun.md", fmt.Sprintf("parte %d", i)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for i := range 10 {
		if got := read(t, e.root, fmt.Sprintf("c/n%d.md", i)); got != strings.Repeat(fmt.Sprintf("línea %d\n", i), 200) {
			t.Errorf("n%d corrupta", i)
		}
	}
	if got := strings.Count(read(t, e.root, "c/comun.md"), "parte "); got != 10 {
		t.Errorf("comun.md tiene %d partes", got)
	}
	if got := gitOut(t, e.root, "status", "--porcelain"); got != "" {
		t.Errorf("quedan cambios sin commit: %q", got)
	}
}

func TestSinGit(t *testing.T) {
	e := setup(t, false)
	res, err := e.w.Write("a", "x.md", "hola mundo", "")
	if err != nil || res.Commit != "" || !res.Created {
		t.Fatalf("%+v %v", res, err)
	}
	if n := e.pushes.Load(); n != 0 {
		t.Fatalf("push sin git: %d", n)
	}
	if hits, _ := e.ix.Search("mundo", 10); len(hits) != 1 {
		t.Fatalf("no indexada: %v", hits)
	}
	if _, err := os.Stat(filepath.Join(e.root, ".git")); !os.IsNotExist(err) {
		t.Fatal("no debería inicializar git")
	}
}

type failAct struct{}

func (failAct) AddActivity(time.Time, string, string, string) error { return errors.New("db caída") }

func TestActividadFallidaNoEsError(t *testing.T) {
	e := setup(t, true)
	e.w.cfg.Act = failAct{}
	res, err := e.w.Write("a", "x.md", "hola", "")
	if err != nil || res.Commit == "" {
		t.Fatalf("%+v %v", res, err)
	}
	// Índice cerrado: Upsert falla, pero la nota ya está escrita y commiteada.
	e.ix.Close()
	if res, err := e.w.Write("a", "y.md", "chao", ""); err != nil || res.Commit == "" {
		t.Fatalf("con índice caído: %+v %v", res, err)
	}
}

// TestPusherConcurrente comprueba que el writer y el Pusher comparten el lock de git:
// sin él, add/commit y pull --rebase chocan en .git/index.lock.
func TestPusherConcurrente(t *testing.T) {
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "remote.git")
	gitOut(t, tmp, "-c", "init.defaultBranch=main", "init", "--bare", "-q", bare)
	root := filepath.Join(tmp, "vault")
	if err := gitvault.Clone(t.Context(), bare, root, gitvault.Cred{}); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(tmp, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	var gitMu sync.Mutex
	var mu sync.Mutex
	var pushErrs []error
	p := gitvault.NewPusher(root, func() (gitvault.Cred, bool) { return gitvault.Cred{}, true }, 50*time.Millisecond,
		func(_ string, err error) {
			if err != nil {
				mu.Lock()
				pushErrs = append(pushErrs, err)
				mu.Unlock()
			}
		})
	p.Lock = &gitMu
	defer p.Close()
	w := New(Config{V: v, Ix: ix, GitMu: &gitMu, Push: p.Trigger})

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Go(func() {
			if _, err := w.Write("ag", fmt.Sprintf("n%d.md", i), fmt.Sprintf("nota %d", i), ""); err != nil {
				errs <- err
			}
			time.Sleep(time.Duration(i%5) * 20 * time.Millisecond)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	p.Trigger()
	deadline := time.Now().Add(15 * time.Second)
	for {
		out, _ := exec.Command("git", "--git-dir="+bare, "rev-list", "--count", "--all").CombinedOutput()
		if strings.TrimSpace(string(out)) == "20" {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			t.Fatalf("el bare no tiene los 20 commits: %s; push: %v", out, pushErrs)
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, err := range pushErrs {
		t.Errorf("push: %v", err)
	}
}
