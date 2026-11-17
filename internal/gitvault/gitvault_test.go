package gitvault

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const secreto = "tok-SECRETO"

// git ejecuta git en dir para preparar escenarios de prueba.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(context.Background(), dir, Cred{}, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, name, content string) string {
	t.Helper()
	write(t, dir, name, content)
	hash, ok, err := CommitAll(dir, "test", "add "+name)
	if err != nil || !ok {
		t.Fatalf("CommitAll: ok=%v err=%v", ok, err)
	}
	return hash
}

// setup crea un bare con un commit inicial en main y devuelve su ruta.
func setup(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	git(t, root, "-c", "init.defaultBranch=main", "init", "--bare", "-q", bare)
	seed := filepath.Join(root, "seed")
	os.Mkdir(seed, 0o755)
	if err := Init(seed); err != nil {
		t.Fatal(err)
	}
	commit(t, seed, "README.md", "hola\n")
	git(t, seed, "remote", "add", "origin", bare)
	if err := Push(context.Background(), seed, Cred{}); err != nil {
		t.Fatal(err)
	}
	return bare
}

func clone(t *testing.T, bare string, cred Cred) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "vault")
	if err := Clone(context.Background(), bare, dir, cred); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInitCommitAll(t *testing.T) {
	dir := t.TempDir()
	if IsRepo(dir) {
		t.Fatal("IsRepo antes de Init")
	}
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if !IsRepo(dir) {
		t.Fatal("IsRepo después de Init")
	}
	if _, ok, err := CommitAll(dir, "a", "vacío"); err != nil || ok {
		t.Fatalf("repo vacío: ok=%v err=%v", ok, err)
	}
	hash := commit(t, dir, "nota.md", "x\n")
	if h, _ := Head(dir); h != hash {
		t.Fatalf("Head=%s, CommitAll=%s", h, hash)
	}
	if got := git(t, dir, "log", "-1", "--format=%an <%ae>"); got != "test via Scriba <scriba@localhost>" {
		t.Fatalf("autor: %q", got)
	}
	if b := git(t, dir, "branch", "--show-current"); b != "main" {
		t.Fatalf("rama: %q", b)
	}
	if _, ok, err := CommitAll(dir, "a", "sin cambios"); err != nil || ok {
		t.Fatalf("sin cambios: ok=%v err=%v", ok, err)
	}
}

func TestClone(t *testing.T) {
	bare := setup(t)
	dir := clone(t, bare, Cred{})
	if b, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(b) != "hola\n" {
		t.Fatalf("README: %q", b)
	}
	// Directorio existente y vacío: también vale.
	empty := t.TempDir()
	if err := Clone(context.Background(), bare, empty, Cred{}); err != nil {
		t.Fatal(err)
	}
	// No vacío: error.
	if err := Clone(context.Background(), bare, dir, Cred{}); err == nil {
		t.Fatal("clone sobre dir no vacío debería fallar")
	}
}

func TestCloneFallidoDejaDirLimpio(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-existe.git")
	dir := filepath.Join(t.TempDir(), "nuevo")
	if err := Clone(context.Background(), missing, dir, Cred{}); err == nil {
		t.Fatal("esperaba error")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir no debería existir: %v", err)
	}
	empty := t.TempDir()
	if err := Clone(context.Background(), missing, empty, Cred{}); err == nil {
		t.Fatal("esperaba error")
	}
	if e, _ := os.ReadDir(empty); len(e) != 0 {
		t.Fatalf("dir debería seguir vacío: %v", e)
	}
}

func TestPush(t *testing.T) {
	bare := setup(t)
	dir := clone(t, bare, Cred{})
	hash := commit(t, dir, "b.md", "b\n")
	if err := Push(context.Background(), dir, Cred{}); err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "--git-dir="+bare, "log", "-1", "--format=%H", "main"); got != hash {
		t.Fatalf("bare main=%s, esperado %s", got, hash)
	}
}

