package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReads(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	if _, ok, err := s.LastRead("ana", "a.md"); ok || err != nil {
		t.Fatalf("ausente: ok=%v err=%v", ok, err)
	}
	t1 := time.Unix(1700000000, 5)
	if err := s.MarkRead("ana", "a.md", "h1", "uno", t1); err != nil {
		t.Fatal(err)
	}
	t2 := t1.Add(time.Hour)
	if err := s.MarkRead("ana", "a.md", "h2", "dos", t2); err != nil { // upsert
		t.Fatal(err)
	}
	if err := s.MarkRead("ana", "b.md", "hb", "b", t1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRead("beto", "a.md", "hx", "x", t1); err != nil {
		t.Fatal(err)
	}
	r, ok, err := s.LastRead("ana", "a.md")
	if err != nil || !ok || r.Hash != "h2" || r.Content != "dos" || !r.At.Equal(t2) {
		t.Fatalf("LastRead = %+v %v %v", r, ok, err)
	}
	m, err := s.ReadHashes("ana")
	if err != nil || len(m) != 2 || m["a.md"] != "h2" || m["b.md"] != "hb" {
		t.Fatalf("ReadHashes = %v %v", m, err)
	}
	if m, err := s.ReadHashes("nadie"); err != nil || len(m) != 0 {
		t.Fatalf("ReadHashes(nadie) = %v %v", m, err)
	}
}

func TestBaseline(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	t1 := time.Unix(1700000000, 7)
	got, err := s.Baseline("ana", t1)
	if err != nil || !got.Equal(t1) {
		t.Fatalf("Baseline primera = %v %v", got, err)
	}
	got, err = s.Baseline("ana", t1.Add(time.Hour)) // no se mueve
	if err != nil || !got.Equal(t1) {
		t.Fatalf("Baseline segunda = %v %v", got, err)
	}
	if got, err := s.Baseline("beto", t1.Add(time.Hour)); err != nil || !got.Equal(t1.Add(time.Hour)) {
		t.Fatalf("Baseline beto = %v %v", got, err)
	}
}

func TestClearBaselines(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	t1 := time.Unix(1700000000, 7)
	for _, u := range []string{"ana", "beto"} {
		if _, err := s.Baseline(u, t1); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetSetting("otra", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearBaselines(); err != nil {
		t.Fatal(err)
	}
	t2 := t1.Add(time.Hour)
	if got, err := s.Baseline("ana", t2); err != nil || !got.Equal(t2) {
		t.Fatalf("Baseline tras limpiar = %v %v", got, err)
	}
	if v, ok, err := s.Setting("otra"); err != nil || !ok || v != "x" {
		t.Fatalf("se borró otro ajuste: %q %v %v", v, ok, err)
	}
}

func TestForgetPath(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	t1 := time.Unix(1700000000, 0)
	for _, r := range [][2]string{{"ana", "a.md"}, {"beto", "a.md"}, {"ana", "b.md"}} {
		if err := s.MarkRead(r[0], r[1], "h", "c", t1); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ForgetPath("a.md"); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"ana", "beto"} {
		if _, ok, err := s.LastRead(u, "a.md"); ok || err != nil {
			t.Fatalf("%s conserva a.md: %v %v", u, ok, err)
		}
	}
	if _, ok, err := s.LastRead("ana", "b.md"); !ok || err != nil {
		t.Fatalf("se borró b.md: %v %v", ok, err)
	}
}
