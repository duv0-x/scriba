// Package mcp expone el vault a los agentes de IA como servidor MCP (streamable HTTP)
// detrás de un token Bearer por agente (ADR-004).
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/oauth"
	"github.com/duv0-x/scriba/internal/vault"
	"github.com/duv0-x/scriba/internal/writer"
)

// Config son las dependencias del servidor MCP.
type Config struct {
	V      *vault.Vault
	Ix     *index.Index
	W      *writer.Writer
	Agents interface {
		Verify(token string) (string, bool)
	}
	PublicURL string           // base para kb_open y las URLs devueltas
	Now       func() time.Time // nil = time.Now
	Version   string
}

const instructions = `Scriba es una base de conocimiento en Markdown (frontmatter YAML) versionada en git, escrita por agentes de IA y leída por personas en un lector web.
Las notas se organizan por tipo en carpetas (journal/, tickets/, plans/, decisions/, runbooks/…) y se enlazan con [[wikilinks]].
Antes de escribir, busca con kb_search (y lee con kb_read) para no duplicar ni contradecir notas existentes.
Cada escritura queda como un commit a tu nombre; para el registro del día usa kb_log_journal.`

type agentKey struct{}

// baseKey es la clave de TokenInfo.Extra con la base de las URLs de la petición.
const baseKey = "base"

// baseURL es la base de las URLs devueltas: PublicURL o, si falta, la deducida de la petición.
func (cfg Config) baseURL(r *http.Request) string { return oauth.BaseURL(cfg.PublicURL, r) }

