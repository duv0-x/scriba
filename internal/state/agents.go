package state

import (
	"database/sql"
	"errors"
	"time"
)

// ErrNameTaken indica que ya hay un token activo (no revocado) con ese nombre.
var ErrNameTaken = errors.New("ya existe un token activo con ese nombre")

// AgentToken es un token de agente. Hash solo lo rellena AgentByHash.
type AgentToken struct {
	ID                int64
	Name              string
	Hash              []byte
	Created, LastUsed time.Time
	Revoked           bool
	ClientID          string    // "" = token estático; con valor = concesión OAuth (ADR-010)
	Expires           time.Time // del access token; cero = no expira
	RefreshExpires    time.Time // solo lo rellena ListAgentTokens
}

// Expired dice si una concesión OAuth ya no se puede renovar.
func (a AgentToken) Expired() bool { return a.ClientID != "" && !time.Now().Before(a.RefreshExpires) }

// Activity es una acción de un agente sobre una nota.
type Activity struct {
	TS                  time.Time
	Agent, Action, Path string
}

// unixNano convierte el entero guardado en time; 0 es "nunca" (time cero).
func unixNano(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// InsertAgentToken guarda un token nuevo. Un nombre solo se reutiliza si el token anterior
// está revocado o es una concesión OAuth caducada (se borra); si no, devuelve ErrNameTaken.
func (s *Store) InsertAgentToken(name string, hash []byte, created time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM agent_tokens WHERE name = ? AND (revoked = 1 OR (client_id != '' AND refresh_expires <= ?))", name, created.UnixNano()); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM agent_tokens WHERE name = ?", name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrNameTaken
	}
	if _, err := tx.Exec("INSERT INTO agent_tokens(name, hash, created, kb) VALUES(?, ?, ?, COALESCE((SELECT value FROM settings WHERE key = 'default_kb'), ''))", name, hash, created.UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

// AgentByHash busca el token por su hash (revocado o no) y dice si existe.
func (s *Store) AgentByHash(hash []byte) (AgentToken, bool, error) {
	var a AgentToken
	var created, used, expires int64
	err := s.db.QueryRow("SELECT id, name, hash, created, last_used, revoked, client_id, expires FROM agent_tokens WHERE hash = ?", hash).
		Scan(&a.ID, &a.Name, &a.Hash, &created, &used, &a.Revoked, &a.ClientID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentToken{}, false, nil
	}
	if err != nil {
		return AgentToken{}, false, err
	}
	a.Created, a.LastUsed, a.Expires = unixNano(created), unixNano(used), unixNano(expires)
	return a, true, nil
}

// ListAgentTokens devuelve todos los tokens, el más reciente primero, sin hashes.
func (s *Store) ListAgentTokens() ([]AgentToken, error) {
	rows, err := s.db.Query("SELECT id, name, created, last_used, revoked, client_id, refresh_expires FROM agent_tokens ORDER BY created DESC, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentToken
	for rows.Next() {
		var a AgentToken
		var created, used, refresh int64
		if err := rows.Scan(&a.ID, &a.Name, &created, &used, &a.Revoked, &a.ClientID, &refresh); err != nil {
			return nil, err
		}
		a.Created, a.LastUsed, a.RefreshExpires = unixNano(created), unixNano(used), unixNano(refresh)
		out = append(out, a)
	}
	return out, rows.Err()
}

// RevokeAgentToken revoca el token activo name y dice si había alguno.
func (s *Store) RevokeAgentToken(name string) (bool, error) {
	res, err := s.db.Exec("UPDATE agent_tokens SET revoked = 1 WHERE name = ? AND revoked = 0", name)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) TouchAgentToken(id int64, t time.Time) error {
	_, err := s.db.Exec("UPDATE agent_tokens SET last_used = ? WHERE id = ?", t.UnixNano(), id)
	return err
}

func (s *Store) AddActivity(kb string, ts time.Time, agent, action, path string) error {
	_, err := s.db.Exec("INSERT INTO activity(kb, ts, agent, action, path) VALUES(?, ?, ?, ?, ?)", kb, ts.UnixNano(), agent, action, path)
	return err
}

// Activity devuelve hasta limit acciones desde since (incluida), la más reciente primero.
func (s *Store) Activity(kb string, since time.Time, limit int) ([]Activity, error) {
	rows, err := s.db.Query("SELECT ts, agent, action, path FROM activity WHERE kb = ? AND ts >= ? ORDER BY ts DESC, id DESC LIMIT ?", kb, since.UnixNano(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var a Activity
		var ts int64
		if err := rows.Scan(&ts, &a.Agent, &a.Action, &a.Path); err != nil {
			return nil, err
		}
		a.TS = time.Unix(0, ts)
		out = append(out, a)
	}
	return out, rows.Err()
}
