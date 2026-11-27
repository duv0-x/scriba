// Package writer serializa las escrituras de agentes y de la web sobre el vault:
// valida la ruta, escribe de forma atómica, reindexa la nota y dispara el push.
// Los agentes commitean al momento; los guardados web (Autosave) agrupan los
// cambios de cada ruta en un commit diferido que se hace tras WebIdle, con Flush,
// con Close o justo antes de cualquier otra escritura de esa ruta.
package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
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
	Act      Activity                       // puede ser nil
	Loc      *time.Location                 // zona para el journal (nil = UTC)
	Push     func()                         // dispara el push (nil = sin push)
	OnChange func(path, agent, hash string) // nil permitido; hash = SHA-256 hex de lo escrito
	// WebIdle es cuánto espera el commit de un guardado web (Autosave) sin otro guardado
	// en la misma ruta (0 = 60 s).
	WebIdle time.Duration
	// GitMu serializa todo lo que toca el repo git del vault; compártelo con el
	// Pusher. nil = el Writer crea uno propio.
	GitMu *sync.Mutex
}

// Writer escribe notas en el vault, una a la vez. Cada escritura (lectura previa,
// escritura y commit) ocurre bajo cfg.GitMu, así un pull nunca ve el árbol a medias.
type Writer struct {
	mu      *sync.Mutex
	cfg     Config
	pending map[string]*pendingCommit // commits web diferidos por ruta
	closed  bool                      // tras Close, Autosave commitea al momento
}

type pendingCommit struct{ t *time.Timer }

var (
	ErrInvalidPath = errors.New("writer: ruta no permitida")
	ErrTooLarge    = errors.New("writer: contenido demasiado grande")
)

// MaxBytes es el tamaño máximo de una nota (y de lo que se añade).
const MaxBytes = 1 << 20

// WebAgent es el autor de los guardados web (Autosave).
const WebAgent = "web"

// Result describe una escritura: la ruta, el commit (vacío si no hubo) y si se creó.
type Result struct {
	Path    string
	Commit  string
	Created bool
	Hash    string // SHA-256 hex del contenido escrito en disco
}

// New crea un Writer.
func New(cfg Config) *Writer {
	if cfg.Loc == nil {
		cfg.Loc = time.UTC
	}
	if cfg.GitMu == nil {
		cfg.GitMu = &sync.Mutex{}
	}
	if cfg.WebIdle == 0 {
		cfg.WebIdle = 60 * time.Second
	}
	return &Writer{mu: cfg.GitMu, cfg: cfg, pending: map[string]*pendingCommit{}}
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
	return w.save(agent, path, abs, content, message, action, created, time.Now(), false)
}

// ErrConflict indica que la nota cambió desde que se leyó (bloqueo optimista del editor web).
var ErrConflict = errors.New("writer: la nota cambió desde que se abrió")

// WriteIfUnchanged reemplaza path solo si su contenido actual tiene el SHA-256 (hex) base.
// La comparación ocurre bajo el mismo lock que las escrituras de los agentes, así un cambio
// de un agente nunca se pisa. El commit es inmediato (lo usa el editor viejo /editar).
func (w *Writer) WriteIfUnchanged(agent, path, content, message, base string) (Result, error) {
	return w.writeIfUnchanged(agent, path, content, message, base, false)
}

// Autosave reemplaza path como WriteIfUnchanged, con autor WebAgent, pero difiere el commit:
// "web: actualiza <path>" se hace tras WebIdle sin otro Autosave de esa ruta, con Flush,
// con Close o justo antes de cualquier otra escritura de esa ruta. El archivo, el índice
// y OnChange se actualizan al momento.
func (w *Writer) Autosave(path, content, base string) (Result, error) {
	return w.writeIfUnchanged(WebAgent, path, content, "", base, true)
}

func (w *Writer) writeIfUnchanged(agent, path, content, message, base string, deferred bool) (Result, error) {
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
	return w.save(agent, path, abs, content, message, "write", false, time.Now(), deferred)
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
	return w.save(agent, path, abs, content, "", "append", created, time.Now(), false)
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
	return w.save(agent, p, abs, content, "", "journal", created, now, false)
}

