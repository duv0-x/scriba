package render

import (
	"net/url"
	"strings"
	"testing"
)

type fakeResolver map[string]string

func (f fakeResolver) Resolve(raw string) (string, bool) {
	p, ok := f[raw]
	return p, ok
}

var res = fakeResolver{
	"adr-001":      "decisions/personal/adr-001.md",
	"con espacios": "notes/con espacios.md",
}

func render(t *testing.T, md string, r Resolver) (string, []Heading) {
	t.Helper()
	out, hs, err := HTML(md, r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), hs
}

func TestHTML(t *testing.T) {
	cases := []struct {
		name, md    string
		contains    []string
		notContains []string
	}{
		{"existente", "[[adr-001]]", []string{`<a class="wl" href="/n/decisions/personal/adr-001.md">adr-001</a>`}, nil},
		{"alias", "[[adr-001|la decisión]]", []string{`<a class="wl" href="/n/decisions/personal/adr-001.md">la decisión</a>`}, nil},
		{"ancla", "[[adr-001#Contexto]]", []string{`href="/n/decisions/personal/adr-001.md#contexto"`}, nil},
		{"ancla local", "[[#Contexto]]", []string{`<a class="wl" href="#contexto">#Contexto</a>`}, []string{"missing"}},
		{"ancla local alias", "[[#Contexto|ver]]", []string{`<a class="wl" href="#contexto">ver</a>`}, nil},
		{"tabla alias", "| a |\n|---|\n| [[adr-001\\|ADR]] |", []string{`<a class="wl" href="/n/decisions/personal/adr-001.md">ADR</a>`}, []string{"missing"}},
		{"inexistente", "[[no-existe]]", []string{`class="wl missing"`, `>no-existe</a>`}, []string{"href="}},
		{"espacios", "[[con espacios]]", []string{`href="/n/notes/con%20espacios.md"`}, nil},
		{"embed", "![[adr-001]]", []string{`<a class="wl" href="/n/decisions/personal/adr-001.md">adr-001</a>`}, []string{"<img"}},
		{"script", "<script>alert(1)</script>\n\ntexto <script>x</script>", nil, []string{"<script>"}},
		{"tabla", "| a | b |\n|---|---|\n| 1 | 2 |", []string{"<table>"}, nil},
		{"escape", "[[a<b>]]", []string{"a&lt;b&gt;"}, []string{"<b>"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, _ := render(t, c.md, res)
			for _, s := range c.contains {
				if !strings.Contains(out, s) {
					t.Errorf("falta %q en %q", s, out)
				}
			}
			for _, s := range c.notContains {
				if strings.Contains(out, s) {
					t.Errorf("sobra %q en %q", s, out)
				}
			}
		})
	}
}

func TestHeadings(t *testing.T) {
	_, hs := render(t, "# Título\n\n## Contexto\n\ntexto\n\n## Decisión **final**\n\n### sub\n", res)
	if len(hs) != 2 {
		t.Fatalf("esperaba 2 headings, hay %d: %+v", len(hs), hs)
	}
	want := []string{"Contexto", "Decisión final"}
	for i, h := range hs {
		if h.ID == "" || h.Text != want[i] {
			t.Errorf("heading %d = %+v, esperaba texto %q e ID no vacío", i, h, want[i])
		}
	}
	if hs[0].ID == hs[1].ID {
		t.Errorf("IDs repetidos: %q", hs[0].ID)
	}
}

func TestResolverNil(t *testing.T) {
	out, _ := render(t, "[[adr-001]] y [[con espacios]]", nil)
	if strings.Count(out, `class="wl missing"`) != 2 || strings.Contains(out, "href=") {
		t.Errorf("con resolver nil todo debe ser missing: %q", out)
	}
}

// El ancla de [[nota#Sección]] debe coincidir con el id Unicode del heading (escapado en el href).
func TestAnclaCoincideConHeading(t *testing.T) {
	_, hs := render(t, "## Mi Sección\n", res)
	if len(hs) != 1 || hs[0].ID != "mi-sección" {
		t.Fatalf("headings: %+v", hs)
	}
	out, _ := render(t, "[[adr-001#Mi Sección]]", res)
	if want := `href="/n/decisions/personal/adr-001.md#` + url.PathEscape(hs[0].ID) + `"`; !strings.Contains(out, want) {
		t.Errorf("falta %q en %q", want, out)
	}
}

func TestHeadingsRepetidos(t *testing.T) {
	out, hs := render(t, "## Contexto\n\n## Contexto\n", res)
	if len(hs) != 2 || hs[0].ID != "contexto" || hs[1].ID != "contexto-1" {
		t.Fatalf("headings: %+v", hs)
	}
	if !strings.Contains(out, `id="contexto-1"`) {
		t.Errorf("falta id=\"contexto-1\" en %q", out)
	}
}
