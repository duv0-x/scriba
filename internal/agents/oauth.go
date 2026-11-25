package agents

import (
	"crypto/rand"
	"errors"
	"log/slog"
	"time"

	"github.com/duv0-x/scriba/internal/state"
)

// Concesiones OAuth de los conectores (ADR-010).
const (
	RefreshPrefix = "scr_"
	AccessTTL     = time.Hour
	RefreshTTL    = 90 * 24 * time.Hour
)

// ErrInvalidGrant es el invalid_grant de OAuth.
var ErrInvalidGrant = errors.New("concesión inválida, caducada o revocada")

// Pair son los dos tokens que recibe un conector.
type Pair struct{ Access, Refresh string }

func newGrant(clientID string) (Pair, state.OAuthToken, error) {
	access, err := Generate()
	if err != nil {
		return Pair{}, state.OAuthToken{}, err
	}
	p := Pair{Access: access, Refresh: RefreshPrefix + rand.Text()}
	now := time.Now()
	return p, state.OAuthToken{ClientID: clientID, Hash: Hash(p.Access), RefreshHash: Hash(p.Refresh),
		Now: now, Expires: now.Add(AccessTTL), RefreshExpires: now.Add(RefreshTTL)}, nil
}

// Grant crea la concesión del agente name para el cliente.
func (s Store) Grant(name, clientID string) (Pair, error) {
	if !ValidName(name) {
		return Pair{}, ErrInvalidName
	}
	p, t, err := newGrant(clientID)
	if err != nil {
		return Pair{}, err
	}
	t.Name = name
	if err := s.S.InsertOAuthToken(t); errors.Is(err, state.ErrNameTaken) {
		return Pair{}, ErrInvalidGrant
	} else if err != nil {
		return Pair{}, err
	}
	slog.Info("conector OAuth autorizado", "agente", name)
	return p, nil
}

// Refresh rota los tokens de la concesión; el refresh usado deja de servir.
func (s Store) Refresh(refresh, clientID string) (Pair, error) {
	p, t, err := newGrant(clientID)
	if err != nil {
		return Pair{}, err
	}
	name, reused, err := s.S.RotateOAuthToken(Hash(refresh), t)
	switch {
	case err != nil:
		return Pair{}, err
	case reused:
		slog.Warn("refresh token reutilizado: concesión revocada", "agente", name)
		return Pair{}, ErrInvalidGrant
	case name == "":
		return Pair{}, ErrInvalidGrant
	}
	return p, nil
}

// HasStatic dice si name es un token estático activo: un conector no puede reemplazarlo.
func (s Store) HasStatic(name string) (bool, error) {
	l, err := s.List()
	if err != nil {
		return false, err
	}
	for _, a := range l {
		if a.Name == name && !a.Revoked && a.ClientID == "" {
			return true, nil
		}
	}
	return false, nil
}
