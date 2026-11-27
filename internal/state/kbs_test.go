package state

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestAdoptKBBackfill(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	dsn := (&url.URL{Scheme: "file", OmitHost: true, Path: p,
		RawQuery: "_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=synchronous(normal)"}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	prov, err := goose.NewProvider(goose.DialectSQLite3, db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.UpTo(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1700000000, 0)
	statements := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO agent_tokens(name, hash, created) VALUES(?, ?, ?)", []any{"viejo", []byte("h1"), t0.UnixNano()}},
		{"INSERT INTO activity(ts, agent, action, path) VALUES(?, ?, ?, ?)", []any{t0.UnixNano(), "claude", "write", "a.md"}},
		{"INSERT INTO activity(ts, agent, action, path) VALUES(?, ?, ?, ?)", []any{t0.Add(time.Minute).UnixNano(), "codex", "read", "b.md"}},
		{"INSERT INTO reads(user, path, hash, content, at) VALUES(?, ?, ?, ?, ?)", []any{"ana", "a.md", "viejo", "contenido viejo", t0.UnixNano()}},
		{"INSERT INTO reads(user, path, hash, content, at) VALUES(?, ?, ?, ?, ?)", []any{"beto", "b.md", "h2", "dos", t0.UnixNano()}},
		{"INSERT INTO connectors(name, account, secret, repo, push, last_push, last_error, updated) VALUES(?, ?, ?, ?, ?, ?, ?, ?)", []any{"github", "duv0-x", []byte("secreto"), "duv0-x/ia-kb", false, "x", "", t0.UnixNano()}},
		{"INSERT INTO connectors(name, account, secret, repo, push, last_push, last_error, updated) VALUES(?, ?, ?, ?, ?, ?, ?, ?)", []any{"kindle", "kindle", []byte("k"), "from@x", true, "", "", t0.UnixNano()}},
	}
	for _, st := range statements {
		if _, err := db.Exec(st.query, st.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := open(t, p)
	c, ok, err := s.KBConnector("github", "")
	if err != nil || !ok || c.Repo != "duv0-x/ia-kb" || c.Push || c.LastPush != "x" {
		t.Fatalf("conector antes de adoptar: %+v %v %v", c, ok, err)
	}
	if err := s.MarkRead("ia-kb", "ana", "a.md", "nuevo", "contenido actual", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.AdoptKB("ia-kb"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.DefaultKB(); err != nil || !ok || got != "ia-kb" {
		t.Fatalf("DefaultKB = %q %v %v", got, ok, err)
	}
	checkRows := func() {
		for table, want := range map[string]int{"agent_tokens": 1, "activity": 2, "reads": 2, "kb_connectors": 1} {
			var total, adopted, blank int
			query := fmt.Sprintf("SELECT COUNT(*), COALESCE(SUM(kb = 'ia-kb'), 0), COALESCE(SUM(kb = ''), 0) FROM %s", table)
			if err := s.db.QueryRow(query).Scan(&total, &adopted, &blank); err != nil {
				t.Fatal(err)
			}
			if total != want || adopted != want || blank != 0 {
				t.Fatalf("%s: total=%d adoptadas=%d sin_kb=%d", table, total, adopted, blank)
			}
		}
	}
	checkRows()
	if err := s.AdoptKB("ia-kb"); err != nil {
		t.Fatal(err)
	}
	checkRows()

	c, ok, err = s.KBConnector("github", "ia-kb")
	if err != nil || !ok || c.Account != "duv0-x" || !bytes.Equal(c.Secret, []byte("secreto")) || c.Repo != "duv0-x/ia-kb" || c.Push || c.LastPush != "x" {
		t.Fatalf("conector adoptado: %+v %v %v", c, ok, err)
	}
	if kindle, ok, err := s.Connector("kindle"); err != nil || !ok || kindle.Repo != "from@x" {
		t.Fatalf("kindle: %+v %v %v", kindle, ok, err)
	}
	if r, ok, err := s.LastRead("ia-kb", "ana", "a.md"); err != nil || !ok || r.Hash != "nuevo" || r.Content != "contenido actual" {
		t.Fatalf("lectura con conflicto: %+v %v %v", r, ok, err)
	}
	if r, ok, err := s.LastRead("ia-kb", "beto", "b.md"); err != nil || !ok || r.Hash != "h2" {
		t.Fatalf("lectura migrada: %+v %v %v", r, ok, err)
	}

	if err := s.InsertAgentToken("nuevo", "", []byte("h3"), t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertOAuthToken(OAuthToken{Name: "oauth", Hash: []byte("h4"), Now: t0, ClientID: "cliente", Expires: t0.Add(time.Hour), RefreshHash: []byte("r4"), RefreshExpires: t0.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertAgentToken("otro", "trabajo", []byte("h5"), t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"nuevo": "ia-kb", "oauth": "ia-kb", "otro": "trabajo"} {
		var kb string
		if err := s.db.QueryRow("SELECT kb FROM agent_tokens WHERE name = ?", name).Scan(&kb); err != nil || kb != want {
			t.Fatalf("KB del token %s: %q %v", name, kb, err)
		}
	}
	if a, ok, err := s.AgentByHash([]byte("h5")); err != nil || !ok || a.KB != "trabajo" {
		t.Fatalf("AgentByHash KB: %+v %v %v", a, ok, err)
	}
}

func TestKBConnectors(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	ts := time.Unix(1700000000, 123)
	legacy := Connector{Name: "github", Account: "vieja", Secret: []byte("old"), Repo: "legacy", Push: false, LastPush: "legacy-push", LastError: "legacy-error", Updated: ts.Add(-time.Hour)}
	if err := s.SaveConnector(legacy); err != nil {
		t.Fatal(err)
	}
	a := Connector{Name: "github", Account: "duv0-x", Secret: []byte("secreto"), Repo: "duv0-x/a", Push: true, LastPush: "push-a", Updated: ts}
	if err := s.SaveKBConnector("a", a); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := s.KBConnector("github", "b"); err != nil || !ok || c.Repo != "" || !c.Push || c.LastPush != "" || c.LastError != "" {
		t.Fatalf("valores sin fila KB: %+v %v %v", c, ok, err)
	}
	b := a
	b.Repo, b.Push, b.LastPush, b.LastError = "duv0-x/b", false, "push-b", ""
	if err := s.SaveKBConnector("b", b); err != nil {
		t.Fatal(err)
	}
	for kb, repo := range map[string]string{"a": "duv0-x/a", "b": "duv0-x/b"} {
		c, ok, err := s.KBConnector("github", kb)
		if err != nil || !ok || c.Account != "duv0-x" || !bytes.Equal(c.Secret, []byte("secreto")) || c.Repo != repo || !c.Updated.Equal(ts) {
			t.Fatalf("conector %s: %+v %v %v", kb, c, ok, err)
		}
	}
	if c, ok, err := s.Connector("github"); err != nil || !ok || c.Account != "duv0-x" || c.Repo != "legacy" || c.Push || c.LastPush != "legacy-push" || c.LastError != "legacy-error" {
		t.Fatalf("parte de instancia: %+v %v %v", c, ok, err)
	}

	if ok, err := s.UpdatePushResult("github", "b", "push-b-2", "boom"); !ok || err != nil {
		t.Fatalf("actualizar b: %v %v", ok, err)
	}
	if c, _, _ := s.KBConnector("github", "b"); c.LastPush != "push-b-2" || c.LastError != "boom" {
		t.Fatalf("push b: %+v", c)
	}
	if c, _, _ := s.KBConnector("github", "a"); c.LastPush != "push-a" || c.LastError != "" {
		t.Fatalf("push a cambió: %+v", c)
	}
	if c, _, _ := s.Connector("github"); c.LastPush != "legacy-push" || c.LastError != "legacy-error" {
		t.Fatalf("resultado se guardó en connectors: %+v", c)
	}
	if ok, err := s.UpdatePushResult("github", "c", "x", ""); ok || err != nil {
		t.Fatalf("KB sin fila: %v %v", ok, err)
	}
	if err := s.DeleteConnector("github"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM kb_connectors WHERE name = 'github'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("filas KB tras borrar: %d %v", n, err)
	}
}

func TestForgetKB(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	t0 := time.Unix(1700000000, 0)
	for _, kb := range []string{"a", "b"} {
		if err := s.SaveKBConnector(kb, Connector{Name: "github", Account: "duv0-x", Repo: "duv0-x/" + kb}); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkRead(kb, "ana", "a.md", "h-"+kb, kb, t0); err != nil {
			t.Fatal(err)
		}
		if err := s.AddActivity(kb, t0, "claude", "write", "a.md"); err != nil {
			t.Fatal(err)
		}
		if err := s.InsertAgentToken("tok-"+kb, kb, []byte("h-"+kb), t0); err != nil {
			t.Fatal(err)
		}
		if err := s.InsertOAuthToken(OAuthToken{Name: "oauth-" + kb, KB: kb, ClientID: "c", Hash: []byte("o-" + kb), RefreshHash: []byte("r-" + kb), Now: t0, Expires: t0.Add(time.Hour), RefreshExpires: t0.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ForgetKB("b"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"reads", "activity", "kb_connectors"} {
		var a, b int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE kb = 'a'").Scan(&a); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE kb = 'b'").Scan(&b); err != nil {
			t.Fatal(err)
		}
		if a != 1 || b != 0 {
			t.Fatalf("%s: a=%d b=%d", table, a, b)
		}
	}
	for h, revoked := range map[string]bool{"h-a": false, "o-a": false, "h-b": true, "o-b": true} {
		if a, ok, err := s.AgentByHash([]byte(h)); err != nil || !ok || a.Revoked != revoked {
			t.Fatalf("token %s: %+v %v %v (revocado esperado %v)", h, a, ok, err, revoked)
		}
	}
}

func TestReadsKB(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	t0 := time.Unix(1700000000, 0)
	if err := s.MarkRead("a", "ana", "a.md", "ha", "a", t0); err != nil {
		t.Fatal(err)
	}
	if hashes, err := s.ReadHashes("b", "ana"); err != nil || len(hashes) != 0 {
		t.Fatalf("lectura de a visible en b: %v %v", hashes, err)
	}
	if err := s.MarkRead("b", "ana", "a.md", "hb", "b", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetPath("a", "a.md"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.LastRead("a", "ana", "a.md"); ok || err != nil {
		t.Fatalf("a conserva la lectura: %v %v", ok, err)
	}
	if r, ok, err := s.LastRead("b", "ana", "a.md"); err != nil || !ok || r.Hash != "hb" {
		t.Fatalf("b perdió la lectura: %+v %v %v", r, ok, err)
	}
}
