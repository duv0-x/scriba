package state

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T, p string) *Store {
	t.Helper()
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSettings(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	if _, ok, err := s.Setting("initialized"); ok || err != nil {
		t.Fatalf("ausente: ok=%v err=%v", ok, err)
	}
	for _, v := range []string{"1", "2"} {
		if err := s.SetSetting("initialized", v); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := s.Setting("initialized"); got != v || !ok || err != nil {
			t.Fatalf("Setting = %q %v %v, se esperaba %q", got, ok, err, v)
		}
	}
}

func TestConnectorUpsert(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	ts := time.Unix(1700000000, 123)
	c := Connector{Name: "github", Account: "duv0-x", Secret: []byte{0, 1, 2}, Push: true, Updated: ts}
	if err := s.SaveConnector(c); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Connector("github")
	if err != nil || !ok || got.Account != "duv0-x" || !bytes.Equal(got.Secret, c.Secret) || !got.Push || !got.Updated.Equal(ts) {
		t.Fatalf("Connector = %+v %v %v", got, ok, err)
	}

	got.Repo, got.Push, got.LastPush, got.LastError = "duv0-x/ia-kb", false, "abc123", "boom"
	got.Updated = time.Time{}
	if err := s.SaveConnector(got); err != nil {
		t.Fatal(err)
	}
	up, _, err := s.Connector("github")
	if err != nil {
		t.Fatal(err)
	}
	if up.Account != "duv0-x" || !bytes.Equal(up.Secret, c.Secret) {
		t.Fatalf("no preservó campos: %+v", up)
	}
	if up.Repo != "duv0-x/ia-kb" || up.Push || up.LastPush != "abc123" || up.LastError != "boom" || !up.Updated.After(ts) {
		t.Fatalf("no actualizó campos: %+v", up)
	}

	if err := s.DeleteConnector("github"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Connector("github"); ok || err != nil {
		t.Fatalf("tras borrar: ok=%v err=%v", ok, err)
	}
}

func TestReabrirConservaDatos(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveConnector(Connector{Name: "github", Account: "a", Secret: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// La segunda apertura re-ejecuta goose Up: debe ser idempotente.
	s2 := open(t, p)
	if v, ok, err := s2.Setting("k"); v != "v" || !ok || err != nil {
		t.Fatalf("setting tras reabrir: %q %v %v", v, ok, err)
	}
	if c, ok, err := s2.Connector("github"); !ok || err != nil || c.Account != "a" || string(c.Secret) != "x" {
		t.Fatalf("conector tras reabrir: %+v %v %v", c, ok, err)
	}
}

func TestUpdatePushResult(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	if ok, err := s.UpdatePushResult("github", "x", ""); ok || err != nil {
		t.Fatalf("sin conector: ok=%v err=%v", ok, err)
	}
	c := Connector{Name: "github", Account: "a", Secret: []byte{9}, Repo: "a/kb", Push: true}
	if err := s.SaveConnector(c); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdatePushResult("github", "hoy · abc", ""); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ok, err := s.UpdatePushResult("github", "", "boom"); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	got, _, _ := s.Connector("github")
	if got.LastPush != "hoy · abc" || got.LastError != "boom" || got.Repo != "a/kb" || !got.Push || !bytes.Equal(got.Secret, c.Secret) {
		t.Fatalf("conector: %+v", got)
	}
	if err := s.DeleteConnector("github"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdatePushResult("github", "z", ""); ok || err != nil {
		t.Fatalf("tras borrar: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := s.Connector("github"); ok {
		t.Fatal("UpdatePushResult recreó el conector")
	}
}
