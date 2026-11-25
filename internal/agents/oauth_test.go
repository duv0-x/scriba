package agents

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/state"
)

func TestConcesionCaducada(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := Store{S: st}
	p, g, err := newGrant("c")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	g.Name, g.Expires, g.RefreshExpires = "claude", past, past
	if err := st.InsertOAuthToken(g); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Verify(p.Access); ok {
		t.Error("un access token caducado sigue valiendo")
	}
	if _, err := s.Refresh(p.Refresh, "c"); !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("refresh caducado: %v", err)
	}
	if l, _ := s.List(); len(l) != 1 || !l[0].Expired() {
		t.Errorf("lista: %+v", l)
	}
	// El nombre de una concesión caducada se puede reutilizar en un token estático.
	if _, err := s.Create("claude"); err != nil {
		t.Errorf("reutilizar nombre: %v", err)
	}
}
