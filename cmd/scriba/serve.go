package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/events"
	"github.com/duv0-x/scriba/internal/github"
	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/mcp"
	"github.com/duv0-x/scriba/internal/secrets"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/vault"
	"github.com/duv0-x/scriba/internal/watch"
	"github.com/duv0-x/scriba/internal/web"
	"github.com/duv0-x/scriba/internal/writer"
)

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	vaultDir := fs.String("vault", "", "directorio del vault (obligatorio)")
	db := fs.String("db", "", "ruta de la base SQLite (default: scriba.db junto al vault)")
	listen := fs.String("listen", "127.0.0.1:8090", "dirección de escucha HTTP")
	clientID := fs.String("github-client-id", github.DefaultClientID, "client_id (público) de la OAuth App de GitHub con Device Flow")
	publicURL := fs.String("public-url", "", "URL pública de la instancia (para enlaces de kb_open y el comando de conexión); vacío = la de cada petición")
	tz := fs.String("tz", "America/Bogota", "zona horaria del journal")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *vaultDir == "" {
		fmt.Fprintln(os.Stderr, "serve: falta --vault")
		fs.Usage()
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runServe(ctx, serveOpts{vault: *vaultDir, db: *db, listen: *listen, clientID: *clientID, publicURL: *publicURL, tz: *tz}); err != nil {
		slog.Error("serve", "err", err)
		return 1
	}
	return 0
}

type serveOpts struct {
	vault, db, listen, clientID, publicURL, tz string
	ln                                         net.Listener // si no es nil se usa en vez de listen (tests)
}

