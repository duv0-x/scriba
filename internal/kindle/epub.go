// Package kindle genera el EPUB de una nota y lo envía a la Kindle por Resend (ADR-012).
package kindle

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	_ "embed"
	"fmt"
	stdhtml "html"
	"html/template"
	"io"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/text/unicode/norm"
)

//go:embed estilo.css
var css []byte

// Note es lo que se manda a la Kindle.
type Note struct {
	Title string        // título del frontmatter
	Date  string        // fecha ya formateada, p. ej. "2026-11-25"; puede ir vacía
	Path  string        // ruta en el vault, p. ej. "decisions/personal/adr-012.md"
	Body  template.HTML // cuerpo ya renderizado por internal/render.HTML (html/template.HTML)
}

const containerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`

const opfTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid" xml:lang="es">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:%s</dc:identifier>
    <dc:title>%s</dc:title>
    <dc:language>es</dc:language>
    <meta property="dcterms:modified">%s</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="css" href="estilo.css" media-type="text/css"/>
    <item id="nota" href="nota.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="nota"/>
  </spine>
</package>
`

const ncxTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head><meta name="dtb:uid" content="urn:uuid:%s"/></head>
  <docTitle><text>%s</text></docTitle>
  <navMap>
    <navPoint id="nota" playOrder="1"><navLabel><text>%s</text></navLabel><content src="nota.xhtml"/></navPoint>
  </navMap>
</ncx>
`

const navTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="es" lang="es">
<head><title>%s</title></head>
<body>
<nav epub:type="toc" id="toc"><ol><li><a href="nota.xhtml">%s</a></li></ol></nav>
</body>
</html>
`

const notaTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="es" lang="es">
<head><title>%s</title><link rel="stylesheet" type="text/css" href="estilo.css"/></head>
<body>
<h1 class="title">%s</h1>
<p class="meta">%s</p>
<hr/>
%s
</body>
</html>
`

// BuildEPUB devuelve los bytes del EPUB 3 y el nombre del adjunto (Filename del título efectivo).
func BuildEPUB(n Note) ([]byte, string, error) {
	title := n.Title
	if title == "" {
		title = n.Path
	}
	body, err := xhtml(n.Body)
	if err != nil {
		return nil, "", fmt.Errorf("cuerpo xhtml: %w", err)
	}
	uuid, err := uuid4()
	if err != nil {
		return nil, "", fmt.Errorf("uuid: %w", err)
	}
	esc := stdhtml.EscapeString
	modified := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	meta := esc(n.Path)
	if n.Date != "" {
		meta = esc(n.Date) + " · " + meta
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, content []byte, method uint16) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			return err
		}
		_, err = w.Write(content)
		return err
	}
	if err := add("mimetype", []byte("application/epub+zip"), zip.Store); err != nil {
		return nil, "", err
	}
	entries := []struct {
		name    string
		content []byte
	}{
		{"META-INF/container.xml", []byte(containerXML)},
		{"OEBPS/content.opf", []byte(fmt.Sprintf(opfTmpl, uuid, esc(title), modified))},
		{"OEBPS/toc.ncx", []byte(fmt.Sprintf(ncxTmpl, uuid, esc(title), esc(title)))},
		{"OEBPS/nav.xhtml", []byte(fmt.Sprintf(navTmpl, esc(title), esc(title)))},
		{"OEBPS/estilo.css", css},
		{"OEBPS/nota.xhtml", []byte(fmt.Sprintf(notaTmpl, esc(title), esc(title), meta, body))},
	}
	for _, e := range entries {
		if err := add(e.name, e.content, zip.Deflate); err != nil {
			return nil, "", err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), Filename(title), nil
}

// xhtml convierte el HTML del cuerpo a XHTML bien formado: deja el contenido
// sin recursos externos (plainLinks) y serializa el resto.
func xhtml(body template.HTML) (string, error) {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(string(body)), ctx)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, n := range nodes {
		plainLinks(n)
		if err := html.Render(&sb, n); err != nil {
			return "", err
		}
	}
	return sb.String(), nil
}

// plainLinks deja el árbol sin recursos externos: el EPUB no lleva más
// recursos que estilo.css y epubcheck rechaza referencias fuera del paquete
// (RSC-006/007). Cada <img> se sustituye por su texto alt (o se elimina si no
// tiene), y cada <a> sin href http(s)/mailto (wikilinks, enlaces a otras
// notas) se sustituye por sus hijos.
func plainLinks(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.ElementNode && c.Data == "img":
			alt := ""
			for _, a := range c.Attr {
				if a.Key == "alt" {
					alt = a.Val
					break
				}
			}
			if alt != "" {
				n.InsertBefore(&html.Node{Type: html.TextNode, Data: alt}, c)
			}
			n.RemoveChild(c)
		case c.Type == html.ElementNode && c.Data == "a" && !externalLink(c):
			plainLinks(c) // primero los hijos: pueden contener <img> u otros enlaces
			for ch := c.FirstChild; ch != nil; {
				chNext := ch.NextSibling
				c.RemoveChild(ch)
				n.InsertBefore(ch, c)
				ch = chNext
			}
			n.RemoveChild(c)
		default:
			plainLinks(c)
		}
		c = next
	}
}

// externalLink dice si el <a> apunta a un recurso externo que se conserva.
func externalLink(n *html.Node) bool {
	for _, a := range n.Attr {
		if a.Key == "href" {
			return strings.HasPrefix(a.Val, "http://") ||
				strings.HasPrefix(a.Val, "https://") ||
				strings.HasPrefix(a.Val, "mailto:")
		}
	}
	return false
}

// uuid4 genera un UUID v4 aleatorio.
func uuid4() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// asciiFold quita acentos (á → a, ñ → n) y cualquier carácter fuera de ASCII.
func asciiFold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if r < 128 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var unsafeName = regexp.MustCompile(`[:*?"<>|\x00-\x1f]+`)

// Filename nombra el adjunto a partir del título: Send to Kindle muestra el
// nombre del adjunto como título del documento, no dc:title. Es ASCII plano:
// un nombre largo no-ASCII se parte en continuaciones RFC 2231 y Amazon solo
// decodifica la primera ("cre%C3%ADamos").
func Filename(title string) string {
	name := strings.NewReplacer("/", "-", "\\", "-").Replace(asciiFold(title))
	name = strings.Join(strings.Fields(unsafeName.ReplaceAllString(name, " ")), " ")
	if r := []rune(name); len(r) > 90 {
		name = strings.TrimSpace(string(r[:90]))
	}
	name = strings.TrimRight(name, ". ")
	if name == "" {
		return "nota.epub"
	}
	return name + ".epub"
}
