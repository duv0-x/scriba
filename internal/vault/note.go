package vault

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Note es una nota del vault ya parseada.
type Note struct {
	Path    string
	Title   string
	Type    string
	Date    string
	Tags    []string
	Meta    map[string]any
	Body    string
	Links   []string
	Words   int
	ModTime time.Time
}

// KnownTypes son las carpetas de primer nivel que definen el Type de una nota.
var KnownTypes = []string{"journal", "tickets", "plans", "decisions", "runbooks", "postmortems", "references", "processes", "onboarding", "domains", "services", "clients", "drafts", "learning", "pocs", "docs"}

var (
	wikilinkRe   = regexp.MustCompile(`\[\[([^\[\]]*)\]\]`)
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
)

func parse(rel, content string) *Note {
	n := &Note{Path: rel, Meta: map[string]any{}, Body: content}
	if meta, body, ok := splitFrontmatter(content); ok {
		n.Meta, n.Body = meta, body
	}
	base := strings.TrimSuffix(path.Base(rel), ".md")
	prose := proseLines(n.Body)

	n.Title = metaString(n.Meta, "title")
	if n.Title == "" {
		for _, l := range prose {
			if strings.HasPrefix(l, "# ") {
				n.Title = strings.TrimSpace(l[2:])
				break
			}
		}
	}
	if n.Title == "" {
		n.Title = base
	}

	// La carpeta raíz manda: el type: del frontmatter no es uniforme entre notas viejas.
	if first, _, ok := strings.Cut(rel, "/"); ok && slices.Contains(KnownTypes, first) {
		n.Type = first
	} else if t := strings.ToLower(metaString(n.Meta, "type")); t != "" {
		if slices.Contains(KnownTypes, t+"s") {
			t += "s"
		}
		n.Type = t
	} else {
		n.Type = "note"
	}

	switch d := n.Meta["date"].(type) {
	case time.Time:
		n.Date = d.Format("2006-01-02")
	case string:
		n.Date = strings.TrimSpace(d)
	}
	if n.Date == "" && n.Type == "journal" {
		n.Date = base
	}

	n.Tags = parseTags(n.Meta["tags"])
	n.Links = parseLinks(prose)
	n.Words = len(strings.Fields(n.Body))
	return n
}

// splitFrontmatter separa el bloque YAML inicial; ok=false si no hay bloque o el YAML es inválido.
func splitFrontmatter(content string) (map[string]any, string, bool) {
	var rest string
	switch {
	case strings.HasPrefix(content, "---\n"):
		rest = content[4:]
	case strings.HasPrefix(content, "---\r\n"):
		rest = content[5:]
	default:
		return nil, "", false
	}
	for off := 0; off <= len(rest); {
		end := strings.IndexByte(rest[off:], '\n')
		line, next := rest[off:], len(rest)
		if end >= 0 {
			line, next = rest[off:off+end], off+end+1
		}
		if strings.TrimSuffix(line, "\r") == "---" {
			meta := map[string]any{}
			if err := yaml.Unmarshal([]byte(rest[:off]), &meta); err != nil {
				return nil, "", false
			}
			if meta == nil {
				meta = map[string]any{}
			}
			return meta, rest[next:], true
		}
		if end < 0 {
			break
		}
		off = next
	}
	return nil, "", false
}

// proseLines devuelve las líneas del body fuera de bloques de código cercados.
func proseLines(body string) []string {
	var out []string
	fence := ""
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSuffix(l, "\r")
		t := strings.TrimSpace(l)
		if fence == "" && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")) {
			fence = t[:3]
			continue
		}
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		out = append(out, l)
	}
	return out
}

func parseLinks(prose []string) []string {
	var links []string
	for _, l := range prose {
		l = inlineCodeRe.ReplaceAllString(l, "")
		for _, m := range wikilinkRe.FindAllStringSubmatch(l, -1) {
			dest, _, _ := strings.Cut(m[1], "|")
			dest = strings.TrimSuffix(dest, "\\") // [[destino\|alias]] dentro de una tabla GFM
			dest, _, _ = strings.Cut(dest, "#")
			dest = strings.TrimSpace(dest)
			if dest != "" && !slices.Contains(links, dest) {
				links = append(links, dest)
			}
		}
	}
	return links
}

func parseTags(v any) []string {
	var raw []string
	switch t := v.(type) {
	case string:
		raw = strings.Split(t, ",")
	case []any:
		for _, x := range t {
			raw = append(raw, fmt.Sprint(x))
		}
	}
	var tags []string
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			tags = append(tags, s)
		}
	}
	return tags
}

func metaString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}
