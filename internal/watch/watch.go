// Package watch mantiene el índice al día cuando cambian los .md del vault.
package watch

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/vault"
)

const debounce = 300 * time.Millisecond

// Run vigila el vault hasta que ctx se cancela. onChange puede ser nil.
func Run(ctx context.Context, v *vault.Vault, ix *index.Index, onChange func(path string)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	if err := addTree(w, v.Root, nil); err != nil {
		return err
	}

	// Debounce sin goroutines extra: cada ruta guarda su deadline y un ticker
	// del propio loop procesa las vencidas; al cancelar no queda nada colgado.
	pending := map[string]time.Time{}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.Warn("watch: error del watcher", "err", err)
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Has(fsnotify.Create) {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					if !skipDir(filepath.Base(ev.Name)) {
						// Un dir nuevo puede llegar ya con .md dentro (mkdir -p + escritura, o mv).
						if err := addTree(w, ev.Name, func(p string) { pending[p] = time.Now().Add(debounce) }); err != nil {
							slog.Warn("watch: no se pudo vigilar el directorio", "dir", ev.Name, "err", err)
						}
					}
					continue
				}
			}
			if isNote(ev.Name) {
				pending[ev.Name] = time.Now().Add(debounce)
			} else if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				// fsnotify no avisa de cada .md dentro de un directorio borrado o renombrado:
				// se trata como posible directorio y se purgan sus notas del índice.
				if name := filepath.Base(ev.Name); !strings.HasSuffix(name, ".md") && !skipDir(name) {
					purgeDir(v, ix, ev.Name, onChange)
				}
			}
		case now := <-tick.C:
			for p, due := range pending {
				if now.Before(due) {
					continue
				}
				delete(pending, p)
				process(v, ix, p, onChange)
			}
		}
	}
}

// skipDir replica el criterio de vault.Walk: ocultos (.git, .obsidian…), _templates y node_modules.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "_templates" || name == "node_modules"
}

// isNote descarta temporales de editores (.foo.md, foo.md~, foo.md.swp) y todo lo que no sea .md.
func isNote(p string) bool {
	name := filepath.Base(p)
	return strings.HasSuffix(name, ".md") && !strings.HasPrefix(name, ".")
}

// addTree añade root y sus subdirectorios al watcher; onNote (si no es nil) recibe cada .md encontrado.
func addTree(w *fsnotify.Watcher, root string, onNote func(string)) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return w.Add(p)
		}
		if onNote != nil && isNote(p) {
			onNote(p)
		}
		return nil
	})
}

// purgeDir quita del índice las notas bajo abs (el directorio ya no existe con ese nombre).
func purgeDir(v *vault.Vault, ix *index.Index, abs string, onChange func(string)) {
	r, err := filepath.Rel(v.Root, abs)
	if err != nil {
		slog.Warn("watch: ruta fuera del vault", "path", abs, "err", err)
		return
	}
	prefix := filepath.ToSlash(r) + "/"
	paths, err := ix.Tree()
	if err != nil {
		slog.Warn("watch: no se pudo leer el índice", "err", err)
		return
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		if err := ix.Delete(p); err != nil {
			slog.Warn("watch: no se pudo borrar del índice", "path", p, "err", err)
		}
		if onChange != nil {
			onChange(p)
		}
	}
}

func process(v *vault.Vault, ix *index.Index, abs string, onChange func(string)) {
	r, err := filepath.Rel(v.Root, abs)
	if err != nil {
		slog.Warn("watch: ruta fuera del vault", "path", abs, "err", err)
		return
	}
	rel := filepath.ToSlash(r)
	if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
		if err := ix.Delete(rel); err != nil {
			slog.Warn("watch: no se pudo borrar del índice", "path", rel, "err", err)
		}
	} else if n, err := v.Read(rel); err != nil {
		slog.Warn("watch: nota ilegible", "path", rel, "err", err)
	} else if err := ix.Upsert(n); err != nil {
		slog.Warn("watch: no se pudo indexar", "path", rel, "err", err)
	}
	if onChange != nil {
		onChange(rel)
	}
}
