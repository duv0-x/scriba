// Package vault lee las notas Markdown del vault (la fuente de verdad, ADR-002).
package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ErrOutside indica que una ruta escapa del Root del vault.
var ErrOutside = errors.New("vault: ruta fuera del vault")

// Vault es un directorio de notas Markdown. Root es absoluta y limpia.
type Vault struct{ Root string }

// Open valida que root exista y sea un directorio, y guarda su ruta absoluta.
func Open(root string) (*Vault, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("vault: %s no es un directorio", abs)
	}
	return &Vault{Root: abs}, nil
}

// Abs valida rel (relativa al vault) y devuelve su ruta absoluta.
func (v *Vault) Abs(rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", ErrOutside
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrOutside
	}
	abs := filepath.Join(v.Root, clean)
	// Un symlink dentro del vault puede apuntar afuera: se compara la ruta resuelta.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return abs, nil
		}
		return "", fmt.Errorf("vault: %w", err)
	}
	root, err := filepath.EvalSymlinks(v.Root)
	if err != nil {
		return "", fmt.Errorf("vault: %w", err)
	}
	r, err := filepath.Rel(root, resolved)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", ErrOutside
	}
	// Tampoco vale un destino dentro del vault pero en zona ignorada (p. ej. un symlink a .git/config).
	if r != "." && slices.ContainsFunc(strings.Split(r, string(filepath.Separator)), ignored) {
		return "", ErrOutside
	}
	return abs, nil
}

// ignored dice si un segmento de ruta queda fuera del vault: ocultos, _templates y node_modules.
func ignored(name string) bool {
	return strings.HasPrefix(name, ".") || name == "_templates" || name == "node_modules"
}

// Walk llama fn con cada archivo *.md (ruta relativa con '/'), en orden léxico,
// saltando directorios ocultos, _templates y node_modules.
func (v *Vault) Walk(fn func(rel string) error) error {
	return filepath.WalkDir(v.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != v.Root && ignored(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(v.Root, path)
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if _, err := v.Abs(rel); err != nil {
				slog.Warn("vault: symlink ignorado", "path", rel, "err", err)
				return nil
			}
		}
		return fn(filepath.ToSlash(rel))
	})
}

// Read lee y parsea la nota rel.
func (v *Vault) Read(rel string) (*Note, error) {
	abs, err := v.Abs(rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	n := parse(filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel))), string(data))
	n.ModTime = info.ModTime()
	return n, nil
}
