package state

import (
	"bytes"
	"database/sql"
	"errors"
	"time"
)

// OAuthToken es la concesión de un conector: un token de agente que expira y se renueva (ADR-010).
type OAuthToken struct {
	Name, ClientID               string
	KB                           string // "" = la KB por defecto
	Hash, RefreshHash            []byte
	Now, Expires, RefreshExpires time.Time
}

// InsertOAuthToken guarda la concesión. Reemplaza un token revocado u otra concesión con el
// mismo nombre (reconexión); un token estático activo devuelve ErrNameTaken.
func (s *Store) InsertOAuthToken(t OAuthToken) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM agent_tokens WHERE name = ? AND (revoked = 1 OR client_id != '')", t.Name); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM agent_tokens WHERE name = ?", t.Name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrNameTaken
	}
	if _, err := tx.Exec(`INSERT INTO agent_tokens(name, hash, created, client_id, expires, refresh_hash, refresh_expires, kb)
		VALUES(?, ?, ?, ?, ?, ?, ?, `+kbOrDefault+`)`, t.Name, t.Hash, t.Now.UnixNano(), t.ClientID, t.Expires.UnixNano(),
		t.RefreshHash, t.RefreshExpires.UnixNano(), t.KB); err != nil {
		return err
	}
	return tx.Commit()
}

// RotateOAuthToken sustituye los dos tokens de la concesión cuyo refresh vigente es old
// (t.Name se ignora) y devuelve su nombre; "" si old no sirve. Si old es el refresh ya
// rotado, revoca la concesión y devuelve reused (RFC 9700).
func (s *Store) RotateOAuthToken(old []byte, t OAuthToken) (name string, reused bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	var (
		id, until int64
		agent     string
		client    string
		current   []byte
		revoked   bool
	)
	err = tx.QueryRow(`SELECT id, name, client_id, refresh_hash, refresh_expires, revoked FROM agent_tokens
		WHERE refresh_hash = ? OR prev_refresh_hash = ?`, old, old).Scan(&id, &agent, &client, &current, &until, &revoked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	case revoked:
		return "", false, nil
	case !bytes.Equal(current, old):
		if _, err := tx.Exec("UPDATE agent_tokens SET revoked = 1 WHERE id = ?", id); err != nil {
			return "", false, err
		}
		return agent, true, tx.Commit()
	case client != t.ClientID || t.Now.UnixNano() >= until:
		return "", false, nil
	}
	if _, err := tx.Exec(`UPDATE agent_tokens SET hash = ?, expires = ?, refresh_hash = ?, prev_refresh_hash = ?, refresh_expires = ?
		WHERE id = ?`, t.Hash, t.Expires.UnixNano(), t.RefreshHash, old, t.RefreshExpires.UnixNano(), id); err != nil {
		return "", false, err
	}
	return agent, false, tx.Commit()
}
