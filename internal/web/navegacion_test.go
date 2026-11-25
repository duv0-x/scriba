package web

import (
	"strings"
	"testing"
)

// La navegación parcial (ADR-009) va en todas las páginas menos el editor, que recarga completo.
func TestNavegacionParcial(t *testing.T) {
	h, _ := newEditor(t, true)
	const boost = `hx-boost:inherited="target:.view select:.view swap:outerHTML"`

	_, body := get(t, h, "/n/notes/hoy.md", nil)
	for _, s := range []string{boost, `hx-history-elt data-current="notes/hoy.md"`, `href="/editar/notes/hoy.md" hx-boost="false"`} {
		if !strings.Contains(body, s) {
			t.Errorf("lector: falta %q", s)
		}
	}

	// Enter en la búsqueda llega boosteado: necesita la página con .view, no el parcial de resultados.
	_, body = get(t, h, "/buscar?q=texto", map[string]string{"HX-Request": "true", "HX-Boosted": "true"})
	if !strings.Contains(body, `class="view"`) {
		t.Error("/buscar boosteado debe devolver la página con .view")
	}
	_, body = get(t, h, "/buscar?q=texto", map[string]string{"HX-Request": "true"})
	if strings.Contains(body, `class="view"`) {
		t.Error("la búsqueda en vivo debe devolver solo el parcial")
	}

	_, body = get(t, h, "/editar/notes/hoy.md", nil)
	if strings.Contains(body, boost) {
		t.Error("el editor no debe llevar navegación parcial")
	}
	if !strings.Contains(body, `data-view="editor"`) {
		t.Error("el editor debe marcar data-view=editor")
	}
}

func TestDrawerMovil(t *testing.T) {
	h, _ := newEditor(t, true)
	_, body := get(t, h, "/n/notes/hoy.md", nil)
	for _, want := range []string{
		`class="menu-button nav-toggle" popovertarget="nav-drawer"`,
		`aria-controls="nav-drawer" aria-label="Abrir navegación"`,
		`class="side" id="nav-drawer" popover="auto"`,
		`matchMedia("(max-width: 720px)")`,
		`closeOverlays();`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("layout sin %q", want)
		}
	}
}

func TestBusquedaLimpiaAntesDeMoverFoco(t *testing.T) {
	h, _ := newEditor(t, true)
	_, body := get(t, h, "/n/notes/hoy.md", nil)
	compact := strings.Join(strings.Fields(body), "")
	const want = `res.innerHTML="";q.value="";constf=document.activeElement;`
	if !strings.Contains(compact, want) {
		t.Error("los resultados y #q deben limpiarse antes de comprobar el foco")
	}
}
