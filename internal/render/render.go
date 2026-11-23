// Package render convierte el Body Markdown de una nota a HTML seguro, resolviendo wikilinks.
package render

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"go.abhg.dev/goldmark/wikilink"

	"github.com/duv0-x/scriba/internal/mdparse"
)

// Resolver traduce el destino crudo de un wikilink ("adr-001", "decisions/x") a la ruta de la nota en el vault.
type Resolver interface {
	Resolve(raw string) (path string, ok bool)
}

// Heading es un h2 o h3 de la nota, para el índice lateral.
type Heading struct {
	ID, Text string
	Level    int
}

// HTML convierte el Body de una nota a HTML seguro y devuelve el índice de h2 y h3.
func HTML(body string, r Resolver) (template.HTML, []Heading, error) {
	// Se arma un goldmark por llamada porque el renderer de wikilinks depende del Resolver.
	md := mdparse.New(
		goldmark.WithRendererOptions(
			// Sin html.WithUnsafe(): el HTML crudo del Markdown se omite.
			renderer.WithNodeRenderers(util.Prioritized(&wikilinkRenderer{r: r}, 199)),
		),
	)

	src := []byte(body)
	doc := md.Parser().Parse(text.NewReader(src), parser.WithContext(mdparse.NewContext()))

	var headings []Heading
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		h, ok := c.(*ast.Heading)
		if !ok || (h.Level != 2 && h.Level != 3) {
			continue
		}
		var id string
		if v, ok := h.AttributeString("id"); ok {
			if b, ok := v.([]byte); ok {
				id = string(b)
			}
		}
		headings = append(headings, Heading{ID: id, Text: plainText(src, h), Level: h.Level})
	}

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return "", nil, err
	}
	return template.HTML(buf.String()), headings, nil
}

// wikilinkRenderer emite <a class="wl"> para notas existentes y <a class="wl missing"> sin href para las que no.
// Los embeds (![[...]]) se renderizan igual que un enlace normal.
type wikilinkRenderer struct{ r Resolver }

func (w *wikilinkRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(wikilink.Kind, w.render)
}

func (w *wikilinkRenderer) render(out util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = out.WriteString("</a>")
		return ast.WalkContinue, nil
	}
	n := node.(*wikilink.Node)
	target := mdparse.Target(n)
	frag := ""
	if len(n.Fragment) > 0 {
		// Mismo Slug que los ids de los headings, para que el ancla coincida.
		frag = "#" + url.PathEscape(mdparse.Slug(string(n.Fragment)))
	}
	if target == "" && frag != "" {
		// [[#Sección]]: ancla dentro de la propia nota.
		_, _ = out.WriteString(`<a class="wl" href="` + template.HTMLEscapeString(frag) + `">`)
		return ast.WalkContinue, nil
	}
	var path string
	ok := false
	if w.r != nil {
		path, ok = w.r.Resolve(target)
	}
	if !ok {
		_, _ = out.WriteString(`<a class="wl missing" title="Nota aún no escrita">`)
		return ast.WalkContinue, nil
	}
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	href := "/n/" + strings.Join(segs, "/") + frag
	// El texto (alias o destino crudo) lo emiten los hijos, que goldmark escapa como HTML.
	_, _ = out.WriteString(`<a class="wl" href="` + template.HTMLEscapeString(href) + `">`)
	return ast.WalkContinue, nil
}

// plainText concatena el texto de los descendientes de n.
func plainText(src []byte, n ast.Node) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		switch t := n.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
		case *ast.String:
			b.Write(t.Value)
		default:
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}
