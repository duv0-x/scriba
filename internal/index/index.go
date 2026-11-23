// Package index mantiene el índice SQLite + FTS5 derivado del vault (ADR-003).
// Es descartable: Rebuild lo reconstruye entero desde los .md.
package index

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/url"
	"path"
	"strings"

	"github.com/ncruces/go-sqlite3"
	_ "github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
	"github.com/pressly/goose/v3"

	"github.com/duv0-x/scriba/internal/vault"
)

//go:embed migrations/*.sql
var migrations embed.FS

// FTS5 NO viene compilado en el SQLite embebido de ncruces v0.35.6: sin registrar,
// "CREATE VIRTUAL TABLE ... USING fts5" falla con "no such module: fts5" (comprobado).
// El paquete ext/fts5 lo carga; AutoExtension lo aplica a cada conexión que abra database/sql.
func init() { sqlite3.AutoExtension(fts5.Register) }

// Index es el índice derivado sobre una base SQLite.
type Index struct{ db *sql.DB }

// Hit es un resultado de búsqueda; Snippet ya es HTML seguro (solo <mark>).
type Hit struct{ Path, Title, Snippet string }

// NoteRef es la referencia mínima a una nota para listados.
type NoteRef struct{ Path, Title, Type, Date string }

// Link es un enlace saliente; Exists indica si resolvió a una nota.
type Link struct {
	Raw, Dst string
	Exists   bool
}

// Open abre (o crea) la base en path y aplica las migraciones embebidas.
func Open(p string) (*Index, error) {
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
		return nil, fmt.Errorf("migrar índice: %w", err)
	}
	return &Index{db: db}, nil
}

func (ix *Index) Close() error { return ix.db.Close() }

func (ix *Index) Ping() error { return ix.db.Ping() }

// Rebuild vacía el índice y lo llena desde el vault en una sola transacción.
func (ix *Index) Rebuild(v *vault.Vault) error {
	return ix.inTx(func(tx *sql.Tx) error {
		for _, q := range []string{"DELETE FROM notes", "DELETE FROM links", "DELETE FROM notes_fts"} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		err := v.Walk(func(rel string) error {
			n, err := v.Read(rel)
			if err != nil {
				slog.Warn("índice: nota ilegible, se salta", "path", rel, "err", err)
				return nil
			}
			return insertNote(tx, n)
		})
		if err != nil {
			return err
		}
		return resolveLinks(tx)
	})
}

// Upsert reemplaza una nota (fila, texto FTS y links) y re-resuelve los links.
func (ix *Index) Upsert(n *vault.Note) error {
	return ix.inTx(func(tx *sql.Tx) error {
		if err := deleteNote(tx, n.Path); err != nil {
			return err
		}
		if err := insertNote(tx, n); err != nil {
			return err
		}
		return resolveLinks(tx)
	})
}

// Delete quita una nota del índice y re-resuelve los links que apuntaban a ella.
func (ix *Index) Delete(p string) error {
	return ix.inTx(func(tx *sql.Tx) error {
		if err := deleteNote(tx, p); err != nil {
			return err
		}
		return resolveLinks(tx)
	})
}

// ResolveLinks recalcula links.dst para todos los links.
func (ix *Index) ResolveLinks() error { return ix.inTx(resolveLinks) }

func (ix *Index) inTx(fn func(*sql.Tx) error) error {
	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func deleteNote(tx *sql.Tx, p string) error {
	for _, q := range []string{"DELETE FROM notes WHERE path = ?", "DELETE FROM notes_fts WHERE path = ?", "DELETE FROM links WHERE src = ?"} {
		if _, err := tx.Exec(q, p); err != nil {
			return err
		}
	}
	return nil
}

func insertNote(tx *sql.Tx, n *vault.Note) error {
	tags := strings.Join(n.Tags, ", ")
	if _, err := tx.Exec(`INSERT INTO notes (path, title, type, date, tags, words, mtime) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		n.Path, n.Title, n.Type, n.Date, tags, n.Words, n.ModTime.Unix()); err != nil {
		return fmt.Errorf("insertar %s: %w", n.Path, err)
	}
	if _, err := tx.Exec(`INSERT INTO notes_fts (path, title, body, tags) VALUES (?, ?, ?, ?)`,
		n.Path, n.Title, n.Body, tags); err != nil {
		return fmt.Errorf("indexar %s: %w", n.Path, err)
	}
	for _, raw := range n.Links {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO links (src, raw) VALUES (?, ?)`, n.Path, raw); err != nil {
			return err
		}
	}
	return nil
}

