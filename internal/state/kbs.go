package state

// DefaultKBKey es la clave de settings con el slug de la KB por defecto.
const DefaultKBKey = "default_kb"

// DefaultKB devuelve el slug de la KB por defecto y si está definido.
func (s *Store) DefaultKB() (string, bool, error) { return s.Setting(DefaultKBKey) }

// AdoptKB fija kb como KB por defecto y le asigna, en una transacción, las filas que aún no tienen KB
// (kb vacío): las de antes de la migración 00005. Es idempotente. Si ya hay una fila con la misma clave
// en kb, gana la existente.
func (s *Store) AdoptKB(kb string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exec := func(query string, args ...any) error {
		_, err := tx.Exec(query, args...)
		return err
	}
	if err := exec("INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", DefaultKBKey, kb); err != nil {
		return err
	}
	for _, query := range []string{
		"UPDATE agent_tokens SET kb = ? WHERE kb = ''",
		"UPDATE activity SET kb = ? WHERE kb = ''",
		"UPDATE OR IGNORE reads SET kb = ? WHERE kb = ''",
		"UPDATE OR IGNORE kb_connectors SET kb = ? WHERE kb = ''",
	} {
		if err := exec(query, kb); err != nil {
			return err
		}
	}
	if err := exec("DELETE FROM reads WHERE kb = ''"); err != nil {
		return err
	}
	if err := exec("DELETE FROM kb_connectors WHERE kb = ''"); err != nil {
		return err
	}
	return tx.Commit()
}

// ForgetKB borra lo que state guarda de la KB kb (lecturas, actividad y repo del conector) al borrarla.
// Los tokens de agente de esa KB los revoca el issue 3, no esta función.
func (s *Store) ForgetKB(kb string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		"DELETE FROM reads WHERE kb = ?",
		"DELETE FROM activity WHERE kb = ?",
		"DELETE FROM kb_connectors WHERE kb = ?",
	} {
		if _, err := tx.Exec(query, kb); err != nil {
			return err
		}
	}
	return tx.Commit()
}
