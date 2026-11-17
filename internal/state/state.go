// Package state guarda el estado persistente de la instancia (ajustes y conectores)
// en state.db. A diferencia del índice, NO es descartable.
package state

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store es el acceso a state.db.
type Store struct{ db *sql.DB }

// Connector es un servicio externo enlazado. Secret llega ya sellado (state no conoce la llave).
type Connector struct {
	Name, Account       string
	Secret              []byte
	Repo                string
	Push                bool
	LastPush, LastError string
	Updated             time.Time
}

// Open abre (o crea) la base en p y aplica las migraciones embebidas.
func Open(p string) (*Store, error) {
	dsn := (&url.URL{Scheme: "file", OmitHost: true, Path: p,
		RawQuery: "_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=synchronous(normal)"}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		db.Close()
		return nil, err
	}
	prov, err := goose.NewProvider(goose.DialectSQLite3, db, fsys)
	if err == nil {
		_, err = prov.Up(context.Background())
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("migrar estado: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Setting devuelve el valor de key y si existe.
func (s *Store) Setting(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec("INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// SaveConnector inserta o reemplaza todos los campos del conector c.Name.
// Si c.Updated es cero se usa la hora actual.
func (s *Store) SaveConnector(c Connector) error {
	if c.Secret == nil {
		c.Secret = []byte{} // la columna es NOT NULL
	}
	if c.Updated.IsZero() {
		c.Updated = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO connectors(name, account, secret, repo, push, last_push, last_error, updated)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET account = excluded.account, secret = excluded.secret, repo = excluded.repo,
		  push = excluded.push, last_push = excluded.last_push, last_error = excluded.last_error, updated = excluded.updated`,
		c.Name, c.Account, c.Secret, c.Repo, c.Push, c.LastPush, c.LastError, c.Updated.UnixNano())
	return err
}

// Connector devuelve el conector name y si existe.
func (s *Store) Connector(name string) (Connector, bool, error) {
	c := Connector{Name: name}
	var updated int64
	err := s.db.QueryRow("SELECT account, secret, repo, push, last_push, last_error, updated FROM connectors WHERE name = ?", name).
		Scan(&c.Account, &c.Secret, &c.Repo, &c.Push, &c.LastPush, &c.LastError, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Connector{}, false, nil
	}
	if err != nil {
		return Connector{}, false, err
	}
	c.Updated = time.Unix(0, updated)
	return c, true, nil
}

func (s *Store) DeleteConnector(name string) error {
	_, err := s.db.Exec("DELETE FROM connectors WHERE name = ?", name)
	return err
}
