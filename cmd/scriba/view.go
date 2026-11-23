package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/term"

	"github.com/duv0-x/scriba/internal/termview"
	"github.com/duv0-x/scriba/internal/vault"
)

const viewUsage = `uso: scriba view <ruta> --vault <dir> [--follow] [--width N] [--style auto|dark|light|notty]
`

func view(args []string) int { return viewCmd(args, os.Stdout, os.Stderr) }

func viewCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("vault", "", "directorio del vault (obligatorio)")
	follow := fs.Bool("follow", false, "repinta cada vez que el archivo cambia (solo en una terminal)")
	width := fs.Int("width", 0, "ancho del texto (por defecto el de la terminal u 80)")
	style := fs.String("style", "auto", "estilo: auto, dark, light o notty")
	// Se aceptan flags antes y después de la ruta: flag se detiene en el primer posicional.
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var rel string
	if fs.NArg() > 0 {
		rel = fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return 2
		}
	}
	if rel == "" || *root == "" || fs.NArg() > 0 || *width < 0 || !slices.Contains(termview.Styles, *style) {
		fmt.Fprint(stderr, viewUsage)
		return 2
	}
	out, isFile := stdout.(*os.File)
	tty := isFile && term.IsTerminal(int(out.Fd()))
	if *style == "auto" && !tty {
		*style = "notty" // glamour ya cae en notty fuera de una terminal; así la cabecera tampoco lleva ANSI
	}
	if *follow && !tty {
		fmt.Fprintln(stderr, "error: --follow necesita que la salida sea una terminal")
		return 2
	}
	if *width == 0 {
		*width = 80
		if w, _, err := term.GetSize(int(out.Fd())); tty && err == nil && w > 0 {
			*width = w
		}
	}

	v, err := vault.Open(*root)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	abs, err := v.Abs(rel)
	if err != nil {
		if errors.Is(err, vault.ErrOutside) {
			fmt.Fprintf(stderr, "error: %s está fuera del vault\n", rel)
		} else {
			fmt.Fprintln(stderr, "error:", err)
		}
		return 1
	}
	paint := func() error {
		n, err := v.Read(rel)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no existe la nota %s en %s", rel, v.Root)
		} else if err != nil {
			return err
		}
		s, err := termview.Render(n, titles(v), *width, *style)
		if err != nil {
			return err
		}
		if *follow {
			s = "\x1b[H\x1b[2J" + s
		}
		_, err = io.WriteString(stdout, s)
		return err
	}
	if err := paint(); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if !*follow {
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := watchFile(ctx, abs, func() {
		if err := paint(); err != nil {
			// Un editor puede borrar y recrear el archivo al guardar: se sigue esperando.
			fmt.Fprintln(stderr, "error:", err)
		}
	}); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

// watchFile llama fn (con debounce de 200 ms) cada vez que cambia abs, hasta que ctx se cancela.
// Se vigila el directorio porque los editores suelen guardar renombrando un archivo temporal.
func watchFile(ctx context.Context, abs string, fn func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	if err := w.Add(filepath.Dir(abs)); err != nil {
		return err
	}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			return err
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if filepath.Clean(ev.Name) == abs {
				timer.Reset(200 * time.Millisecond)
			}
		case <-timer.C:
			fn()
		}
	}
}

// titles recorre el vault y devuelve el resolvedor de wikilinks a título, con la misma regla que
// el índice: nombre base o ruta sin ".md", sin mayúsculas; gana la ruta más corta y luego la menor.
// shortcut: lee todas las notas en cada repintado; si el vault crece mucho, usar scriba.db.
func titles(v *vault.Vault) func(string) (string, bool) {
	best := map[string]*vault.Note{}
	better := func(a, b string) bool { return len(a) < len(b) || (len(a) == len(b) && a < b) }
	_ = v.Walk(func(rel string) error {
		n, err := v.Read(rel)
		if err != nil {
			return nil // una nota ilegible solo deja sus enlaces sin título
		}
		noExt := strings.TrimSuffix(rel, ".md")
		for _, k := range []string{strings.ToLower(noExt), strings.ToLower(path.Base(noExt))} {
			if cur, ok := best[k]; !ok || better(rel, cur.Path) {
				best[k] = n
			}
		}
		return nil
	})
	return func(raw string) (string, bool) {
		n, ok := best[strings.ToLower(strings.TrimSpace(raw))]
		if !ok {
			return "", false
		}
		return n.Title, true
	}
}
