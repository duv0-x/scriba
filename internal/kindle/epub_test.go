package kindle

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/duv0-x/scriba/internal/render"
)

type fakeResolver struct{}

func (fakeResolver) Resolve(raw string) (string, bool) {
	if raw == "adr-001" {
		return "decisions/personal/adr-001.md", true
	}
	return "", false
}

func TestBuildEPUB(t *testing.T) {
	md := "## Contexto\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nx := 1 < 2\n```\n\n[[adr-001|la decisión]] y [[no-existe]] y [Resend](https://resend.com/docs) y [otra](otra.md)\n\n![diagrama](assets/a.png)\n\n[![diagrama](a.png)](otra.md)\n"
	html, _, err := render.HTML(md, fakeResolver{})
	if err != nil {
		t.Fatalf("render.HTML: %v", err)
	}
	n := Note{Title: "Plan: ¿qué/cómo?", Date: "2026-11-25", Path: "plans/x.md", Body: html}
	data, name, err := BuildEPUB(n)
	if err != nil {
		t.Fatalf("BuildEPUB: %v", err)
	}
	if name != "Plan que-como.epub" {
		t.Errorf("filename = %q, quiero %q", name, "Plan que-como.epub")
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if r.File[0].Name != "mimetype" {
		t.Errorf("primera entrada = %q, quiero %q", r.File[0].Name, "mimetype")
	}
	if r.File[0].Method != zip.Store {
		t.Errorf("mimetype method = %d, quiero zip.Store", r.File[0].Method)
	}
	read := func(f *zip.File) string {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("abrir %s: %v", f.Name, err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("leer %s: %v", f.Name, err)
		}
		return string(b)
	}
	if got := read(r.File[0]); got != "application/epub+zip" {
		t.Errorf("mimetype = %q, quiero %q", got, "application/epub+zip")
	}

	contents := map[string]string{}
	for _, f := range r.File {
		contents[f.Name] = read(f)
	}
	for _, want := range []string{
		"META-INF/container.xml",
		"OEBPS/content.opf",
		"OEBPS/nav.xhtml",
		"OEBPS/toc.ncx",
		"OEBPS/estilo.css",
		"OEBPS/nota.xhtml",
	} {
		if _, ok := contents[want]; !ok {
			t.Errorf("falta la entrada %s", want)
		}
	}

	opf := contents["OEBPS/content.opf"]
	for _, want := range []string{`<dc:title>`, `<dc:language>es</dc:language>`, `<dc:identifier id="uid">urn:uuid:`, `properties="nav"`} {
		if !strings.Contains(opf, want) {
			t.Errorf("content.opf no contiene %q", want)
		}
	}

	for name, c := range contents {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".opf") &&
			!strings.HasSuffix(name, ".ncx") && !strings.HasSuffix(name, ".xhtml") {
			continue
		}
		d := xml.NewDecoder(strings.NewReader(c))
		d.Strict = true
		for {
			if _, err := d.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Errorf("%s no es XML bien formado: %v", name, err)
				break
			}
		}
	}

	nota := contents["OEBPS/nota.xhtml"]
	for _, want := range []string{"<h2", "<table", "<pre", "la decisión", "no-existe", `href="https://resend.com/docs"`, "2026-11-25", "plans/x.md", "diagrama", "otra"} {
		if !strings.Contains(nota, want) {
			t.Errorf("nota.xhtml no contiene %q", want)
		}
	}
	for _, bad := range []string{`class="wl`, `href="/n/`, "<img", `href="otra.md"`} {
		if strings.Contains(nota, bad) {
			t.Errorf("nota.xhtml no debería contener %q", bad)
		}
	}
}

func TestBuildEPUBEmptyTitle(t *testing.T) {
	_, name, err := BuildEPUB(Note{Path: "plans/x.md", Body: "<p>a</p>"})
	if err != nil {
		t.Fatalf("BuildEPUB: %v", err)
	}
	if name != "plans-x.md.epub" {
		t.Errorf("filename = %q, quiero %q", name, "plans-x.md.epub")
	}
}

func TestFilename(t *testing.T) {
	for _, tc := range []struct {
		title, want string
	}{
		{"ADR-012: Conector Kindle", "ADR-012 Conector Kindle.epub"},
		{"Café/ñandú", "Cafe-nandu.epub"},
		{"", "nota.epub"},
		{"...", "nota.epub"},
	} {
		if got := Filename(tc.title); got != tc.want {
			t.Errorf("Filename(%q) = %q, quiero %q", tc.title, got, tc.want)
		}
	}
}
