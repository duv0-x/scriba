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
	"syscall"
	"time"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/github"
	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/mcp"
	"github.com/duv0-x/scriba/internal/oauth"
	"github.com/duv0-x/scriba/internal/secrets"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/web"
)

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	data := fs.String("data", "", "directorio de datos: state.db, secret.key y kbs/<slug>/")
	vaultDir := fs.String("vault", "", "retirado: usa --data")
	db := fs.String("db", "", "retirado: usa --data")
	listen := fs.String("listen", "127.0.0.1:8090", "dirección de escucha HTTP")
	clientID := fs.String("github-client-id", github.DefaultClientID, "client_id (público) de la OAuth App de GitHub con Device Flow")
	publicURL := fs.String("public-url", "", "URL pública de la instancia (para enlaces de kb_open y el comando de conexión); vacío = la de cada petición")
	tz := fs.String("tz", "America/Bogota", "zona horaria del journal")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case *vaultDir != "" || *db != "":
		fmt.Fprintln(os.Stderr, "serve: --vault y --db se retiraron (ADR-013): usa --data <dir>, el directorio que contiene state.db (en el layout viejo, el padre del vault)")
		return 2
	case *data == "":
		fmt.Fprintln(os.Stderr, "serve: usa --data <dir>")
		fs.Usage()
		return 2
	}
	// Vacíos llegan desde systemd cuando falta la variable en scriba.env: "" escucharía en todas las
	// interfaces en un puerto aleatorio y "" como zona sería UTC sin avisar.
	if *listen == "" || *tz == "" {
		fmt.Fprintln(os.Stderr, "serve: --listen y --tz no pueden estar vacíos (¿falta SCRIBA_LISTEN o SCRIBA_TZ en scriba.env?)")
		return 2
	}

	o := serveOpts{data: *data, listen: *listen, clientID: *clientID, publicURL: *publicURL, tz: *tz}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runServe(ctx, o); err != nil {
		slog.Error("serve", "err", err)
		return 1
	}
	return 0
}

type serveOpts struct {
	data, listen, clientID, publicURL, tz string
	ln                                    net.Listener // si no es nil se usa en vez de listen (tests)
}

// runServe sirve hasta que ctx se cancele o un watcher muera.
func runServe(ctx context.Context, o serveOpts) error {
	loc, err := time.LoadLocation(o.tz)
	if err != nil {
		return fmt.Errorf("--tz: %w", err)
	}
	if err := os.MkdirAll(o.data, 0o755); err != nil {
		return err
	}
	// Con TMPDIR en disco (scriba.service), git y las subidas fallarían si alguien borra el directorio.
	if err := os.MkdirAll(os.TempDir(), 0o700); err != nil {
		return err
	}
	st, err := state.Open(filepath.Join(o.data, "state.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	key, err := secrets.LoadOrCreateKey(filepath.Join(o.data, "secret.key"))
	if err != nil {
		return err
	}
	reg, err := kbs.Open(ctx, kbs.Config{Data: o.data, State: st, Key: key, Loc: loc})
	if err != nil {
		return err
	}
	defer reg.Close()

	ln := o.ln
	if ln == nil {
		if ln, err = net.Listen("tcp", o.listen); err != nil {
			return err
		}
	}

	ctx, stop := context.WithCancel(ctx)
	defer stop()

	ag := &agents.Store{S: st}
	// OAuth para conectores (ADR-010): las rutas públicas van aquí; el consentimiento, en la web.
	az, err := oauth.New(key, ag, o.publicURL)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	az.Register(mux)
	mux.Handle("/mcp", mcp.Handler(mcp.Config{KBs: reg, Agents: ag, PublicURL: o.publicURL, Version: version}))
	mux.Handle("/", (&web.Server{
		KBs: reg, State: st, Key: key, Agents: ag, OAuth: az, PublicURL: o.publicURL,
		GitHub: github.New(), Git: web.GitVault{}, ClientID: o.clientID,
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
		return err
	case watchErr = <-reg.Err():
		// Si un watcher muere (p. ej. límite de inotify) el índice dejaría de actualizarse:
		// se apaga con error para que systemd reinicie el proceso.
		slog.Error("watcher", "err", watchErr)
		stop()
	case <-ctx.Done():
	}
	slog.Info("apagando")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = srv.Shutdown(shutCtx)
	stop()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(watchErr, err)
}
