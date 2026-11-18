package state

import (
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Read es la instantánea de una nota la última vez que un usuario la leyó.
type Read struct {
	Hash, Content string
	At            time.Time
}

// MarkRead guarda (o reemplaza) la lectura de path por user.
func (s *Store) MarkRead(user, path, hash, content string, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO reads(user, path, hash, content, at) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(user, path) DO UPDATE SET hash = excluded.hash, content = excluded.content, at = excluded.at`,
		user, path, hash, content, at.UnixNano())
	return err
}

// LastRead devuelve la última lectura de path por user y si existe.
func (s *Store) LastRead(user, path string) (Read, bool, error) {
	var r Read
	var at int64
	err := s.db.QueryRow("SELECT hash, content, at FROM reads WHERE user = ? AND path = ?", user, path).Scan(&r.Hash, &r.Content, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Read{}, false, nil
	}
	if err != nil {
		return Read{}, false, err
	}
	r.At = time.Unix(0, at)
	return r, true, nil
}

// ReadHashes devuelve path → hash leído por user, sin traer contenidos.
func (s *Store) ReadHashes(user string) (map[string]string, error) {
	rows, err := s.db.Query("SELECT path, hash FROM reads WHERE user = ?", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var p, h string
		if err := rows.Scan(&p, &h); err != nil {
			return nil, err
		}
		m[p] = h
	}
	return m, rows.Err()
}

// Baseline devuelve la primera visita de user (settings['baseline:'+user]);
// si no existe, guarda now y lo devuelve.
func (s *Store) Baseline(user string, now time.Time) (time.Time, error) {
	key := "baseline:" + user
	// Se lee primero para no escribir en cada vista; solo la primera visita inserta.
	v, ok, err := s.Setting(key)
	if err != nil {
		return time.Time{}, err
	}
	if !ok {
		// INSERT OR IGNORE: dos visitas simultáneas no pisan la primera.
		if _, err := s.db.Exec("INSERT OR IGNORE INTO settings(key, value) VALUES(?, ?)", key, strconv.FormatInt(now.UnixNano(), 10)); err != nil {
			return time.Time{}, err
		}
		if v, _, err = s.Setting(key); err != nil {
			return time.Time{}, err
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, n), nil
}

// ForgetPath borra las lecturas de path de todos los usuarios (la nota ya no existe).
func (s *Store) ForgetPath(path string) error {
	_, err := s.db.Exec("DELETE FROM reads WHERE path = ?", path)
	return err
}

// ClearBaselines borra la primera visita de todos los usuarios (la web lo llama tras
// importar un vault).
func (s *Store) ClearBaselines() error {
	_, err := s.db.Exec("DELETE FROM settings WHERE key GLOB 'baseline:*'")
	return err
}
