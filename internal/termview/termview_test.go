package termview

import (
	"strings"
	"testing"

	"github.com/duv0-x/scriba/internal/vault"
)

func TestRender(t *testing.T) {
	v, err := vault.Open("testdata")
	if err != nil {
		t.Fatal(err)
	}
	n, err := v.Read("nota.md")
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(raw string) (string, bool) {
		if raw == "adr-001" {
			return "ADR-001 · Markdown plano", true
		}
		return "", false
	}
	out, err := Render(n, resolve, 60, "notty")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Desplegar Scriba", "nota.md · note · 2026-11-17 · #go #pi",
		"⟦ADR-001 · Markdown plano⟧", "⟦el runbook⟧", "⟦falta#Paso⟧",
		`echo "[[dentro-de-codigo]]"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q en:\n%s", want, out)
		}
	}
	if strings.Count(out, "Desplegar Scriba") != 1 {
		t.Errorf("el H1 repetido no se quitó:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("notty no debería emitir secuencias ANSI:\n%q", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if w := len([]rune(l)); w > 60 {
			t.Errorf("línea de %d columnas > 60: %q", w, l)
		}
	}
}
