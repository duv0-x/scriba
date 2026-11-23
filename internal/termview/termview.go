// Package termview renderiza una nota del vault para la terminal (scriba view) con glamour.
package termview

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/wikilink"

	"github.com/duv0-x/scriba/internal/mdparse"
	"github.com/duv0-x/scriba/internal/vault"
)

// Styles son los valores aceptados de style.
var Styles = []string{"auto", "dark", "light", "notty"}

// Render arma la cabecera (título, path, tipo, fecha y tags) y el cuerpo de n formateado
// con glamour. resolve traduce el destino crudo de un wikilink al título de su nota.
func Render(n *vault.Note, resolve func(raw string) (title string, ok bool), width int, style string) (string, error) {
	opt := glamour.WithStandardStyle(style)
	if style == "auto" {
		opt = glamour.WithAutoStyle()
	}
	r, err := glamour.NewTermRenderer(opt, glamour.WithWordWrap(width))
	if err != nil {
		return "", fmt.Errorf("termview: %w", err)
	}
	body, err := r.Render(replaceWikilinks(dropLeadingH1(n), resolve))
	if err != nil {
		return "", fmt.Errorf("termview: %w", err)
	}

	meta := []string{n.Path, n.Type}
	if n.Date != "" {
		meta = append(meta, n.Date)
	}
	if len(n.Tags) > 0 {
		meta = append(meta, "#"+strings.Join(n.Tags, " #"))
	}
	title, muted := n.Title, strings.Join(meta, " · ")
	if style != "notty" {
		title, muted = "\x1b[1m"+title+"\x1b[0m", "\x1b[2m"+muted+"\x1b[0m"
	}
	// glamour ya deja margen izquierdo de 2 columnas: la cabecera se alinea con él.
	return "\n  " + title + "\n  " + muted + "\n" + body, nil
}

// replaceWikilinks cambia cada wikilink por ⟦texto⟧ (alias, título resuelto o destino).
// Se localizan con el mismo parser de goldmark que usan el lector y el indexador, así que
// lo que está en bloques o spans de código no se toca; luego se reescribe el Markdown fuente
// por rangos de bytes, porque goldmark no sabe volver a emitir Markdown desde el AST.
func replaceWikilinks(body string, resolve func(string) (string, bool)) string {
	src := []byte(body)
	doc := mdparse.New().Parser().Parse(text.NewReader(src))
	var out bytes.Buffer
	last := 0
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		wl, ok := node.(*wikilink.Node)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		t, ok := wl.FirstChild().(*ast.Text)
		if !ok {
			return ast.WalkSkipChildren, nil
		}
		// El hijo es el alias o el destino completo; "[[" (o "![[") lo precede y "]]" lo cierra.
		start := bytes.LastIndex(src[:t.Segment.Start], []byte("[["))
		if start > 0 && src[start-1] == '!' {
			start--
		}
		end := t.Segment.Stop + 2
		if start < last || end > len(src) {
			return ast.WalkSkipChildren, nil
		}
		label := string(t.Segment.Value(src))
		if !bytes.Contains(src[start:t.Segment.Start], []byte("|")) {
			dest := mdparse.Target(wl)
			label = dest
			if title, ok := resolve(dest); ok && dest != "" {
				label = title
			}
			if len(wl.Fragment) > 0 {
				label += "#" + string(wl.Fragment)
			}
		}
		out.Write(src[last:start])
		out.WriteString("⟦" + label + "⟧")
		last = end
		return ast.WalkSkipChildren, nil
	})
	out.Write(src[last:])
	return out.String()
}

// dropLeadingH1 quita el "# Título" inicial cuando repite el título de la cabecera
// (misma regla que el lector web en internal/web).
func dropLeadingH1(n *vault.Note) string {
	rest := strings.TrimLeft(n.Body, " \t\r\n")
	if !strings.HasPrefix(rest, "# ") {
		return n.Body
	}
	h1, after, _ := strings.Cut(rest, "\n")
	if _, ok := n.Meta["title"]; ok && strings.TrimSpace(h1[2:]) != n.Title {
		return n.Body
	}
	return after
}
