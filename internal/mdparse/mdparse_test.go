package mdparse

import (
	"testing"

	"github.com/yuin/goldmark/ast"
)

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Mi Sección":          "mi-sección",
		"  --Hola,  mundo-- ": "hola-mundo",
		"ADR-001 · Año 2026":  "adr-001-año-2026",
		"¿?":                  "",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIDsUnicos(t *testing.T) {
	ids := NewContext().IDs()
	got := []string{
		string(ids.Generate([]byte("Contexto"), ast.KindHeading)),
		string(ids.Generate([]byte("Contexto"), ast.KindHeading)),
		string(ids.Generate([]byte("Contexto"), ast.KindHeading)),
		string(ids.Generate([]byte("¿?"), ast.KindHeading)),
	}
	want := []string{"contexto", "contexto-1", "contexto-2", "heading"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}