// save escribe, reindexa, avisa del cambio, hace commit (con deferred solo lo agenda),
// dispara el push y registra la actividad (la de un guardado diferido se registra en
// flushLocked, al hacer su commit). Requiere w.mu.
// Una vez escrito el archivo, los fallos del índice y de la actividad solo se registran:
// la nota ya está en disco (y en git, o pendiente de su commit diferido) y el watcher
// la reindexa.
func (w *Writer) save(agent, rel, abs, content, message, action string, created bool, now time.Time, deferred bool) (Result, error) {
	if len(content) > MaxBytes {
		return Result{}, fmt.Errorf("%w: %d bytes (máximo %d)", ErrTooLarge, len(content), MaxBytes)
	}
	if !deferred {
		// El commit web pendiente de esta ruta se hace antes: la historia queda
		// "web: …" → "<agente>: …" y nunca se mezcla el contenido web con la
		// autoría del agente.
		if err := w.flushLocked(rel); err != nil {
			return Result{}, fmt.Errorf("writer: commit web pendiente de %s: %w", rel, err)
		}
	}
	content = strings.TrimRight(content, "\n") + "\n"
	sum := sha256.Sum256([]byte(content))
	h := hex.EncodeToString(sum[:])
	if err := atomicWrite(abs, []byte(content)); err != nil {
		return Result{}, err
	}
	res := Result{Path: rel, Created: created, Hash: h}
	if n, err := w.cfg.V.Read(rel); err != nil {
		slog.Warn("writer: leer para reindexar", "path", rel, "err", err)
	} else if err := w.cfg.Ix.Upsert(n); err != nil {
		slog.Warn("writer: reindexar", "path", rel, "err", err)
	}
	// Se avisa antes del commit: el archivo ya cambió aunque el commit falle, y en la Pi el
	// commit puede tardar más que el debounce del watcher.
	if w.cfg.OnChange != nil {
		w.cfg.OnChange(rel, agent, h)
	}
	scheduled := false
	if gitvault.IsRepo(w.cfg.V.Root) {
		if deferred && !w.closed {
			// Sin commit: se (re)agenda el commit diferido de esta ruta.
			w.schedule(rel)
			scheduled = true
		} else {
			if message == "" {
				verb := "actualiza"
				if created {
					verb = "crea"
				}
				message = agent + ": " + verb + " " + rel
			}
			hash, ok, err := gitvault.CommitPaths(w.cfg.V.Root, agent, message, rel)
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
	}
	// Los guardados diferidos registran su actividad en flushLocked, una vez por commit
	// y no por guardado: con autosave cada pocos segundos, state.db crecería sin parar.
	if w.cfg.Act != nil && !scheduled {
		if err := w.cfg.Act.AddActivity(now, agent, action, rel); err != nil {
			slog.Warn("writer: registrar actividad", "path", rel, "agent", agent, "err", err)
		}
	}
	return res, nil
}

// schedule (re)programa el commit diferido de rel. Requiere w.mu.
func (w *Writer) schedule(rel string) {
	if p := w.pending[rel]; p != nil {
		p.t.Stop()
	}
	p := &pendingCommit{}
	p.t = time.AfterFunc(w.cfg.WebIdle, func() { w.flushIf(rel, p) }) // flushIf espera a w.mu: p.t ya está asignado
	w.pending[rel] = p
}

// flushIf commitea rel si su commit pendiente sigue siendo p (no se re-agendó ni se hizo ya).
func (w *Writer) flushIf(rel string, p *pendingCommit) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending[rel] != p {
		return
	}
	if err := w.flushLocked(rel); err != nil {
		slog.Warn("writer: commit web diferido", "path", rel, "err", err)
	}
}

// Flush hace ya el commit web pendiente de path, si lo hay.
func (w *Writer) Flush(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushLocked(path)
}

// flushLocked commitea el cambio web pendiente de rel. Requiere w.mu.
func (w *Writer) flushLocked(rel string) error {
	p := w.pending[rel]
	if p == nil {
		return nil
	}
	p.t.Stop()
	delete(w.pending, rel)
	// El pendiente ya se borró: si el commit falla, el cambio queda en disco y lo
	// recoge el barrido del arranque (Sweep) o el siguiente commit de esta ruta.
	_, ok, err := gitvault.CommitPaths(w.cfg.V.Root, WebAgent, WebAgent+": actualiza "+rel, rel)
	// La actividad del guardado web se registra aquí, una vez por commit, también si
	// el commit falla: la escritura ya ocurrió.
	if w.cfg.Act != nil {
		if aerr := w.cfg.Act.AddActivity(time.Now(), WebAgent, "write", rel); aerr != nil {
			slog.Warn("writer: registrar actividad", "path", rel, "agent", WebAgent, "err", aerr)
		}
	}
	if err != nil {
		return err
	}
	if ok && w.cfg.Push != nil {
		w.cfg.Push()
	}
	return nil
}

// FlushPendingHeld hace todos los commits web pendientes. Requiere GitMu tomado
// (lo llama el Pusher antes del pull).
func (w *Writer) FlushPendingHeld() error {
	var errs []error
	for _, rel := range slices.Sorted(maps.Keys(w.pending)) {
		if err := w.flushLocked(rel); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
		}
	}
	return errors.Join(errs...)
}

// Close hace los commits web pendientes; después, Autosave commitea al momento.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return w.FlushPendingHeld()
}

// Sweep commitea como WebAgent las rutas que quedaron sin commit (el proceso murió con
// commits web pendientes). Se llama al abrir la KB.
func (w *Writer) Sweep() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	root := w.cfg.V.Root
	if !gitvault.IsRepo(root) {
		return nil
	}
	paths, err := gitvault.Dirty(root)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	msg := WebAgent + ": actualiza " + paths[0]
	if len(paths) > 1 {
		msg = fmt.Sprintf("%s: actualiza %d rutas pendientes", WebAgent, len(paths))
	}
	_, ok, err := gitvault.CommitPaths(root, WebAgent, msg, paths...)
	if err != nil {
		return err
	}
	if ok && w.cfg.Push != nil {
		w.cfg.Push()
	}
	return nil
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
