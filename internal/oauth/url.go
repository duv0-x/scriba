package oauth

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// ClaudeCallback es el redirect_uri de claude.ai, Claude Desktop y la app móvil.
const ClaudeCallback = "https://claude.ai/api/mcp/auth_callback"

// hostOK limita el Host a los caracteres de un nombre, IPv4/IPv6 y puerto: no se puede
// inyectar ruta, query ni HTML en las URLs devueltas.
var hostOK = regexp.MustCompile(`^[A-Za-z0-9.:\-\[\]]+$`)

// BaseURL es la base de las URLs de la instancia: public o, si falta, la deducida de la petición.
func BaseURL(public string, r *http.Request) string {
	if public != "" {
		return strings.TrimRight(public, "/")
	}
	if !hostOK.MatchString(r.Host) {
		return "http://127.0.0.1"
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ResourceMetadataURL es la URL que anuncia el 401 de /mcp.
func ResourceMetadataURL(base string) string { return base + resourcePath + "/mcp" }

func loopback(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || strings.Contains(raw, "#") {
		return nil, false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return u, true
	}
	return nil, false
}

func allowedRedirect(raw string) bool {
	_, local := loopback(raw)
	return local || raw == ClaudeCallback
}

// matches compara redirect_uris: iguales o, en loopback, iguales salvo el puerto (RFC 8252).
func matches(registered, got string) bool {
	if registered == got {
		return true
	}
	a, ok1 := loopback(registered)
	b, ok2 := loopback(got)
	if !ok1 || !ok2 {
		return false
	}
	a.Host, b.Host = a.Hostname(), b.Hostname()
	return a.String() == b.String()
}

// redirectWith añade key=value (y state, si viene) a un redirect_uri ya validado.
func redirectWith(uri, key, value, state string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	q := u.Query()
	q.Set(key, value)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