// resolveLinks: raw casa (case-insensitive) con el nombre base sin ".md" o con la ruta sin ".md";
// entre varias candidatas gana la ruta más corta y, a igual longitud, la menor léxicamente.
func resolveLinks(tx *sql.Tx) error {
	paths, err := queryStrings(tx, "SELECT path FROM notes")
	if err != nil {
		return err
	}
	best := map[string]string{}
	better := func(a, b string) bool { return len(a) < len(b) || (len(a) == len(b) && a < b) }
	for _, p := range paths {
		noExt := strings.TrimSuffix(p, ".md")
		for _, k := range []string{strings.ToLower(noExt), strings.ToLower(path.Base(noExt))} {
			if cur, ok := best[k]; !ok || better(p, cur) {
				best[k] = p
			}
		}
	}
	raws, err := queryStrings(tx, "SELECT DISTINCT raw FROM links")
	if err != nil {
		return err
	}
	for _, raw := range raws {
		var dst any // nil → NULL
		if p, ok := best[strings.ToLower(raw)]; ok {
			dst = p
		}
		if _, err := tx.Exec("UPDATE links SET dst = ? WHERE raw = ?", dst, raw); err != nil {
			return err
		}
	}
	return nil
}

type querier interface {
	Query(string, ...any) (*sql.Rows, error)
}

func queryStrings(db querier, q string) ([]string, error) {
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

var searchStrip = strings.NewReplacer(
	`"`, " ", "*", " ", "(", " ", ")", " ", ":", " ", "^", " ", "-", " ", "+", " ",
	"{", " ", "}", " ", "[", " ", "]", " ", "<", " ", ">", " ", ",", " ", ".", " ",
	";", " ", "!", " ", "?", " ", "'", " ", "`", " ", "“", " ", "”", " ", "‘", " ", "’", " ",
)

// Search busca por prefijo con AND implícito entre términos, ordenado por bm25.
func (ix *Index) Search(q string, limit int) ([]Hit, error) {
	terms := strings.Fields(searchStrip.Replace(q))
	if len(terms) == 0 {
		return []Hit{}, nil
	}
	for i, t := range terms {
		terms[i] = `"` + t + `"*`
	}
	if limit <= 0 {
		limit = 20
	}
	// \x02/\x03 como marcas: sobreviven a html.EscapeString y luego se vuelven <mark>.
	rows, err := ix.db.Query(`SELECT n.path, n.title, snippet(notes_fts, 2, char(2), char(3), '…', 12)
		FROM notes_fts JOIN notes n ON n.path = notes_fts.path
		WHERE notes_fts MATCH ? ORDER BY bm25(notes_fts) LIMIT ?`, strings.Join(terms, " "), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	marks := strings.NewReplacer("\x02", "<mark>", "\x03", "</mark>")
	hits := []Hit{}
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Path, &h.Title, &h.Snippet); err != nil {
			return nil, err
		}
		h.Snippet = marks.Replace(html.EscapeString(h.Snippet))
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

func (ix *Index) refs(q string, args ...any) ([]NoteRef, error) {
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NoteRef{}
	for rows.Next() {
		var r NoteRef
		if err := rows.Scan(&r.Path, &r.Title, &r.Type, &r.Date); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Backlinks devuelve las notas que enlazan a path, ordenadas por título.
func (ix *Index) Backlinks(p string) ([]NoteRef, error) {
	return ix.refs(`SELECT DISTINCT n.path, n.title, n.type, n.date FROM links l JOIN notes n ON n.path = l.src
		WHERE l.dst = ? ORDER BY n.title, n.path`, p)
}

// Recent devuelve las notas por mtime descendente.
func (ix *Index) Recent(limit int) ([]NoteRef, error) {
	return ix.refs(`SELECT path, title, type, date FROM notes ORDER BY mtime DESC, path LIMIT ?`, limit)
}

// Outlinks devuelve los links de path en orden de inserción.
func (ix *Index) Outlinks(p string) ([]Link, error) {
	rows, err := ix.db.Query(`SELECT raw, dst FROM links WHERE src = ? ORDER BY rowid`, p)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		var dst sql.NullString
		if err := rows.Scan(&l.Raw, &dst); err != nil {
			return nil, err
		}
		l.Dst, l.Exists = dst.String, dst.Valid
		out = append(out, l)
	}
	return out, rows.Err()
}

// Edges devuelve todos los links resueltos (src, dst) en una sola consulta.
func (ix *Index) Edges() ([][2]string, error) {
	rows, err := ix.db.Query(`SELECT src, dst FROM links WHERE dst IS NOT NULL ORDER BY src, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var e [2]string
		if err := rows.Scan(&e[0], &e[1]); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Counts devuelve el número de notas por type.
func (ix *Index) Counts() (map[string]int, error) {
	rows, err := ix.db.Query(`SELECT type, count(*) FROM notes GROUP BY type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var c int
		if err := rows.Scan(&t, &c); err != nil {
			return nil, err
		}
		out[t] = c
	}
	return out, rows.Err()
}

// Refs devuelve todas las notas ordenadas por path (para el árbol de la sidebar).
func (ix *Index) Refs() ([]NoteRef, error) {
	return ix.refs(`SELECT path, title, type, date FROM notes ORDER BY path`)
}

// Tree devuelve todos los paths ordenados.
func (ix *Index) Tree() ([]string, error) {
	return queryStrings(ix.db, `SELECT path FROM notes ORDER BY path`)
}

func (ix *Index) NoteCount() (n int, err error) {
	err = ix.db.QueryRow(`SELECT count(*) FROM notes`).Scan(&n)
	return n, err
}
