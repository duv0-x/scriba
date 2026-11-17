// Package agents gestiona los tokens con que los agentes se autentican en /mcp.
// Solo se guarda el SHA-256 del token; el valor en claro se muestra una vez.
package agents

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"regexp"
	"time"

	"github.com/duv0-x/scriba/internal/state"
)

// Prefix identifica los tokens de Scriba (útil para escáneres de secretos).
const Prefix = "scb_"

const (
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	bodyLen  = 43 // 62^43 > 2^256
)

var (
	nameRe         = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	ErrInvalidName = errors.New("nombre inválido: minúsculas, dígitos y guiones, hasta 40 caracteres")
)

// Generate devuelve un token nuevo: Prefix + 43 caracteres base62 de crypto/rand.
func Generate() (string, error) {
	out := make([]byte, 0, len(Prefix)+bodyLen)
	out = append(out, Prefix...)
	buf := make([]byte, 64)
	for len(out) < cap(out) {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			// Rechazo de bytes ≥ 248 (= 4·62) para que no haya sesgo de módulo.
			if b < 248 && len(out) < cap(out) {
				out = append(out, alphabet[b%62])
			}
		}
	}
	return string(out), nil
}

func Hash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func ValidName(name string) bool { return nameRe.MatchString(name) }

// Store opera los tokens sobre state.db.
type Store struct{ S *state.Store }

// Create genera y guarda un token para name; devuelve el valor en claro.
func (s Store) Create(name string) (string, error) {
	if !ValidName(name) {
		return "", ErrInvalidName
	}
	tok, err := Generate()
	if err != nil {
		return "", err
	}
	if err := s.S.InsertAgentToken(name, Hash(tok), time.Now()); err != nil {
		return "", err
	}
	return tok, nil
}

// Verify devuelve el nombre del agente si token es válido y no está revocado.
func (s Store) Verify(token string) (string, bool) {
	if len(token) != len(Prefix)+bodyLen || token[:len(Prefix)] != Prefix {
		return "", false
	}
	h := Hash(token)
	a, ok, err := s.S.AgentByHash(h)
	if err != nil || !ok || subtle.ConstantTimeCompare(a.Hash, h) != 1 || a.Revoked {
		return "", false
	}
	// last_used con resolución de un minuto: evita una escritura por petición.
	if now := time.Now(); now.Sub(a.LastUsed) >= time.Minute {
		_ = s.S.TouchAgentToken(a.ID, now) // si falla, la autenticación sigue siendo válida
	}
	return a.Name, true
}

func (s Store) List() ([]state.AgentToken, error) { return s.S.ListAgentTokens() }

// Revoke revoca el token activo name y dice si existía.
func (s Store) Revoke(name string) (bool, error) { return s.S.RevokeAgentToken(name) }