// runServe sirve hasta que ctx se cancele o el watcher muera.
func runServe(ctx context.Context, o serveOpts) error {
	vaultDir, db, listen, clientID := o.vault, o.db, o.listen, o.clientID
	loc, err := time.LoadLocation(o.tz)
	if err != nil {
		return fmt.Errorf("--tz: %w", err)
	}
	// Una instancia nueva arranca sin vault: lo crea el onboarding.
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		return err
	}
	v, err := vault.Open(vaultDir)
	if err != nil {
		return err
	}
	if db == "" {
		db = defaultDB(v.Root)
	}
	ix, err := index.Open(db)
	if err != nil {
		return err
	}
	defer ix.Close()

	// El estado (conectores, ajustes) y la llave van junto al índice, fuera del vault.
	dataDir := filepath.Dir(db)
	st, err := state.Open(filepath.Join(dataDir, "state.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dataDir, "secret.key"))
	if err != nil {
		return err
	}

	start := time.Now()
	if err := ix.Rebuild(v); err != nil {
		return fmt.Errorf("reconstruir índice: %w", err)
	}
	n, err := ix.NoteCount()
	if err != nil {
		return err
	}
	slog.Info("índice listo", "notas", n, "duración", time.Since(start).Round(time.Millisecond), "vault", v.Root)

	ln := o.ln
	if ln == nil {
		if ln, err = net.Listen("tcp", listen); err != nil {
			return err
		}
	}

	ctx, stop := context.WithCancel(ctx)
	defer stop()

	// Tiempo real: la presencia se actualiza antes de avisar al navegador, así el evento
	// "presence" que sigue a cada cambio ya incluye al agente.
	bus := events.NewBus()
	presence := events.NewPresence(60*time.Second, nil)
	// El Publisher une el aviso del writer y el del watcher de una misma escritura.
	pub := events.NewPublisher(bus, presence, 2*time.Second, nil)
	defer pub.Close()
	onDisk := func(path string, deleted bool) {
		if deleted {
			if err := st.ForgetPath(path); err != nil {
				slog.Warn("olvidar lecturas de nota borrada", "path", path, "err", err)
			}
		}
		pub.Disk(path, deleted)
	}

	watchDone := make(chan error, 1)
	go func() { watchDone <- watch.Run(ctx, v, ix, onDisk) }()

	// Push a GitHub tras cada commit de un agente; el ciclo comprueba al ejecutarse si el
	// conector sigue activo con push. gitMu evita que el commit del writer y el
	// pull --rebase del Pusher choquen en el repo.
	var gitMu sync.Mutex
	pusher := gitvault.NewPusher(v.Root, func() (gitvault.Cred, bool) { return githubCred(st, key) }, 30*time.Second,
		func(hash string, err error) { recordPush(st, hash, err) })
	pusher.Lock = &gitMu
	defer pusher.Close()
	wr := writer.New(writer.Config{V: v, Ix: ix, Act: st, Loc: loc, GitMu: &gitMu, Push: pusher.Trigger, OnChange: pub.Agent})
	// Commits que quedaron sin empujar (p. ej. un apagado antes del ciclo): se empujan al arrancar.
	if _, push := githubCred(st, key); push && gitvault.IsRepo(v.Root) {
		if ahead, _, err := gitvault.Status(ctx, v.Root); err != nil {
			slog.Warn("estado git al arrancar", "err", err)
		} else if ahead > 0 {
			pusher.Trigger()
		}
	}
	ag := &agents.Store{S: st}

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.Handler(mcp.Config{V: v, Ix: ix, W: wr, Agents: ag, PublicURL: o.publicURL, Version: version}))
	mux.Handle("/", (&web.Server{
		V: v, Ix: ix, State: st, Key: key, Agents: ag, PublicURL: o.publicURL,
		Bus: bus, Presence: presence, W: wr,
		GitHub: github.New(), Git: web.GitVault{}, ClientID: clientID,
		OnImported: func() {
			if err := ix.Rebuild(v); err != nil {
				slog.Error("reconstruir índice tras importar", "err", err)
			}
		},
	}).Handler())
	// Shutdown no corta las peticiones en curso: los streams GET/SSE del SDK MCP viven
	// hasta que se cancela su contexto, y baseCtx se cancela al empezar el apagado.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}
	srv.RegisterOnShutdown(cancelBase)
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("escuchando", "addr", ln.Addr().String())
		serveErr <- srv.Serve(ln)
	}()

	var watchErr error
	select {
	case err := <-serveErr:
		stop()
		<-watchDone
		return err
	case watchErr = <-watchDone:
		// Si el watcher muere (p. ej. límite de inotify) el índice dejaría de actualizarse:
		// se apaga con error para que systemd reinicie el proceso.
		if watchErr == nil {
			watchErr = errors.New("watcher terminó inesperadamente")
		}
		slog.Error("watcher", "err", watchErr)
		stop()
	case <-ctx.Done():
	}
	slog.Info("apagando")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = srv.Shutdown(shutCtx)
	if watchErr == nil {
		if werr := <-watchDone; werr != nil {
			slog.Warn("watcher", "err", werr)
		}
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(watchErr, err)
}

// defaultDB pone la base junto al vault (no dentro) para no ensuciar el repositorio git de las notas.
// root debe ser absoluta (vault.Open la normaliza), así "--vault ." no deja la base dentro del vault.
func defaultDB(root string) string {
	return filepath.Join(filepath.Dir(root), "scriba.db")
}

// githubCred descifra el token del conector de GitHub en el momento de usarlo y dice si
// hay que empujar: false sin conector, con push apagado, sin repo o si el token no descifra.
func githubCred(st *state.Store, key []byte) (gitvault.Cred, bool) {
	c, ok, err := st.Connector("github")
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
func recordPush(st *state.Store, hash string, err error) {
	var last, msg string
	if err != nil {
		msg = err.Error()
		slog.Warn("push a GitHub", "err", err)
	} else {
		last = time.Now().Format("2006-01-02 15:04") + " · " + shortHash(hash)
	}
	if _, err := st.UpdatePushResult("github", last, msg); err != nil {
		slog.Error("guardar resultado del push", "err", err)
	}
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}
