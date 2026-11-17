package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/duv0-x/scriba/internal/github"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/secrets"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/vault"
	"github.com/duv0-x/scriba/internal/watch"
	"github.com/duv0-x/scriba/internal/web"
)

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	vaultDir := fs.String("vault", "", "directorio del vault (obligatorio)")
	db := fs.String("db", "", "ruta de la base SQLite (default: scriba.db junto al vault)")
	listen := fs.String("listen", "127.0.0.1:8090", "dirección de escucha HTTP")
	clientID := fs.String("github-client-id", github.DefaultClientID, "client_id (público) de la OAuth App de GitHub con Device Flow")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *vaultDir == "" {
		fmt.Fprintln(os.Stderr, "serve: falta --vault")
		fs.Usage()
		return 2
	}
	if err := runServe(*vaultDir, *db, *listen, *clientID); err != nil {
		slog.Error("serve", "err", err)
		return 1
	}
	return 0
}

func runServe(vaultDir, db, listen, clientID string) error {
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	watchDone := make(chan error, 1)
	go func() { watchDone <- watch.Run(ctx, v, ix, nil) }()

	srv := &http.Server{
		Addr: listen,
		Handler: (&web.Server{
			V: v, Ix: ix, State: st, Key: key,
			GitHub: github.New(), Git: web.GitVault{}, ClientID: clientID,
			OnImported: func() {
				if err := ix.Rebuild(v); err != nil {
					slog.Error("reconstruir índice tras importar", "err", err)
				}
			},
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("escuchando", "addr", listen)
		serveErr <- srv.ListenAndServe()
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