func TestPullRebaseYStatus(t *testing.T) {
	ctx := context.Background()
	bare := setup(t)
	a := clone(t, bare, Cred{})
	b := clone(t, bare, Cred{})

	if ah, bh, err := Status(ctx, a); err != nil || ah != 0 || bh != 0 {
		t.Fatalf("status inicial: %d %d %v", ah, bh, err)
	}
	// b empuja un commit concurrente; a tiene uno local.
	commit(t, b, "remoto.md", "r\n")
	if err := Push(ctx, b, Cred{}); err != nil {
		t.Fatal(err)
	}
	commit(t, a, "local.md", "l\n")
	git(t, a, "fetch", "-q")
	if ah, bh, err := Status(ctx, a); err != nil || ah != 1 || bh != 1 {
		t.Fatalf("status divergente: ahead=%d behind=%d err=%v", ah, bh, err)
	}
	if err := Pull(ctx, a, Cred{}); err != nil {
		t.Fatal(err)
	}
	if ah, bh, err := Status(ctx, a); err != nil || ah != 1 || bh != 0 {
		t.Fatalf("tras rebase: ahead=%d behind=%d err=%v", ah, bh, err)
	}
	if _, err := os.Stat(filepath.Join(a, "remoto.md")); err != nil {
		t.Fatal(err)
	}
	if err := Push(ctx, a, Cred{}); err != nil {
		t.Fatal(err)
	}
	// Sin upstream: 0, 0 y Pull no-op.
	local := t.TempDir()
	Init(local)
	commit(t, local, "x.md", "x")
	if ah, bh, err := Status(ctx, local); err != nil || ah != 0 || bh != 0 {
		t.Fatalf("sin upstream: %d %d %v", ah, bh, err)
	}
	if err := Pull(ctx, local, Cred{}); err != nil {
		t.Fatal(err)
	}
}

func TestPusherDebounce(t *testing.T) {
	bare := setup(t)
	dir := clone(t, bare, Cred{})
	var calls atomic.Int32
	results := make(chan error, 10)
	p := NewPusher(dir, func() (Cred, bool) { calls.Add(1); return Cred{}, true }, 100*time.Millisecond,
		func(hash string, err error) { results <- err })
	hash := commit(t, dir, "p.md", "p\n")
	p.Trigger()
	p.Trigger()
	p.Trigger()
	select {
	case err := <-results:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("el Pusher no empujó")
	}
	time.Sleep(300 * time.Millisecond)
	p.Close()
	if n := calls.Load(); n != 1 {
		t.Fatalf("ciclos=%d, esperado 1", n)
	}
	if got := git(t, dir, "--git-dir="+bare, "log", "-1", "--format=%H", "main"); got != hash {
		t.Fatalf("bare main=%s, esperado %s", got, hash)
	}
	p.Trigger() // tras Close es no-op
	p.Close()
}

func TestTokenNoSeFiltra(t *testing.T) {
	ctx := context.Background()
	cred := Cred{Token: secreto}
	bare := setup(t)
	dir := clone(t, bare, cred)
	commit(t, dir, "s.md", "s\n")
	if err := Push(ctx, dir, cred); err != nil {
		t.Fatal(err)
	}
	if err := Pull(ctx, dir, cred); err != nil {
		t.Fatal(err)
	}

	// Fallos forzados: push a un remoto inexistente y clone fallido.
	git(t, dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "no-existe.git"))
	errs := []error{Push(ctx, dir, cred)}
	errs = append(errs, Clone(ctx, "/no/existe.git", filepath.Join(t.TempDir(), "c"), cred))
	// Un error cuyo stderr sí contiene el token debe salir saneado.
	_, err := run(ctx, dir, cred, secreto)
	errs = append(errs, err)
	for _, err := range errs {
		if err == nil {
			t.Fatal("esperaba error")
		}
		if strings.Contains(err.Error(), secreto) {
			t.Fatalf("token en error: %v", err)
		}
	}
	if !strings.Contains(err.Error(), "***") {
		t.Fatalf("esperaba token saneado: %v", err)
	}

	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if b, _ := os.ReadFile(path); strings.Contains(string(b), secreto) {
			t.Errorf("token en %s", path)
		}
		return nil
	})
}

