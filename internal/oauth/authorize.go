package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/url"
	"slices"
	"time"
)

const codeTTL = 5 * time.Minute

// AuthRequest es una petición de autorización ya validada.
type AuthRequest struct {
	ClientID, ClientName, RedirectURI, State, Challenge, Resource string
}

// grant es lo que representa un authorization code.
type grant struct {
	clientID, redirectURI, challenge, agent, kb string
	expires                                     time.Time
}

// Parse valida los parámetros de /oauth/authorize. Con err no se puede redirigir (el cliente
// o su redirect_uri no valen). Con redirect, lo demás es inválido y hay que volver ahí.
func (s *Server) Parse(v url.Values, base string) (req AuthRequest, redirect string, err error) {
	id := v.Get("client_id")
	c, ok := s.open(id)
	if !ok {
		return req, "", errors.New("cliente desconocido: vuelve a añadir el conector")
	}
	uri := v.Get("redirect_uri")
	if !allowedRedirect(uri) || !slices.ContainsFunc(c.Redirects, func(r string) bool { return matches(r, uri) }) {
		return req, "", errors.New("la dirección de retorno no está registrada para este cliente")
	}
	req = AuthRequest{ClientID: id, ClientName: c.Name, RedirectURI: uri, State: v.Get("state"),
		Challenge: v.Get("code_challenge"), Resource: v.Get("resource")}
	var code string
	switch {
	case v.Get("response_type") != "code":
		code = "unsupported_response_type"
	case v.Get("code_challenge_method") != "S256" || !validChallenge(req.Challenge):
		code = "invalid_request"
	case req.Resource != "" && req.Resource != base+"/mcp":
		code = "invalid_target"
	default:
		return req, "", nil
	}
	return req, redirectWith(uri, "error", code, req.State), nil
}

// Approve emite un code de un solo uso para el agente, ligado a la KB kb, y devuelve a dónde redirigir.
func (s *Server) Approve(req AuthRequest, agent, kb string) string {
	code, now := rand.Text(), s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, g := range s.codes {
		if !now.Before(g.expires) {
			delete(s.codes, k)
		}
	}
	s.codes[code] = grant{req.ClientID, req.RedirectURI, req.Challenge, agent, kb, now.Add(codeTTL)}
	return redirectWith(req.RedirectURI, "code", code, req.State)
}

// Deny devuelve a dónde redirigir cuando el dueño rechaza.
func (s *Server) Deny(req AuthRequest) string {
	return redirectWith(req.RedirectURI, "error", "access_denied", req.State)
}

// take consume el code: sirve una vez, acierte o no el resto.
func (s *Server) take(code string) (grant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.codes[code]
	delete(s.codes, code)
	return g, ok && s.now().Before(g.expires)
}

func validChallenge(c string) bool {
	b, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil && len(b) == sha256.Size
}

// verifyPKCE comprueba S256 (RFC 7636).
func verifyPKCE(challenge, verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}
