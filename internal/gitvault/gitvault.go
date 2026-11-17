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

// run ejecuta git en dir y devuelve stdout. El error incluye stderr saneado.
// Neutraliza la config que ejecuta código o guarda credenciales: fsmonitor,
// hooks, credential helpers (el token solo llega por GIT_ASKPASS) y la config
// global y de sistema del usuario.
func run(ctx context.Context, dir string, cred Cred, args ...string) (string, error) {
	hooks, err := emptyHooksDir()
	if err != nil {
		return "", fmt.Errorf("gitvault: hooks: %w", err)
	}
	// Los -c de línea de comandos ganan a .git/config; credential.helper vacío
	// vacía la lista de helpers.
	safe := []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + hooks, "-c", "credential.helper="}
	cmd := exec.CommandContext(ctx, "git", append(safe, args...)...)
	cmd.Dir = dir
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
		msg := fmt.Sprintf("gitvault: git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
		if cred.Token != "" {
			msg = strings.ReplaceAll(msg, cred.Token, "***")
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(stdout.String()), nil
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
	_, err = run(ctx, dir, cred, "-c", "user.name=Scriba", "-c", "user.email=scriba@localhost",
		"-c", "commit.gpgsign=false", "pull", "--rebase", "-q")
	if err != nil && rebasing(dir) {
		// Un rebase a medias deja el vault en detached HEAD con marcas de conflicto: se deshace.
		if _, aerr := run(context.Background(), dir, Cred{}, "rebase", "--abort"); aerr != nil {
			return fmt.Errorf("conflicto con origin: falló el rebase --abort (%v): %w", aerr, err)
		}
		return fmt.Errorf("conflicto con origin: se abortó el rebase: %w", err)
	}
	return err
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
