// Package oauth implementa la autorización OAuth de la especificación MCP que usan los
// conectores personalizados: metadata, registro dinámico y code + PKCE (ADR-010). La
// pantalla de consentimiento la sirve internal/web, detrás del proxy de autenticación.
package oauth

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/duv0-x/scriba/internal/agents"
	"github.com/duv0-x/scriba/internal/secrets"
)

// AuthorizePath es la única ruta que debe quedar detrás del proxy de autenticación.
const AuthorizePath = "/oauth/authorize"

const (
	tokenPath    = "/oauth/token"
	registerPath = "/oauth/register"
	resourcePath = "/.well-known/oauth-protected-resource"
	serverPath   = "/.well-known/oauth-authorization-server"

	maxBody = 16 << 10
	maxURI  = 512
	maxURIs = 5
	maxName = 80
)

var (
	grantTypes    = []string{"authorization_code", "refresh_token"}
	responseTypes = []string{"code"}
)

// Tokens emite y rota los tokens de un conector (agents.Store).
type Tokens interface {
	Grant(agent, kb, clientID string) (agents.Pair, error)
	Refresh(refresh, clientID string) (agents.Pair, error)
}

// Server es el authorization server de la instancia.
type Server struct {
	tokens Tokens
	public string
	key    []byte
	now    func() time.Time

	mu    sync.Mutex
	codes map[string]grant // authorization codes pendientes; solo en memoria
}

// New deriva de key la subclave que cifra los client_id. publicURL vacía = la de cada petición.
func New(key []byte, tokens Tokens, publicURL string) (*Server, error) {
	sub, err := hkdf.Key(sha256.New, key, nil, "scriba oauth client_id", 32)
	if err != nil {
		return nil, err
	}
	return &Server{tokens: tokens, public: publicURL, key: sub, now: time.Now, codes: map[string]grant{}}, nil
}

// Register monta las rutas públicas (sin sesión: las llama el servidor del conector).
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+resourcePath, s.resource)
	mux.HandleFunc("GET "+resourcePath+"/mcp", s.resource)
	mux.HandleFunc("GET "+serverPath, s.metadata)
	mux.HandleFunc("POST "+registerPath, s.register)
	mux.HandleFunc("POST "+tokenPath, s.token)
}

func (s *Server) resource(w http.ResponseWriter, r *http.Request) {
	base := BaseURL(s.public, r)
	writeJSON(w, http.StatusOK, &oauthex.ProtectedResourceMetadata{
		Resource: base + "/mcp", AuthorizationServers: []string{base},
		BearerMethodsSupported: []string{"header"}, ResourceName: "Scriba",
	})
}

// metadata no usa oauthex.AuthServerMeta porque serializa un jwks_uri vacío.
func (s *Server) metadata(w http.ResponseWriter, r *http.Request) {
	base := BaseURL(s.public, r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + AuthorizePath,
		"token_endpoint":                        base + tokenPath,
		"registration_endpoint":                 base + registerPath,
		"response_types_supported":              responseTypes,
		"grant_types_supported":                 grantTypes,
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	})
}

// client es lo que viaja cifrado dentro del client_id: registrarse no escribe en la base.
type client struct {
	Name      string   `json:"n"`
	Redirects []string `json:"r"`
}

func (s *Server) seal(c client) (string, error) {
	plain, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sealed, err := secrets.Seal(s.key, plain)
	return base64.RawURLEncoding.EncodeToString(sealed), err
}

func (s *Server) open(id string) (client, bool) {
	var c client
	if len(id) > maxBody {
		return c, false
	}
	sealed, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return c, false
	}
	plain, err := secrets.Open(s.key, sealed)
	if err != nil {
		return c, false
	}
	return c, json.Unmarshal(plain, &c) == nil
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string   `json:"client_name"`
		Redirects []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "JSON inválido")
		return
	}
	c := client{Name: in.Name}
	if c.Name == "" {
		c.Name = "Cliente sin nombre"
	}
	if n := []rune(c.Name); len(n) > maxName {
		c.Name = string(n[:maxName])
	}
	// RFC 7591 permite al servidor recortar la metadata: se guardan solo los redirects admitidos.
	for _, u := range in.Redirects {
		if len(u) <= maxURI && len(c.Redirects) < maxURIs && allowedRedirect(u) {
			c.Redirects = append(c.Redirects, u)
		}
	}
	if len(c.Redirects) == 0 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "solo se admiten "+ClaudeCallback+" y http en loopback")
		return
	}
	id, err := s.seal(c)
	if err != nil {
		slog.Error("oauth: registro", "err", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "error interno")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id": id, "client_id_issued_at": s.now().Unix(), "client_name": c.Name,
		"redirect_uris": c.Redirects, "token_endpoint_auth_method": "none",
		"grant_types": grantTypes, "response_types": responseTypes,
	})
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "formulario inválido")
		return
	}
	f := r.PostForm
	id := f.Get("client_id")
	// Se valida antes de consumir el code: algunos clientes prueban primero con Basic.
	if _, ok := s.open(id); !ok {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "client_id desconocido")
		return
	}
	if res := f.Get("resource"); res != "" && res != BaseURL(s.public, r)+"/mcp" {
		oauthError(w, http.StatusBadRequest, "invalid_target", "resource desconocido")
		return
	}
	var pair agents.Pair
	var err error
	switch f.Get("grant_type") {
	case "authorization_code":
		g, ok := s.take(f.Get("code"))
		if ok && g.clientID == id && g.redirectURI == f.Get("redirect_uri") && verifyPKCE(g.challenge, f.Get("code_verifier")) {
			pair, err = s.tokens.Grant(g.agent, g.kb, id)
		} else {
			err = agents.ErrInvalidGrant
		}
	case "refresh_token":
		pair, err = s.tokens.Refresh(f.Get("refresh_token"), id)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type no soportado")
		return
	}
	switch {
	case errors.Is(err, agents.ErrInvalidGrant):
		oauthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
	case err != nil:
		slog.Error("oauth: token", "err", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "error interno")
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": pair.Access, "token_type": "Bearer",
			"expires_in": int(agents.AccessTTL.Seconds()), "refresh_token": pair.Refresh,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // la cabecera ya salió: no hay a quién avisar
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}
