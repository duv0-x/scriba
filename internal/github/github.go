// Package github es un cliente mínimo de GitHub: OAuth device flow, usuario y
// listado de repositorios. Solo usa la stdlib. El token nunca aparece en errores.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultClientID es el client_id (público) de la OAuth App "Scriba" de duv0-x, con
// Device Flow y sin caducidad de tokens (Scriba aún no usa el refresh_token). Otra
// instancia puede usar su propia app con --github-client-id.
const DefaultClientID = "Ov23ligMXMCVEmn5DJeK"

// Errores centinela de PollToken.
var (
	ErrPending  = errors.New("github: autorización pendiente")
	ErrSlowDown = errors.New("github: slow_down, sumar 5 s al intervalo") // el llamador suma 5 s
	ErrExpired  = errors.New("github: el código de dispositivo expiró")
	ErrDenied   = errors.New("github: el usuario denegó el acceso")
)

// Client habla con github.com (WebURL) y api.github.com (APIURL); ambos se
// inyectan en tests.
type Client struct {
	HTTP   *http.Client
	WebURL string
	APIURL string
}

// New devuelve un cliente contra GitHub público con timeout de 15 s.
func New() *Client {
	return &Client{
		HTTP:   &http.Client{Timeout: 15 * time.Second},
		WebURL: "https://github.com",
		APIURL: "https://api.github.com",
	}
}

// DeviceCode es la respuesta de /login/device/code.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// Token es el access token obtenido por device flow.
type Token struct {
	AccessToken string
	Scope       string
}

// Repo es un repositorio accesible por el usuario.
type Repo struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	CloneURL      string `json:"clone_url"`
}

// StartDevice inicia el device flow y devuelve el código a mostrar al usuario.
func (c *Client) StartDevice(ctx context.Context, clientID, scope string) (DeviceCode, error) {
	var dc DeviceCode
	if clientID == "" {
		return dc, errors.New("github: falta el client_id de la OAuth App de Scriba")
	}
	var raw struct {
		DeviceCode
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := c.postForm(ctx, "/login/device/code", url.Values{"client_id": {clientID}, "scope": {scope}}, &raw); err != nil {
		return dc, err
	}
	if raw.Error != "" {
		return dc, fmt.Errorf("github: device code: %s: %s", raw.Error, raw.ErrorDescription)
	}
	if raw.DeviceCode.DeviceCode == "" {
		return dc, errors.New("github: respuesta de device code sin device_code")
	}
	return raw.DeviceCode, nil
}

// PollToken consulta una vez si el usuario ya autorizó. Devuelve ErrPending,
// ErrSlowDown, ErrExpired o ErrDenied según el estado del flujo.
func (c *Client) PollToken(ctx context.Context, clientID, deviceCode string) (Token, error) {
	var raw struct {
		AccessToken      string `json:"access_token"`
		Scope            string `json:"scope"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	form := url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	if err := c.postForm(ctx, "/login/oauth/access_token", form, &raw); err != nil {
		return Token{}, err
	}
	switch raw.Error {
	case "":
		if raw.AccessToken == "" {
			return Token{}, errors.New("github: respuesta sin access_token")
		}
		return Token{AccessToken: raw.AccessToken, Scope: raw.Scope}, nil
	case "authorization_pending":
		return Token{}, ErrPending
	case "slow_down":
		return Token{}, ErrSlowDown
	case "expired_token":
		return Token{}, ErrExpired
	case "access_denied":
		return Token{}, ErrDenied
	default:
		return Token{}, fmt.Errorf("github: %s: %s", raw.Error, raw.ErrorDescription)
	}
}

// User devuelve el login del dueño del token.
func (c *Client) User(ctx context.Context, token string) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	if _, err := c.getAPI(ctx, token, c.APIURL+"/user", &u); err != nil {
		return "", err
	}
	return u.Login, nil
}

const maxRepoPages = 10

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// Repos lista los repos del usuario (propios, de colaborador y de sus orgs),
// siguiendo Link rel="next" hasta 10 páginas de 100.
func (c *Client) Repos(ctx context.Context, token string) ([]Repo, error) {
	next := c.APIURL + "/user/repos?per_page=100&sort=updated&affiliation=owner,collaborator,organization_member"
	var all []Repo
	for page := 0; page < maxRepoPages && next != ""; page++ {
		var batch []Repo
		h, err := c.getAPI(ctx, token, next, &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		next = ""
		if m := nextLink.FindStringSubmatch(h.Get("Link")); m != nil {
			// Solo seguimos enlaces a la misma API: el token no debe salir a otro host.
			if !strings.HasPrefix(m[1], c.APIURL+"/") {
				return nil, errors.New("github: Link rel=next apunta fuera de la API")
			}
			next = m[1]
		}
	}
	return all, nil
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.WebURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "scriba")
	_, err = c.do(req, path, out)
	return err
}

func (c *Client) getAPI(ctx context.Context, token, rawURL string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "scriba")
	return c.do(req, req.URL.Path, out)
}

// do ejecuta la petición y decodifica JSON. Los errores solo citan el path,
// nunca headers ni cuerpo de la petición (ahí viaja el token).
func (c *Client) do(req *http.Request, path string, out any) (http.Header, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// *url.Error incluye la URL; la nuestra no lleva token, pero recortamos igual.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("github: %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("github: %s: leyendo respuesta: %w", path, err)
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &e)
		return nil, fmt.Errorf("github: %s: HTTP %d: %s", path, resp.StatusCode, e.Message)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("github: %s: JSON inválido: %w", path, err)
	}
	return resp.Header, nil
}
