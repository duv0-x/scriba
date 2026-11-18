// Package writer serializa las escrituras de agentes sobre el vault: valida la ruta,
// escribe de forma atómica, reindexa la nota, hace commit y dispara el push.
package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/vault"
)

// Activity registra lo que hace cada agente (lo implementa state).
type Activity interface {
	AddActivity(ts time.Time, agent, action, path string) error
}

// Config son las dependencias del Writer.
type Config struct {
	V        *vault.Vault
	Ix       *index.Index
	Act      Activity                 // puede ser nil
	Loc      *time.Location           // zona para el journal (nil = UTC)
	Push     func()                   // dispara el push (nil = sin push)
	OnChange func(path, agent string) // nil permitido
	// GitMu serializa todo lo que toca el repo git del vault; compártelo con el
	// Pusher. nil = el Writer crea uno propio.
	GitMu *sync.Mutex
}

// Writer escribe notas en el vault, una a la vez. Cada escritura (lectura previa,
// escritura y commit) ocurre bajo cfg.GitMu, así un pull nunca ve el árbol a medias.
type Writer struct {
	mu  *sync.Mutex
	cfg Config
}

var (
	ErrInvalidPath = errors.New("writer: ruta no permitida")
	ErrTooLarge    = errors.New("writer: contenido demasiado grande")
)

// MaxBytes es el tamaño máximo de una nota (y de lo que se añade).
const MaxBytes = 1 << 20

// Result describe una escritura: la ruta, el commit (vacío si no hubo) y si se creó.
type Result struct {
	Path    string
	Commit  string
	Created bool
}

// New crea un Writer.
func New(cfg Config) *Writer {
	if cfg.Loc == nil {
		cfg.Loc = time.UTC
	}
	if cfg.GitMu == nil {
		cfg.GitMu = &sync.Mutex{}
	}
	return &Writer{mu: cfg.GitMu, cfg: cfg}
}

