// Package onboarding prepara el vault de un usuario nuevo: crea la estructura
// inicial desde una plantilla embebida o importa una base existente desde un zip.
package onboarding

import (
	"archive/zip"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:template
var templateFS embed.FS

// Límites para ImportZip.
const (
	MaxZipBytes   = 200 << 20 // tamaño descomprimido total
	MaxZipEntries = 20000
)

// maxZipBytes existe para que los tests puedan bajar el límite.
var maxZipBytes uint64 = MaxZipBytes

var (
	// ErrUnsafeZip envuelve el motivo por el que se rechazó un zip.
	ErrUnsafeZip = errors.New("zip inseguro")
	// ErrNotEmpty indica que el directorio de destino ya tiene contenido.
	ErrNotEmpty = errors.New("el directorio de destino no está vacío")
)

// Scaffold copia la estructura inicial del vault en dir, que se crea si no existe.
func Scaffold(dir string) error {
	if err := ensureEmpty(dir); err != nil {
		return err
	}
	sub, err := fs.Sub(templateFS, "template")
	if err != nil {
		return err
	}
	return fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

// Result resume una importación.
type Result struct {
	Files      int  // archivos regulares extraídos
	DroppedGit bool // el zip traía un .git (historial) y se descartó
}

// ImportZip extrae el zip en dir, que debe estar vacío o no existir. Valida todas
// las entradas antes de escribir nada y, si falla a mitad, borra lo escrito.
// Nunca extrae .git: su config (core.fsmonitor, hooks…) ejecutaría código al
// operar el vault con git.
func ImportZip(r io.ReaderAt, size int64, dir string) (res Result, err error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return res, fmt.Errorf("%w: %v", ErrUnsafeZip, err)
	}
	files, droppedGit, err := plan(zr.File)
	if err != nil {
		return res, err
	}
	if err := ensureEmpty(dir); err != nil {
		return res, err
	}

	_, statErr := os.Stat(dir)
	created := os.IsNotExist(statErr)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	defer func() {
		if err == nil {
			return
		}
		if created {
			os.RemoveAll(dir)
			return
		}
		// dir estaba vacío (salvo .DS_Store): todo lo que cuelga de él es nuestro.
		for _, f := range files {
			os.RemoveAll(filepath.Join(dir, strings.SplitN(f.name, "/", 2)[0]))
		}
	}()

	for _, f := range files {
		dst := filepath.Join(dir, filepath.FromSlash(f.name))
		if f.isDir {
			if err = os.MkdirAll(dst, 0o755); err != nil {
				return Result{}, err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return Result{}, err
		}
		if err = extract(f.file, dst); err != nil {
			return Result{}, err
		}
		res.Files++
	}
	res.DroppedGit = droppedGit
	return res, nil
}

type entry struct {
	file  *zip.File
	name  string // ruta limpia, separada por "/", ya sin el directorio raíz aplanado
	isDir bool
}

// plan valida las entradas del zip y devuelve las que hay que escribir, y si
// descartó alguna entrada de .git.
func plan(zfiles []*zip.File) ([]entry, bool, error) {
	if len(zfiles) > MaxZipEntries {
		return nil, false, fmt.Errorf("%w: %d entradas (máximo %d)", ErrUnsafeZip, len(zfiles), MaxZipEntries)
	}
	var (
		out        []entry
		total      uint64
		droppedGit bool
	)
	for _, f := range zfiles {
		raw := f.Name
		if strings.Contains(raw, `\`) {
			return nil, false, fmt.Errorf(`%w: ruta con "\": %q`, ErrUnsafeZip, raw)
		}
		if strings.HasPrefix(raw, "/") || filepath.IsAbs(raw) {
			return nil, false, fmt.Errorf("%w: ruta absoluta: %q", ErrUnsafeZip, raw)
		}
		for _, part := range strings.Split(raw, "/") {
			if part == ".." {
				return nil, false, fmt.Errorf("%w: ruta con '..': %q", ErrUnsafeZip, raw)
			}
		}
		if ignored(raw) {
			continue
		}
		// Un componente .git en cualquier nivel cubre también la ruta tras
		// aplanar la raíz, que solo quita un prefijo.
		if hasGitDir(raw) {
			droppedGit = true
			continue
		}
		if t := f.Mode().Type(); t != 0 && t != fs.ModeDir {
			return nil, false, fmt.Errorf("%w: %q no es archivo ni directorio (%v)", ErrUnsafeZip, raw, t)
		}
		name := path.Clean(strings.TrimSuffix(raw, "/"))
		if name == "." {
			continue
		}
		if !filepath.IsLocal(name) {
			return nil, false, fmt.Errorf("%w: ruta no local: %q", ErrUnsafeZip, raw)
		}
		isDir := strings.HasSuffix(raw, "/") || f.Mode().IsDir()
		if !isDir {
			if f.UncompressedSize64 > maxZipBytes || total+f.UncompressedSize64 > maxZipBytes {
				return nil, false, fmt.Errorf("%w: más de %d bytes descomprimidos", ErrUnsafeZip, maxZipBytes)
			}
			total += f.UncompressedSize64
		}
		out = append(out, entry{file: f, name: name, isDir: isDir})
	}
	return flatten(out), droppedGit, nil
}

// hasGitDir indica si algún componente de la ruta es .git; sin distinguir
// mayúsculas porque el sistema de archivos de macOS tampoco lo hace.
func hasGitDir(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}

// ignored descarta la basura que dejan macOS y Windows al comprimir.
func ignored(name string) bool {
	if name == "__MACOSX" || strings.HasPrefix(name, "__MACOSX/") {
		return true
	}
	base := path.Base(strings.TrimSuffix(name, "/"))
	return base == ".DS_Store" || base == "Thumbs.db"
}

// flatten quita el directorio raíz si todas las entradas cuelgan de él.
func flatten(es []entry) []entry {
	if len(es) == 0 {
		return es
	}
	root := strings.SplitN(es[0].name, "/", 2)[0]
	nested := false
	for _, e := range es {
		switch {
		case e.name == root && e.isDir:
		case strings.HasPrefix(e.name, root+"/"):
			nested = true
		default:
			return es
		}
	}
	if !nested {
		return nil // solo el directorio raíz vacío
	}
	out := es[:0:0]
	for _, e := range es {
		if e.name == root {
			continue
		}
		e.name = strings.TrimPrefix(e.name, root+"/")
		out = append(out, e)
	}
	return out
}

// extract escribe un archivo sin leer más de lo que declara la cabecera.
func extract(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafeZip, err)
	}
	defer rc.Close()
	// O_EXCL: una entrada duplicada no pisa a otra.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	limit := int64(f.UncompressedSize64)
	n, err := io.Copy(out, io.LimitReader(rc, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("extraer %q: %w", f.Name, err)
	}
	if n > limit {
		return fmt.Errorf("%w: %q es más grande de lo que declara", ErrUnsafeZip, f.Name)
	}
	return nil
}

// ensureEmpty acepta un dir inexistente o que solo contenga .DS_Store.
func ensureEmpty(dir string) error {
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.Name() != ".DS_Store" {
			return fmt.Errorf("%w: %s", ErrNotEmpty, dir)
		}
	}
	return nil
}
