package web

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGating(t *testing.T) {
	e := newSetupServer(t)
	res, _ := get(t, e.h, "/", nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/bienvenida" {
		t.Fatalf("GET / = %d %q; quiero 303 a /bienvenida", res.StatusCode, res.Header.Get("Location"))
	}
	for _, p := range []string{"/readyz", "/bienvenida", "/conectores", "/static/app.css"} {
		if res, _ := get(t, e.h, p, nil); res.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d; no debe redirigir", p, res.StatusCode)
		}
	}
}

func TestBienvenidaNueva(t *testing.T) {
	e := newSetupServer(t)
	res, body := post(t, e.h, "/bienvenida/nueva", nil, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Fatalf("status %d %q\n%s", res.StatusCode, res.Header.Get("Location"), body)
	}
	if _, err := os.Stat(filepath.Join(e.srv.V.Root, "README.md")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(e.git.calls, []string{"init", "commit"}) || e.imported != 1 {
		t.Fatalf("git = %v, OnImported = %d", e.git.calls, e.imported)
	}
	if res, _ := get(t, e.h, "/", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("GET / tras crear = %d", res.StatusCode)
	}
	// Una segunda vez, la base ya tiene contenido.
	if _, body := post(t, e.h, "/bienvenida/nueva", nil, nil); !strings.Contains(body, "La base ya tiene notas") {
		t.Error("falta el aviso de base con notas")
	}
}

func TestCSRF(t *testing.T) {
	e := newSetupServer(t)
	cases := map[string]map[string]string{
		"sin cabeceras":   {},
		"cross-site":      {"Sec-Fetch-Site": "cross-site"},
		"same-site":       {"Sec-Fetch-Site": "same-site"},
		"origin ajeno":    {"Origin": "https://evil.example"},
		"origin inválido": {"Origin": "null"},
	}
	for name, hdr := range cases {
		t.Run(name, func(t *testing.T) {
			if res, _ := post(t, e.h, "/bienvenida/nueva", nil, hdr); res.StatusCode != http.StatusForbidden {
				t.Fatalf("status %d; quiero 403", res.StatusCode)
			}
		})
	}
	// Sin Sec-Fetch-Site, Origin igual al Host vale (httptest usa example.com).
	if res, _ := post(t, e.h, "/conectores/github/desconectar", nil, map[string]string{"Origin": "http://example.com"}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("origin propio: status %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.srv.V.Root, "README.md")); err == nil {
		t.Fatal("un POST rechazado escribió en el vault")
	}
}

func TestZipInseguro(t *testing.T) {
	e := newSetupServer(t)
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("../x")
	_, _ = w.Write([]byte("malo"))
	_ = zw.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("zip", "base.zip")
	_, _ = fw.Write(zbuf.Bytes())
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/bienvenida/zip", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "El .zip no es seguro: ") {
		t.Fatalf("status %d sin el mensaje:\n%s", rec.Code, rec.Body.String())
	}
	if ents, _ := os.ReadDir(e.srv.V.Root); len(ents) != 0 {
		t.Errorf("el vault quedó con %d entradas", len(ents))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.srv.V.Root), "x")); err == nil {
		t.Error("escribió fuera del vault")
	}
	if len(e.git.calls) != 0 || e.imported != 0 {
		t.Errorf("git = %v, OnImported = %d", e.git.calls, e.imported)
	}
}

// Tras importar, nada de lo importado sale "sin leer": ni la visita con el vault vacío ni un
// baseline previo cuentan.
func TestImportarNoMarcaSinLeer(t *testing.T) {
	e := newSetupServer(t)
	e.srv.OnImported = func() {
		if err := e.srv.Ix.Rebuild(e.srv.V); err != nil {
			t.Error(err)
		}
	}
	if res, _ := get(t, e.h, "/bienvenida", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("bienvenida = %d", res.StatusCode)
	}
	if _, err := e.srv.State.Baseline("default", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if res, body := post(t, e.h, "/bienvenida/nueva", nil, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("nueva = %d\n%s", res.StatusCode, body)
	}
	_, body := get(t, e.h, "/", nil)
	if !strings.Contains(body, `data-path="`) {
		t.Fatalf("la sidebar no lista notas\n%s", body)
	}
	if strings.Contains(body, `class="unread`) {
		t.Error("hay notas marcadas como sin leer tras importar")
	}
}
