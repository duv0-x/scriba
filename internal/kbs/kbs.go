package kbs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/duv0-x/scriba/internal/events"
	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/onboarding"
	"github.com/duv0-x/scriba/internal/secrets"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/vault"
	"github.com/duv0-x/scriba/internal/watch"
	"github.com/duv0-x/scriba/internal/writer"
)

// KB es una base de conocimiento abierta: lo que serve.go armaba una sola vez.
type KB struct {
	Slug     string
	V        *vault.Vault
	Ix       *index.Index
	W        *writer.Writer
	Bus      *events.Bus
	Presence *events.Presence

	pub    *events.Publisher
	pusher *gitvault.Pusher
	gitMu  *sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// Reindex reconstruye el índice desde el vault (tras importar).
func (k *KB) Reindex() error { return k.Ix.Rebuild(k.V) }

// Config son las dependencias del Registry.
type Config struct {
	Data  string
	State *state.Store
	Key   []byte
	Loc   *time.Location

	OldVault, OldIndex string
}

var (
	ErrInvalidSlug = errors.New("slug inválido")
	ErrExists      = errors.New("ya existe una base con ese slug")
	ErrNotFound    = errors.New("no existe esa base")
	ErrDefault     = errors.New("la base por defecto no se puede borrar")
	errClosed      = errors.New("registry cerrado")
)

type Registry struct {
	cfg    Config
	ctx    context.Context
	errc   chan error
	mu     sync.RWMutex
	kbs    map[string]*KB
	def    string
	closed bool
}

// Open migra el layout viejo si hace falta y abre todas las KBs de <data>/kbs/.
func Open(ctx context.Context, cfg Config) (*Registry, error) {
	if cfg.OldVault == "" {
		cfg.OldVault = filepath.Join(cfg.Data, "vault")
	}
	if cfg.OldIndex == "" {
		cfg.OldIndex = filepath.Join(cfg.Data, "scriba.db")
	}
	migrated, _, err := migrateLayout(cfg.Data, cfg.OldVault, cfg.OldIndex, func() (string, error) {
		c, ok, err := cfg.State.KBConnector("github", "")
		if err != nil {
			return "", err
		}
		if ok && c.Repo != "" {
			return SlugFromRepo(c.Repo), nil
		}
		return Fallback, nil
	})
	if err != nil {
		return nil, fmt.Errorf("migrar layout: %w", err)
	}

	kbsDir := filepath.Join(cfg.Data, "kbs")
	if err := os.MkdirAll(kbsDir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(kbsDir)
	if err != nil {
		return nil, err
	}
	var slugs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !ValidSlug(e.Name()) {
			slog.Warn("directorio de KB ignorado", "slug", e.Name())
			continue
		}
		slugs = append(slugs, e.Name())
	}
	sort.Strings(slugs)

	def := migrated
	stored, hasDefault, err := cfg.State.DefaultKB()
	if err != nil {
		return nil, err
	}
	if def == "" && hasDefault {
		i := sort.SearchStrings(slugs, stored)
		if i < len(slugs) && slugs[i] == stored {
			def = stored
		} else if len(slugs) > 0 {
			slog.Warn("KB por defecto no existe en disco", "kb", stored)
		}
	}
	if def == "" && len(slugs) > 0 {
		def = slugs[0]
	}
	if len(slugs) == 0 {
		def = Fallback
		if err := os.MkdirAll(filepath.Join(kbsDir, def, "vault"), 0o755); err != nil {
			return nil, err
		}
		slugs = append(slugs, def)
	}
	if err := cfg.State.AdoptKB(def); err != nil {
		return nil, err
	}

	r := &Registry{cfg: cfg, ctx: ctx, errc: make(chan error, 1), kbs: map[string]*KB{}, def: def}
	for _, slug := range slugs {
		k, err := r.open(slug)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("abrir KB %s: %w", slug, err), r.Close())
		}
		r.kbs[slug] = k
	}
	return r, nil
}

func (r *Registry) open(slug string) (*KB, error) {
	dir := filepath.Join(r.cfg.Data, "kbs", slug)
	vaultDir := filepath.Join(dir, "vault")
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		return nil, err
	}
	v, err := vault.Open(vaultDir)
	if err != nil {
		return nil, err
	}
	ix, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		return nil, err
	}
	start := time.Now()
	if err := ix.Rebuild(v); err != nil {
		ix.Close()
		return nil, fmt.Errorf("reconstruir índice: %w", err)
	}
	n, err := ix.NoteCount()
	if err != nil {
		ix.Close()
		return nil, err
	}
	slog.Info("índice listo", "kb", slug, "notas", n, "duración", time.Since(start).Round(time.Millisecond))

	// La presencia se actualiza antes de avisar al navegador, así el evento
	// "presence" que sigue a cada cambio ya incluye al agente.
	bus := events.NewBus()
	presence := events.NewPresence(60*time.Second, nil)
	// El Publisher une el aviso del writer y el del watcher de una misma escritura.
	pub := events.NewPublisher(bus, presence, 2*time.Second, nil)
	onDisk := func(path string, deleted bool) {
		if deleted {
			if err := r.cfg.State.ForgetPath(slug, path); err != nil {
				slog.Warn("olvidar lecturas de nota borrada", "path", path, "err", err)
			}
		}
		pub.Disk(path, deleted)
	}

	kctx, cancel := context.WithCancel(r.ctx)
	k := &KB{Slug: slug, V: v, Ix: ix, Bus: bus, Presence: presence, pub: pub, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(k.done)
		err := watch.Run(kctx, v, ix, onDisk)
		if kctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("watcher terminó inesperadamente")
		}
		e := fmt.Errorf("kb %s: %w", slug, err)
		select {
		case r.errc <- e:
		default:
		}
	}()

	// gitMu evita que el commit del writer y el pull --rebase del Pusher choquen en el repo.
	var gitMu sync.Mutex
	pusher := gitvault.NewPusher(v.Root, func() (gitvault.Cred, bool) { return githubCred(r.cfg.State, r.cfg.Key, slug) }, 30*time.Second,
		func(hash string, err error) { recordPush(r.cfg.State, slug, hash, err) })
	pusher.Lock = &gitMu
	k.pusher = pusher
	k.gitMu = &gitMu
	k.W = writer.New(writer.Config{V: v, Ix: ix, Act: activity{r.cfg.State, slug}, Loc: r.cfg.Loc, GitMu: &gitMu, Push: pusher.Trigger, OnChange: pub.Agent})
	// Los commits que quedaron sin empujar durante un apagado se empujan al arrancar.
	if _, push := githubCred(r.cfg.State, r.cfg.Key, slug); push && gitvault.IsRepo(v.Root) {
		if ahead, _, err := gitvault.Status(r.ctx, v.Root); err != nil {
			slog.Warn("estado git al arrancar", "err", err)
		} else if ahead > 0 {
			pusher.Trigger()
		}
	}
	return k, nil
}

