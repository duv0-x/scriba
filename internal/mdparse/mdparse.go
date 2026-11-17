// Package mdparse concentra la configuración de goldmark que comparten el lector (render)
// y el indexador (vault), para que ambos vean exactamente los mismos wikilinks y anclas.
package mdparse

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/util"
	"go.abhg.dev/goldmark/wikilink"
)

// New arma un goldmark con GFM, IDs automáticos en headings y el parser de wikilinks;
// opts añade lo propio de cada uso (p. ej. el renderer de wikilinks del lector).
func New(opts ...goldmark.Option) goldmark.Markdown {
	base := []goldmark.Option{
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithInlineParsers(util.Prioritized(&wikilink.Parser{}, 199)),
		),
	}
	return goldmark.New(append(base, opts...)...)
}

// NewContext devuelve un contexto de parseo con los IDs Unicode de Slug; uno por documento.
func NewContext() parser.Context {
	return parser.NewContext(parser.WithIDs(&ids{seen: map[string]bool{}}))
}

// Target es el destino del wikilink sin la '\' final que deja el alias escapado en tablas GFM ([[x\|alias]]).
func Target(n *wikilink.Node) string {
	return strings.TrimSuffix(string(n.Target), "\\")
}

// Slug pasa un texto a id de ancla: minúsculas, letras y dígitos Unicode, espacios y guiones a '-'.
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		case unicode.IsSpace(r) || r == '-':
			dash = true
		}
	}
	return b.String()
}

// ids implementa parser.IDs con Slug y sufijos -1, -2… para repetidos.
type ids struct{ seen map[string]bool }

func (i *ids) Generate(value []byte, _ ast.NodeKind) []byte {
	base := Slug(string(value))
	if base == "" {
		base = "heading"
	}
	id := base
	for n := 1; i.seen[id]; n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	i.seen[id] = true
	return []byte(id)
}

func (i *ids) Put(value []byte) { i.seen[string(value)] = true }