func TestAskpassScript(t *testing.T) {
	script := filepath.Join(t.TempDir(), "askpass.sh")
	os.WriteFile(script, []byte(askpassScript), 0o700)
	ask := func(prompt string) string {
		cmd := exec.Command(script, prompt)
		cmd.Env = append(os.Environ(), "SCRIBA_GIT_TOKEN="+secreto)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := ask("Username for 'https://github.com': "); got != "x-access-token" {
		t.Fatalf("usuario: %q", got)
	}
	if got := ask("Password for 'https://x-access-token@github.com': "); got != secreto {
		t.Fatalf("password: %q", got)
	}
}

// hook escribe en dir un hook ejecutable que crea marker.
func hook(t *testing.T, dir, name, marker string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertNoExiste(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s no debería existir (err=%v)", p, err)
		}
	}
}

func TestConfigDelRepoNoEjecuta(t *testing.T) {
	out := t.TempDir()
	fsmon, preCommit := filepath.Join(out, "pwned-fsmonitor"), filepath.Join(out, "pwned-hook")
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("[core]\n\tfsmonitor = \"touch '" + fsmon + "'\"\n")
	f.Close()
	hook(t, filepath.Join(dir, ".git", "hooks"), "pre-commit", preCommit)
	commit(t, dir, "nota.md", "x\n")
	assertNoExiste(t, fsmon, preCommit)
}

func TestConfigGlobalIgnorada(t *testing.T) {
	bare := setup(t)
	home := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pwned")
	hooks := filepath.Join(home, "hooks")
	for _, h := range []string{"pre-commit", "pre-push", "post-checkout", "post-commit"} {
		hook(t, hooks, h, marker)
	}
	write(t, home, ".gitconfig", "[credential]\n\thelper = store\n[core]\n\thooksPath = "+hooks+"\n")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)

	cred := Cred{Token: secreto}
	dir := clone(t, bare, cred)
	commit(t, dir, "g.md", "g\n")
	if err := Push(context.Background(), dir, cred); err != nil {
		t.Fatal(err)
	}
	assertNoExiste(t, marker, filepath.Join(home, ".git-credentials"))
}

func TestPullConflictoAbortaRebase(t *testing.T) {
	ctx := context.Background()
	bare := setup(t)
	a := clone(t, bare, Cred{})
	b := clone(t, bare, Cred{})
	commit(t, b, "README.md", "remoto\n")
	if err := Push(ctx, b, Cred{}); err != nil {
		t.Fatal(err)
	}
	local := commit(t, a, "README.md", "local\n")

	err := Pull(ctx, a, Cred{})
	if err == nil || !strings.Contains(err.Error(), "conflicto con origin: se abortó el rebase") {
		t.Fatalf("Pull: %v", err)
	}
	if rebasing(a) {
		t.Fatal("quedó un rebase en curso")
	}
	if br := git(t, a, "branch", "--show-current"); br != "main" {
		t.Fatalf("rama tras abortar: %q", br)
	}
	if h, _ := Head(a); h != local {
		t.Fatalf("HEAD=%s, esperado el commit local %s", h, local)
	}
}

func TestPusherApagadoNoHaceNada(t *testing.T) {
	bare := setup(t)
	dir := clone(t, bare, Cred{})
	var calls, results atomic.Int32
	p := NewPusher(dir, func() (Cred, bool) { calls.Add(1); return Cred{}, false }, 10*time.Millisecond,
		func(string, error) { results.Add(1) })
	commit(t, dir, "p.md", "p\n")
	p.Trigger()
	time.Sleep(200 * time.Millisecond)
	p.Close()
	if calls.Load() != 1 || results.Load() != 0 {
		t.Fatalf("calls=%d results=%d", calls.Load(), results.Load())
	}
	if ah, _, err := Status(context.Background(), dir); err != nil || ah != 1 {
		t.Fatalf("no debería haber empujado: ahead=%d err=%v", ah, err)
	}
}
