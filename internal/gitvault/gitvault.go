// Package gitvault opera el repositorio git del vault llamando al binario
// git por os/exec. Las credenciales llegan a git por un GIT_ASKPASS temporal
// y nunca quedan en la URL, en .git/config ni en los errores.
package gitvault

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Cred es la credencial HTTPS para el remoto. Token vacío = sin autenticación.
type Cred struct{ Token string }

// askpassScript responde a los prompts de git: usuario fijo para tokens de
// GitHub y el token leído del entorno del subproceso (no del disco).
const askpassScript = `#!/bin/sh
case "$1" in
*Username*) echo x-access-token ;;
*Password*) printf '%s\n' "$SCRIBA_GIT_TOKEN" ;;
esac
`

// emptyHooksDir es un directorio vacío (0700) creado una vez por proceso para
// core.hooksPath: ni los hooks del repo ni los de la config global se ejecutan.
var emptyHooksDir = sync.OnceValues(func() (string, error) {
	return os.MkdirTemp("", "scriba-nohooks-")
})

// run ejecuta git en dir y devuelve stdout recortado. El error incluye stderr
// saneado.
func run(ctx context.Context, dir string, cred Cred, args ...string) (string, error) {
	out, err := runRaw(ctx, dir, cred, args...)
	return strings.TrimSpace(out), err
}

// runRaw ejecuta git en dir y devuelve stdout sin recortar (lo necesitan
// salidas con -z o espacios significativos, como status --porcelain).
func runRaw(ctx context.Context, dir string, cred Cred, args ...string) (string, error) {
	return runRawStdin(ctx, dir, cred, "", args...)
}

