package state

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentTokens(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	t0 := time.Unix(1700000000, 0)
	if err := s.InsertAgentToken("claude", []byte("h1"), t0); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertAgentToken("claude", []byte("h2"), t0); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("nombre activo repetido: err=%v", err)
	}
	a, ok, err := s.AgentByHash([]byte("h1"))
	if err != nil || !ok || a.Name != "claude" || !a.Created.Equal(t0) || !a.LastUsed.IsZero() || a.Revoked {
		t.Fatalf("AgentByHash = %+v %v %v", a, ok, err)
	}
	if err := s.TouchAgentToken(a.ID, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := s.AgentByHash([]byte("h1")); !a.LastUsed.Equal(t0.Add(time.Hour)) {
		t.Fatalf("last_used = %v", a.LastUsed)
	}

	if ok, err := s.RevokeAgentToken("claude"); !ok || err != nil {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	if ok, err := s.RevokeAgentToken("claude"); ok || err != nil {
		t.Fatalf("revoke repetido: %v %v", ok, err)
	}
	// Nombre revocado: se reutiliza y el token viejo desaparece.
	if err := s.InsertAgentToken("claude", []byte("h2"), t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.AgentByHash([]byte("h1")); ok {
		t.Fatal("el token revocado sigue en la base")
	}
	if err := s.InsertAgentToken("codex", []byte("h3"), t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	l, err := s.ListAgentTokens()
	if err != nil || len(l) != 2 || l[0].Name != "codex" || l[1].Name != "claude" || l[0].Hash != nil {
		t.Fatalf("List = %+v %v", l, err)
	}
}

func TestActivity(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "state.db"))
	t0 := time.Unix(1700000000, 0)
	for i, p := range []string{"a.md", "b.md", "c.md"} {
		if err := s.AddActivity("a", t0.Add(time.Duration(i)*time.Minute), "claude", "write", p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Activity("a", t0.Add(time.Minute), 10)
	if err != nil || len(got) != 2 || got[0].Path != "c.md" || got[1].Path != "b.md" || !got[0].TS.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("Activity = %+v %v", got, err)
	}
	if got, _ := s.Activity("a", t0, 1); len(got) != 1 || got[0].Path != "c.md" {
		t.Fatalf("limit: %+v", got)
	}
}