// Write crea o reemplaza la nota path con content.
func (w *Writer) Write(agent, path, content, message string) (Result, error) {
	if err := checkContent(content); err != nil {
		return Result{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	abs, err := w.abs(path)
	if err != nil {
		return Result{}, err
	}
	created := !exists(abs)
	action := "write"
	if created {
		action = "create"
	}
	return w.save(agent, path, abs, content, message, action, created, time.Now())
}

// ErrConflict indica que la nota cambió desde que se leyó (bloqueo optimista del editor web).
var ErrConflict = errors.New("writer: la nota cambió desde que se abrió")

// WriteIfUnchanged reemplaza path solo si su contenido actual tiene el SHA-256 (hex) base.
// La comparación ocurre bajo el mismo lock que las escrituras de los agentes, así un cambio
// de un agente nunca se pisa.
func (w *Writer) WriteIfUnchanged(agent, path, content, message, base string) (Result, error) {
	if err := checkContent(content); err != nil {
		return Result{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	abs, err := w.abs(path)
	if err != nil {
		return Result{}, err
	}
	cur, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, err
	}
	if sum := sha256.Sum256(cur); hex.EncodeToString(sum[:]) != base {
		return Result{}, ErrConflict
	}
	return w.save(agent, path, abs, content, message, "write", false, time.Now())
}

// Append añade content al final de path, separado por una línea en blanco; crea la nota si no existe.
func (w *Writer) Append(agent, path, content string) (Result, error) {
	if err := checkContent(content); err != nil {
		return Result{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	abs, err := w.abs(path)
	if err != nil {
		return Result{}, err
	}
	old, created, err := readExisting(abs)
	if err != nil {
		return Result{}, err
	}
	if !created {
		content = strings.TrimRight(old, "\n") + "\n\n" + content
	}
	return w.save(agent, path, abs, content, "", "append", created, time.Now())
}

// LogJournal añade una sección "## title" con body a journal/YYYY-MM-DD.md (fecha de now en Loc).
func (w *Writer) LogJournal(agent, title, body string, now time.Time) (Result, error) {
	title = strings.TrimSpace(title)
	if title == "" || strings.ContainsAny(title, "\r\n") {
		return Result{}, fmt.Errorf("writer: título de journal inválido")
	}
	if err := checkContent(title + body); err != nil {
		return Result{}, err
	}
	day := now.In(w.cfg.Loc).Format(time.DateOnly)
	p := "journal/" + day + ".md"
	w.mu.Lock()
	defer w.mu.Unlock()
	abs, err := w.abs(p)
	if err != nil {
		return Result{}, err
	}
	old, created, err := readExisting(abs)
	if err != nil {
		return Result{}, err
	}
	if created {
		old = "---\ndate: " + day + "\ntype: journal\n---\n\n# " + day + "\n"
	}
	content := strings.TrimRight(old, "\n") + "\n\n## " + title + "\n\n" + strings.TrimSpace(body) + "\n"
	return w.save(agent, p, abs, content, "", "journal", created, now)
}

// save escribe, reindexa, avisa del cambio, hace commit, dispara el push y registra la actividad. Requiere w.mu.
// Una vez escrito el archivo, los fallos del índice y de la actividad solo se registran:
// la nota ya está en disco (y en git) y el watcher la reindexa.
func (w *Writer) save(agent, rel, abs, content, message, action string, created bool, now time.Time) (Result, error) {
	if len(content) > MaxBytes {
		return Result{}, fmt.Errorf("%w: %d bytes (máximo %d)", ErrTooLarge, len(content), MaxBytes)
	}
	content = strings.TrimRight(content, "\n") + "\n"
	if err := atomicWrite(abs, []byte(content)); err != nil {
		return Result{}, err
	}
	res := Result{Path: rel, Created: created}
	if n, err := w.cfg.V.Read(rel); err != nil {
		slog.Warn("writer: leer para reindexar", "path", rel, "err", err)
	} else if err := w.cfg.Ix.Upsert(n); err != nil {
		slog.Warn("writer: reindexar", "path", rel, "err", err)
	}
	// Se avisa antes del commit: el archivo ya cambió aunque el commit falle, y en la Pi el
	// commit puede tardar más que el debounce del watcher.
	if w.cfg.OnChange != nil {
		w.cfg.OnChange(rel, agent)
	}
	if gitvault.IsRepo(w.cfg.V.Root) {
		if message == "" {
			verb := "actualiza"
			if created {
				verb = "crea"
			}
			message = agent + ": " + verb + " " + rel
		}
		hash, ok, err := gitvault.CommitAll(w.cfg.V.Root, agent, message)
		if err != nil {
			return res, fmt.Errorf("writer: commit: %w", err)
		}
		if ok {
			res.Commit = hash
			if w.cfg.Push != nil {
				w.cfg.Push()
			}
		}
	}
	if w.cfg.Act != nil {
		if err := w.cfg.Act.AddActivity(now, agent, action, rel); err != nil {
			slog.Warn("writer: registrar actividad", "path", rel, "agent", agent, "err", err)
		}
	}
	return res, nil
}

// abs valida rel y devuelve su ruta absoluta dentro del vault.
func (w *Writer) abs(rel string) (string, error) {
	if err := checkPath(rel); err != nil {
		return "", err
	}
	// vault.Abs no resuelve symlinks de directorios intermedios si el archivo aún no existe:
	// se comprueba cada prefijo para no crear nada fuera del vault.
	segs := strings.Split(rel, "/")
	for i := 1; i < len(segs); i++ {
		if _, err := w.cfg.V.Abs(strings.Join(segs[:i], "/")); err != nil {
			return "", fmt.Errorf("%w: %s: %v", ErrInvalidPath, rel, err)
		}
	}
	abs, err := w.cfg.V.Abs(rel)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrInvalidPath, rel, err)
	}
	if info, err := os.Stat(abs); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s no es un archivo", ErrInvalidPath, rel)
	}
	return abs, nil
}

// checkPath aplica las reglas de forma de la ruta (relativa, con '/', .md, sin ocultos, _templates ni node_modules).
func checkPath(rel string) error {
	bad := func(why string) error { return fmt.Errorf("%w: %q: %s", ErrInvalidPath, rel, why) }
	switch {
	case rel == "":
		return bad("vacía")
	case strings.Contains(rel, `\`):
		return bad(`contiene '\'`)
	case !utf8.ValidString(rel) || strings.ContainsRune(rel, 0):
		return bad("caracteres inválidos")
	case path.Ext(rel) != ".md":
		return bad("debe terminar en .md")
	}
	for _, s := range strings.Split(rel, "/") {
		switch {
		case s == "" || s == "." || s == "..":
			return bad("segmento vacío, '.' o '..'")
		case strings.HasPrefix(s, "."):
			return bad("segmento oculto")
		case strings.EqualFold(s, "_templates") || strings.EqualFold(s, "node_modules"):
			return bad("_templates o node_modules")
		case len(s) > 255:
			return bad("segmento de más de 255 bytes")
		}
	}
	return nil
}

func checkContent(s string) error {
	if len(s) > MaxBytes {
		return fmt.Errorf("%w: %d bytes (máximo %d)", ErrTooLarge, len(s), MaxBytes)
	}
	if !utf8.ValidString(s) {
		return errors.New("writer: el contenido no es UTF-8 válido")
	}
	return nil
}

func exists(abs string) bool {
	_, err := os.Stat(abs)
	return err == nil
}

// readExisting devuelve el contenido actual y created=true si el archivo no existe.
func readExisting(abs string) (string, bool, error) {
	b, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return "", true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("writer: %w", err)
	}
	return string(b), false, nil
}

// atomicWrite escribe data en abs vía temporal + fsync + rename, creando los directorios padre.
func atomicWrite(abs string, data []byte) error {
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("writer: %w", err)
	}
	// Prefijo "." para que el watcher y el Walk ignoren el temporal.
	f, err := os.CreateTemp(dir, ".scriba-*.tmp")
	if err != nil {
		return fmt.Errorf("writer: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op tras el rename
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, abs)
	}
	if err != nil {
		return fmt.Errorf("writer: %w", err)
	}
	return nil
}
