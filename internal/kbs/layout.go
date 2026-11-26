// Package kbs abre y gestiona las bases de conocimiento (KB) de una instancia (ADR-013).
package kbs

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Fallback es el slug de la KB migrada cuando no hay repo de GitHub conectado.
const Fallback = "principal"

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ValidSlug dice si s sirve como slug: termina en rutas de disco y URLs, así que se valida en el borde.
func ValidSlug(s string) bool { return slugRE.MatchString(s) }

// SlugFromRepo normaliza el nombre de un repo de GitHub para usarlo como slug.
func SlugFromRepo(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	var b strings.Builder
	b.Grow(len(repo))
	for _, r := range strings.ToLower(repo) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s := strings.TrimLeft(b.String(), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	s = strings.TrimRight(s, "-")
	if !ValidSlug(s) {
		return Fallback
	}
	return s
}

const migrating = ".kbs-migrating"

// migrateLayout mueve el layout de un solo vault a <data>/kbs/<slug>/vault.
func migrateLayout(data, oldVault, oldIndex string, slug func() (string, error)) (string, bool, error) {
	kbsDir := filepath.Join(data, "kbs")
	tmpDir := filepath.Join(data, migrating)
	if _, err := os.Stat(kbsDir); err == nil {
		warnOldVault(oldVault)
		return "", false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}

	oldInfo, oldErr := os.Stat(oldVault)
	if _, err := os.Stat(tmpDir); err == nil {
		if errors.Is(oldErr, os.ErrNotExist) {
			entries, err := os.ReadDir(tmpDir)
			if err != nil {
				return "", false, err
			}
			if len(entries) != 1 || !entries[0].IsDir() {
				return "", false, fmt.Errorf("migración a medias inválida en %s", tmpDir)
			}
			s := entries[0].Name()
			if err := os.Rename(tmpDir, kbsDir); err != nil {
				return "", false, err
			}
			if err := removeOldIndex(oldIndex); err != nil {
				return "", false, err
			}
			slog.Info("layout migrado", "kb", s, "desde", oldVault)
			return s, true, nil
		}
		if oldErr != nil {
			return "", false, oldErr
		}
		if err := os.RemoveAll(tmpDir); err != nil {
			return "", false, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}

	if oldErr != nil {
		if errors.Is(oldErr, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, oldErr
	}
	if !oldInfo.IsDir() {
		return "", false, nil
	}

	s, err := slug()
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, s), 0o755); err != nil {
		return "", false, err
	}
	if err := os.Rename(oldVault, filepath.Join(tmpDir, s, "vault")); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmpDir, kbsDir); err != nil {
		return "", false, err
	}
	if err := removeOldIndex(oldIndex); err != nil {
		return "", false, err
	}
	slog.Info("layout migrado", "kb", s, "desde", oldVault)
	return s, true, nil
}

func warnOldVault(oldVault string) {
	info, err := os.Stat(oldVault)
	if err != nil || !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(oldVault)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != ".DS_Store" {
			slog.Warn("hay un vault del layout viejo que no se migra porque ya existe kbs/", "vault", oldVault)
			return
		}
	}
}

func removeOldIndex(oldIndex string) error {
	for _, p := range []string{oldIndex, oldIndex + "-wal", oldIndex + "-shm"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