type activity struct {
	st *state.Store
	kb string
}

func (a activity) AddActivity(ts time.Time, agent, action, path string) error {
	return a.st.AddActivity(a.kb, ts, agent, action, path)
}

func (r *Registry) Get(slug string) (*KB, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.kbs[slug]
	return k, ok
}

func (r *Registry) Default() *KB {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.kbs[r.def]
}

func (r *Registry) DefaultSlug() string { return r.def }

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.kbs))
	for slug := range r.kbs {
		out = append(out, slug)
	}
	sort.Strings(out)
	return out
}

// Err entrega el error del primer watcher que muere: el proceso debe apagarse.
func (r *Registry) Err() <-chan error { return r.errc }

// Create crea una KB con la plantilla inicial y su propio repositorio git.
func (r *Registry) Create(slug string) (*KB, error) {
	if !ValidSlug(slug) {
		return nil, ErrInvalidSlug
	}
	r.mu.RLock()
	closed := r.closed
	_, ok := r.kbs[slug]
	r.mu.RUnlock()
	if closed {
		return nil, errClosed
	}
	if ok {
		return nil, ErrExists
	}

	dir := filepath.Join(r.cfg.Data, "kbs", slug)
	if err := os.Mkdir(dir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrExists
		}
		return nil, err
	}
	ok = false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	vaultDir := filepath.Join(dir, "vault")
	if err := onboarding.Scaffold(vaultDir); err != nil {
		return nil, err
	}
	if err := gitvault.Init(vaultDir); err != nil {
		return nil, err
	}
	if _, _, err := gitvault.CommitAll(vaultDir, "Scriba", "Base nueva"); err != nil {
		return nil, err
	}
	k, err := r.open(slug)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		stop(k)
		return nil, errClosed
	}
	r.kbs[slug] = k
	r.mu.Unlock()
	ok = true
	return k, nil
}

// Delete para y borra una KB que no sea la por defecto.
func (r *Registry) Delete(slug string) error {
	r.mu.Lock()
	if slug == r.def {
		r.mu.Unlock()
		return ErrDefault
	}
	k, ok := r.kbs[slug]
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	delete(r.kbs, slug)
	r.mu.Unlock()

	closeErr := stop(k)
	if err := r.cfg.State.ForgetKB(slug); err != nil {
		return errors.Join(closeErr, err)
	}
	return errors.Join(closeErr, os.RemoveAll(filepath.Join(r.cfg.Data, "kbs", slug)))
}

func stop(k *KB) error {
	k.cancel()
	<-k.done
	k.pusher.Close()
	k.gitMu.Lock()
	defer k.gitMu.Unlock()
	k.pub.Close()
	return k.Ix.Close()
}

// Close para todas las KBs.
func (r *Registry) Close() error {
	r.mu.Lock()
	open := r.kbs
	r.kbs = map[string]*KB{}
	r.closed = true
	r.mu.Unlock()
	slugs := make([]string, 0, len(open))
	for slug := range open {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	var errs []error
	for _, slug := range slugs {
		if err := stop(open[slug]); err != nil {
			errs = append(errs, fmt.Errorf("cerrar KB %s: %w", slug, err))
		}
	}
	return errors.Join(errs...)
}

// githubCred descifra el token del conector de GitHub en el momento de usarlo y dice si
// hay que empujar: false sin conector, con push apagado, sin repo o si el token no descifra.
func githubCred(st *state.Store, key []byte, kb string) (gitvault.Cred, bool) {
	c, ok, err := st.KBConnector("github", kb)
	if err != nil || !ok || !c.Push || c.Repo == "" {
		return gitvault.Cred{}, false
	}
	tok, err := secrets.Open(key, c.Secret)
	if err != nil {
		slog.Error("descifrar token de GitHub", "err", err)
		return gitvault.Cred{}, false
	}
	return gitvault.Cred{Token: string(tok)}, true
}

// recordPush guarda el resultado del último push para mostrarlo en Conectores. Solo
// actualiza esas columnas: si el conector se borró mientras empujaba, no lo resucita.
func recordPush(st *state.Store, kb, hash string, err error) {
	var last, msg string
	if err != nil {
		msg = err.Error()
		slog.Warn("push a GitHub", "err", err)
	} else {
		last = time.Now().Format("2006-01-02 15:04") + " · " + shortHash(hash)
	}
	if _, err := st.UpdatePushResult("github", kb, last, msg); err != nil {
		slog.Error("guardar resultado del push", "err", err)
	}
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}