// Handler devuelve el endpoint MCP: sin un Bearer válido responde 401 sin llegar al SDK.
func Handler(cfg Config) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	srv := newServer(cfg)
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		// Detrás del túnel de Cloudflare el Host no es localhost; el Bearer ya protege del DNS rebinding.
		// Las sesiones inactivas se cierran para no acumularlas en memoria.
		&sdk.StreamableHTTPOptions{DisableLocalhostProtection: true, SessionTimeout: 30 * time.Minute})
	// El middleware del SDK mete el agente en TokenInfo.UserID: llega a cada tool en
	// req.Extra.TokenInfo y además ata cada sesión MCP al agente que la abrió.
	withInfo := auth.RequireBearerToken(func(ctx context.Context, _ string, r *http.Request) (*auth.TokenInfo, error) {
		name, _ := ctx.Value(agentKey{}).(string)
		// La base viaja en TokenInfo porque es lo único por petición que llega a las tools
		// (su ctx es el de la sesión, no el de la petición HTTP).
		return &auth.TokenInfo{UserID: name, Extra: map[string]any{baseKey: cfg.baseURL(r)}}, nil
	}, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		name, ok := "", false
		presented := strings.EqualFold(scheme, "Bearer") && token != ""
		if presented {
			name, ok = cfg.Agents.Verify(strings.TrimSpace(token))
		}
		if !ok {
			// RFC 9728: el 401 dice dónde descubrir el authorization server (ADR-010).
			challenge := fmt.Sprintf("Bearer resource_metadata=%q", oauth.ResourceMetadataURL(cfg.baseURL(r)))
			if presented {
				challenge += `, error="invalid_token"`
			}
			w.Header().Set("WWW-Authenticate", challenge)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"token inválido"}`))
			return
		}
		withInfo.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), agentKey{}, name)))
	})
}

// agentOf saca el nombre del agente de la petición MCP.
func agentOf(req *sdk.CallToolRequest) (string, error) {
	if req.Extra == nil || req.Extra.TokenInfo == nil || req.Extra.TokenInfo.UserID == "" {
		return "", errors.New("petición sin agente autenticado")
	}
	return req.Extra.TokenInfo.UserID, nil
}

// Entradas y salidas de las tools (el SDK infiere el jsonschema; sin omitempty = obligatorio).
type (
	searchIn struct {
		Query string `json:"query" jsonschema:"texto a buscar; varios términos se combinan con AND y cada uno cuenta como prefijo"`
		Limit int    `json:"limit,omitempty" jsonschema:"máximo de resultados (1 a 50, por defecto 10)"`
	}
	searchHit struct {
		Path    string `json:"path"`
		Title   string `json:"title"`
		Snippet string `json:"snippet"`
	}
	searchOut struct {
		Hits []searchHit `json:"hits"`
	}
	pathIn struct {
		Path string `json:"path" jsonschema:"ruta de la nota relativa a la raíz del vault, con '/' y terminada en .md (p. ej. decisions/personal/adr-001.md)"`
	}
	readOut struct {
		Path      string   `json:"path"`
		Title     string   `json:"title"`
		Type      string   `json:"type"`
		Date      string   `json:"date"`
		Tags      []string `json:"tags"`
		Content   string   `json:"content"`
		Backlinks []string `json:"backlinks"`
	}
	listIn struct {
		Folder string `json:"folder,omitempty" jsonschema:"carpeta relativa a la raíz (p. ej. decisions o tickets/personal); vacía = todo el vault"`
	}
	listNote struct {
		Path  string `json:"path"`
		Title string `json:"title"`
		Type  string `json:"type"`
		Date  string `json:"date"`
	}
	listOut struct {
		Notes     []listNote `json:"notes"`
		Truncated bool       `json:"truncated"`
	}
	ref struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	backlinksOut struct {
		Backlinks []ref `json:"backlinks"`
	}
	writeIn struct {
		Path    string `json:"path" jsonschema:"ruta de la nota relativa a la raíz, terminada en .md; sin segmentos ocultos ni _templates"`
		Content string `json:"content" jsonschema:"contenido completo de la nota en Markdown, con su frontmatter YAML; máximo 1 MB"`
		Message string `json:"message,omitempty" jsonschema:"mensaje del commit (opcional)"`
	}
	appendIn struct {
		Path    string `json:"path" jsonschema:"ruta de la nota relativa a la raíz, terminada en .md; se crea si no existe"`
		Content string `json:"content" jsonschema:"Markdown que se añade al final, separado por una línea en blanco"`
	}
	journalIn struct {
		Title string `json:"title" jsonschema:"título de la sección (una línea); queda como '## título'"`
		Body  string `json:"body" jsonschema:"cuerpo de la sección en Markdown"`
	}
	writeOut struct {
		Path    string `json:"path"`
		URL     string `json:"url"`
		Commit  string `json:"commit"`
		Created bool   `json:"created"`
	}
	openOut struct {
		URL string `json:"url"`
	}
)

const maxList = 500

func newServer(cfg Config) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "scriba", Version: cfg.Version},
		&sdk.ServerOptions{Instructions: instructions})

	searchSchema, err := jsonschema.For[searchIn](nil)
	if err != nil {
		panic(err)
	}
	lim := searchSchema.Properties["limit"]
	lim.Minimum, lim.Maximum, lim.Default = new(1.0), new(50.0), json.RawMessage("10")
	sdk.AddTool(s, &sdk.Tool{Name: "kb_search", InputSchema: searchSchema,
		Description: "Búsqueda de texto completo en la base de conocimiento. Devuelve las notas más relevantes con su ruta, título y un fragmento. Úsala antes de escribir para no duplicar notas."},
		func(_ context.Context, _ *sdk.CallToolRequest, in searchIn) (*sdk.CallToolResult, searchOut, error) {
			if in.Limit == 0 {
				in.Limit = 10
			}
			hits, err := cfg.Ix.Search(in.Query, in.Limit)
			if err != nil {
				return nil, searchOut{}, err
			}
			out := searchOut{Hits: make([]searchHit, len(hits))}
			for i, h := range hits {
				out.Hits[i] = searchHit{h.Path, h.Title, plain(h.Snippet)}
			}
			return nil, out, nil
		})

	sdk.AddTool(s, &sdk.Tool{Name: "kb_read",
		Description: "Lee una nota completa (frontmatter incluido) con sus metadatos (título, tipo, fecha, tags) y las rutas de las notas que la enlazan."},
		func(_ context.Context, _ *sdk.CallToolRequest, in pathIn) (*sdk.CallToolResult, readOut, error) {
			abs, err := cfg.noteAbs(in.Path)
			if err != nil {
				return nil, readOut{}, err
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return nil, readOut{}, humanErr(in.Path, err)
			}
			n, err := cfg.V.Read(in.Path)
			if err != nil {
				return nil, readOut{}, humanErr(in.Path, err)
			}
			bl, err := cfg.Ix.Backlinks(n.Path)
			if err != nil {
				return nil, readOut{}, err
			}
			out := readOut{Path: n.Path, Title: n.Title, Type: n.Type, Date: n.Date, Tags: n.Tags,
				Content: string(data), Backlinks: make([]string, len(bl))}
			if out.Tags == nil {
				out.Tags = []string{}
			}
			for i, b := range bl {
				out.Backlinks[i] = b.Path
			}
			return nil, out, nil
		})

	sdk.AddTool(s, &sdk.Tool{Name: "kb_list",
		Description: "Lista las notas (ruta, título, tipo, fecha) de una carpeta y sus subcarpetas, ordenadas por ruta; sin carpeta lista todo el vault. Máximo 500."},
		func(_ context.Context, _ *sdk.CallToolRequest, in listIn) (*sdk.CallToolResult, listOut, error) {
			refs, err := cfg.Ix.Refs()
			if err != nil {
				return nil, listOut{}, err
			}
			prefix := strings.Trim(in.Folder, "/")
			if prefix != "" {
				prefix += "/"
			}
			out := listOut{Notes: []listNote{}}
			for _, r := range refs {
				if !strings.HasPrefix(r.Path, prefix) {
					continue
				}
				if len(out.Notes) == maxList {
					out.Truncated = true
					break
				}
				out.Notes = append(out.Notes, listNote{r.Path, r.Title, r.Type, r.Date})
			}
			return nil, out, nil
		})

	sdk.AddTool(s, &sdk.Tool{Name: "kb_backlinks",
		Description: "Devuelve las notas (ruta y título) que enlazan con [[wikilink]] a la nota indicada."},
		func(_ context.Context, _ *sdk.CallToolRequest, in pathIn) (*sdk.CallToolResult, backlinksOut, error) {
			bl, err := cfg.Ix.Backlinks(in.Path)
			if err != nil {
				return nil, backlinksOut{}, err
			}
			out := backlinksOut{Backlinks: make([]ref, len(bl))}
			for i, b := range bl {
				out.Backlinks[i] = ref{b.Path, b.Title}
			}
			return nil, out, nil
		})

	sdk.AddTool(s, &sdk.Tool{Name: "kb_write",
		Description: "Crea o reemplaza por completo una nota con el contenido dado (incluye el frontmatter YAML) y hace un commit a tu nombre. Devuelve la URL de la nota. Para añadir sin reescribir usa kb_append."},
		func(_ context.Context, req *sdk.CallToolRequest, in writeIn) (*sdk.CallToolResult, writeOut, error) {
			agent, err := agentOf(req)
			if err != nil {
				return nil, writeOut{}, err
			}
			res, err := cfg.W.Write(agent, in.Path, in.Content, in.Message)
			return writeResult(req, in.Path, res, err)
		})

	sdk.AddTool(s, &sdk.Tool{Name: "kb_append",
		Description: "Añade Markdown al final de una nota (separado por una línea en blanco), creándola si no existe, y hace un commit a tu nombre."},
		func(_ context.Context, req *sdk.CallToolRequest, in appendIn) (*sdk.CallToolResult, writeOut, error) {
			agent, err := agentOf(req)
			if err != nil {
				return nil, writeOut{}, err
			}
			res, err := cfg.W.Append(agent, in.Path, in.Content)
			return writeResult(req, in.Path, res, err)
		})

	sdk.AddTool(s, &sdk.Tool{Name: "kb_log_journal",
		Description: "Registra una entrada en el journal del día (journal/AAAA-MM-DD.md): añade una sección '## título' con el cuerpo, creando la nota del día si no existe."},
		func(_ context.Context, req *sdk.CallToolRequest, in journalIn) (*sdk.CallToolResult, writeOut, error) {
			agent, err := agentOf(req)
			if err != nil {
				return nil, writeOut{}, err
			}
			res, err := cfg.W.LogJournal(agent, in.Title, in.Body, cfg.Now())
			return writeResult(req, res.Path, res, err)
		})

	sdk.AddTool(s, &sdk.Tool{Name: "kb_open",
		Description: "Devuelve la URL del lector web de Scriba para una nota existente, para que la persona la abra en su navegador."},
		func(_ context.Context, req *sdk.CallToolRequest, in pathIn) (*sdk.CallToolResult, openOut, error) {
			if _, err := cfg.noteAbs(in.Path); err != nil {
				return nil, openOut{}, err
			}
			return nil, openOut{URL: noteURL(req, in.Path)}, nil
		})
	return s
}

// noteAbs valida que path sea una nota .md existente dentro del vault y devuelve su ruta absoluta.
func (cfg Config) noteAbs(p string) (string, error) {
	if !strings.HasSuffix(p, ".md") {
		return "", fmt.Errorf("la ruta %q no es una nota: debe terminar en .md", p)
	}
	abs, err := cfg.V.Abs(p)
	if err != nil {
		return "", humanErr(p, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", humanErr(p, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("la ruta %q no es una nota", p)
	}
	return abs, nil
}

// noteURL arma la URL del lector con la base que Handler dejó en la petición.
func noteURL(req *sdk.CallToolRequest, p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	var base string
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		base, _ = req.Extra.TokenInfo.Extra[baseKey].(string)
	}
	return base + "/n/" + strings.Join(segs, "/")
}

func writeResult(req *sdk.CallToolRequest, p string, res writer.Result, err error) (*sdk.CallToolResult, writeOut, error) {
	if err != nil {
		return nil, writeOut{}, humanErr(p, err)
	}
	return nil, writeOut{Path: res.Path, URL: noteURL(req, res.Path), Commit: res.Commit, Created: res.Created}, nil
}

// humanErr traduce los errores de validación a un mensaje en español para el agente;
// el SDK lo devuelve como resultado con IsError, no como error de protocolo.
func humanErr(p string, err error) error {
	switch {
	case errors.Is(err, writer.ErrInvalidPath), errors.Is(err, vault.ErrOutside):
		return fmt.Errorf("ruta no permitida (%v). Usa una ruta relativa a la raíz del vault, con '/', terminada en .md, sin '..', segmentos ocultos ni _templates", err)
	case errors.Is(err, writer.ErrTooLarge):
		return fmt.Errorf("contenido demasiado grande: el máximo es %d bytes", writer.MaxBytes)
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("la nota %q no existe; búscala con kb_search o kb_list", p)
	}
	return err
}

var unmark = strings.NewReplacer("<mark>", "", "</mark>", "")

// plain pasa el snippet de index.Search (HTML escapado con <mark>) a texto plano.
func plain(s string) string { return html.UnescapeString(unmark.Replace(s)) }