// runRawStdin es runRaw alimentando stdin con input (lo necesita
// check-ignore --stdin -z, que no acepta -z con rutas por argumentos).
// Neutraliza la config que ejecuta código o guarda credenciales: fsmonitor,
// hooks, credential helpers (el token solo llega por GIT_ASKPASS) y la config
// global y de sistema del usuario.
func runRawStdin(ctx context.Context, dir string, cred Cred, input string, args ...string) (string, error) {
	hooks, err := emptyHooksDir()
	if err != nil {
		return "", fmt.Errorf("gitvault: hooks: %w", err)
	}
	// Los -c de línea de comandos ganan a .git/config; credential.helper vacío
	// vacía la lista de helpers.
	safe := []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + hooks, "-c", "credential.helper="}
	cmd := exec.CommandContext(ctx, "git", append(safe, args...)...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	// exec.Cmd se queda con el último valor de cada variable repetida.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull)
	if cred.Token != "" {
		tmp, err := os.MkdirTemp("", "scriba-askpass-")
		if err != nil {
			return "", fmt.Errorf("gitvault: askpass: %w", err)
		}
		defer os.RemoveAll(tmp)
		script := filepath.Join(tmp, "askpass.sh")
		if err := os.WriteFile(script, []byte(askpassScript), 0o700); err != nil {
			return "", fmt.Errorf("gitvault: askpass: %w", err)
		}
		cmd.Env = append(cmd.Env, "GIT_ASKPASS="+script, "SCRIBA_GIT_TOKEN="+cred.Token)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := fmt.Sprintf("gitvault: git %s: %v: %s", subcomando(args), err, strings.TrimSpace(stderr.String()))
		if cred.Token != "" {
			msg = strings.ReplaceAll(msg, cred.Token, "***")
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

// subcomando devuelve el primer argumento que no es opción ("add", "commit"...)
// para que los errores no digan "git --literal-pathspecs". Tras "-c" va su
// valor, que también se salta.
func subcomando(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return args[0]
}

// IsRepo indica si dir es la raíz de un repositorio git.
func IsRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// Init crea un repositorio con rama main.
func Init(dir string) error {
	_, err := run(context.Background(), dir, Cred{}, "-c", "init.defaultBranch=main", "init")
	return err
}

// Clone clona url en dir, que no debe existir o debe estar vacío.
// Si falla, dir queda como estaba (inexistente o vacío).
func Clone(ctx context.Context, url, dir string, cred Cred) error {
	entries, err := os.ReadDir(dir)
	existed := err == nil
	switch {
	case existed && len(entries) > 0:
		return fmt.Errorf("gitvault: clone: %s no está vacío", dir)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("gitvault: clone: %w", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("gitvault: clone: %w", err)
	}
	if _, err := run(ctx, filepath.Dir(abs), cred, "clone", "--", url, abs); err != nil {
		if existed {
			cleanDir(abs)
		} else {
			os.RemoveAll(abs)
		}
		return err
	}
	return nil
}

func cleanDir(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// CommitAll hace git add -A y un commit si hay cambios. author es el nombre
// del agente; el commit queda como "<author> via Scriba <scriba@localhost>".
func CommitAll(dir, author, msg string) (hash string, committed bool, err error) {
	ctx := context.Background()
	if _, err := run(ctx, dir, Cred{}, "add", "-A"); err != nil {
		return "", false, err
	}
	out, err := run(ctx, dir, Cred{}, "status", "--porcelain")
	if err != nil {
		return "", false, err
	}
	if out == "" {
		return "", false, nil
	}
	if _, err := run(ctx, dir, Cred{},
		"-c", "user.name="+author+" via Scriba", "-c", "user.email=scriba@localhost",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", msg); err != nil {
		return "", false, err
	}
	hash, err = Head(dir)
	return hash, err == nil, err
}

// CommitPaths hace commit solo de paths (añadidos, modificados o borrados),
// sin tocar lo que haya cambiado o esté en el índice fuera de ellos. Las
// rutas son literales (--literal-pathspecs: ni globs ni magia ':'). Las rutas
// ignoradas por .gitignore se saltan en silencio, como hace CommitAll. Sin
// cambios en paths no hace commit.
func CommitPaths(dir, author, msg string, paths ...string) (hash string, committed bool, err error) {
	if len(paths) == 0 {
		return "", false, nil
	}
	ctx := context.Background()
	ign, err := ignoradas(ctx, dir, paths)
	if err != nil {
		return "", false, err
	}
	if len(ign) > 0 {
		skip := make(map[string]bool, len(ign))
		for _, p := range ign {
			skip[p] = true
		}
		var filtradas []string
		for _, p := range paths {
			if !skip[p] {
				filtradas = append(filtradas, p)
			}
		}
		paths = filtradas
	}
	if len(paths) == 0 {
		return "", false, nil
	}
	addArgs := append([]string{"--literal-pathspecs", "add", "-A", "--"}, paths...)
	if _, err := run(ctx, dir, Cred{}, addArgs...); err != nil {
		return "", false, err
	}
	diffArgs := append([]string{"--literal-pathspecs", "diff", "--cached", "--name-only", "--"}, paths...)
	out, err := run(ctx, dir, Cred{}, diffArgs...)
	if err != nil {
		return "", false, err
	}
	if out == "" {
		return "", false, nil
	}
	commitArgs := append([]string{"--literal-pathspecs",
		"-c", "user.name=" + author + " via Scriba", "-c", "user.email=scriba@localhost",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", msg, "--"}, paths...)
	if _, err := run(ctx, dir, Cred{}, commitArgs...); err != nil {
		return "", false, err
	}
	hash, err = Head(dir)
	return hash, err == nil, err
}

// ignoradas devuelve las rutas de paths que el .gitignore del repo ignora.
// check-ignore sale con exit 1 cuando ninguna lo está: no es un fallo. Con -z
// (que exige --stdin) las rutas con caracteres no ASCII no salen
// entrecomilladas (core.quotePath).
func ignoradas(ctx context.Context, dir string, paths []string) ([]string, error) {
	input := strings.Join(paths, "\x00") + "\x00"
	out, err := runRawStdin(ctx, dir, Cred{}, input, "check-ignore", "--stdin", "-z")
	if err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			return nil, nil
		}
		return nil, err
	}
	var rutas []string
	for _, e := range strings.Split(out, "\x00") {
		if e != "" {
			rutas = append(rutas, e)
		}
	}
	return rutas, nil
}

// Dirty devuelve, ordenadas, las rutas (relativas, con '/') con cambios sin
// commit: modificadas, nuevas (sin ignorar las de subdirectorios) o borradas.
func Dirty(dir string) ([]string, error) {
	out, err := runRaw(context.Background(), dir, Cred{},
		"status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range strings.Split(out, "\x00") {
		if e == "" {
			continue
		}
		// Cada entrada es "XY ruta": la ruta empieza en el tercer carácter.
		paths = append(paths, e[3:])
	}
	sort.Strings(paths)
	return paths, nil
}

// Head devuelve el hash del commit actual.
func Head(dir string) (string, error) {
	return run(context.Background(), dir, Cred{}, "rev-parse", "HEAD")
}

// hasUpstream indica si la rama actual tiene upstream configurado.
func hasUpstream(ctx context.Context, dir string) (bool, error) {
	_, err := run(ctx, dir, Cred{}, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		// "unknown revision": upstream configurado pero aún sin rama remota (clon de un repo vacío).
		if strings.Contains(err.Error(), "no upstream") || strings.Contains(err.Error(), "unknown revision") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Pull hace pull --rebase del upstream. Sin upstream no hay nada que traer.
func Pull(ctx context.Context, dir string, cred Cred) error {
	ok, err := hasUpstream(ctx, dir)
	if err != nil || !ok {
		return err
	}
	// Con commits web diferidos el árbol puede estar sucio y pull --rebase sin
	// autostash se niega.
	_, err = run(ctx, dir, cred, "-c", "user.name=Scriba", "-c", "user.email=scriba@localhost",
		"-c", "commit.gpgsign=false", "pull", "--rebase", "--autostash", "-q")
	if err != nil {
		if rebasing(dir) {
			// Un rebase a medias deja el vault en detached HEAD con marcas de conflicto: se deshace.
			if _, aerr := run(context.Background(), dir, Cred{}, "rebase", "--abort"); aerr != nil {
				return fmt.Errorf("conflicto con origin: falló el rebase --abort (%v): %w", aerr, err)
			}
			return fmt.Errorf("conflicto con origin: se abortó el rebase: %w", err)
		}
		return err
	}
	// Si al reaplicar el stash del autostash hay conflicto, git sale con exit 0
	// y deja rutas unmerged con marcas <<<<<<< en disco: se dejan en la versión
	// de HEAD (el cambio local queda en git stash) y se avisa.
	// Con -z las rutas con caracteres no ASCII no salen entrecomilladas
	// (core.quotePath).
	out, err := runRaw(ctx, dir, Cred{}, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return err
	}
	var rutas []string
	for _, e := range strings.Split(out, "\x00") {
		if e != "" {
			rutas = append(rutas, e)
		}
	}
	if len(rutas) == 0 {
		return nil
	}
	checkout := append([]string{"--literal-pathspecs", "checkout", "HEAD", "--"}, rutas...)
	if _, err := run(ctx, dir, Cred{}, checkout...); err != nil {
		return err
	}
	return fmt.Errorf("conflicto al reaplicar cambios locales en %s: quedaron en git stash", strings.Join(rutas, ", "))
}

// rebasing indica si hay un rebase en curso en dir.
func rebasing(dir string) bool {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(dir, ".git", d)); err == nil {
			return true
		}
	}
	return false
}

// Push empuja la rama actual a origin y fija el upstream.
func Push(ctx context.Context, dir string, cred Cred) error {
	_, err := run(ctx, dir, cred, "push", "-q", "-u", "origin", "HEAD")
	return err
}

// Status cuenta commits adelante/atrás del upstream local (sin fetch).
// Devuelve 0, 0 si la rama no tiene upstream.
func Status(ctx context.Context, dir string) (ahead, behind int, err error) {
	ok, err := hasUpstream(ctx, dir)
	if err != nil || !ok {
		return 0, 0, err
	}
	out, err := run(ctx, dir, Cred{}, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("gitvault: rev-list: salida inesperada %q", out)
	}
	if behind, err = strconv.Atoi(f[0]); err == nil {
		ahead, err = strconv.Atoi(f[1])
	}
	return ahead, behind, err
}
