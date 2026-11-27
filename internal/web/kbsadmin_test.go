package web

import (
	"archive/zip"
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/state"
)

type kbsEnv struct {
	h    http.Handler
	reg  *kbs.Registry
	data string
}

// newKBsServer arma la web sobre un Registry real (con su KB por defecto, "principal").
func newKBsServer(t *testing.T) *kbsEnv {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// La KB por defecto nace vacía: sin esto el gate mandaría todo a /bienvenida.
	if err := st.SetSetting(settingInitialized, "1"); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	data := t.TempDir()
	reg, err := kbs.Open(context.Background(), kbs.Config{Data: data, State: st, Key: key, Loc: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	srv := &Server{KBs: reg, State: st, Key: key, GitHub: &fakeGitHub{}, Git: &fakeGit{}, ClientID: "cid"}
	return &kbsEnv{h: srv.Handler(), reg: reg, data: data}
}

func (e *kbsEnv) exists(slug string) bool {
	_, err := os.Stat(filepath.Join(e.data, "kbs", slug))
	return err == nil
}

func TestKBsCrearBuscarBorrar(t *testing.T) {
	e := newKBsServer(t)
	def := e.reg.Default().Slug
	if res, body := get(t, e.h, "/kbs", nil); res.StatusCode != http.StatusOK || !strings.Contains(body, "<code>"+def+"</code>") {
		t.Fatalf("GET /kbs = %d\n%s", res.StatusCode, body)
	}

	res, body := post(t, e.h, "/kbs", url.Values{"slug": {"prueba"}, "origen": {"plantilla"}}, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/k/prueba/" {
		t.Fatalf("crear = %d %q\n%s", res.StatusCode, res.Header.Get("Location"), body)
	}
	if !e.exists("prueba") {
		t.Fatal("no se creó <data>/kbs/prueba")
	}
	if res, _ := post(t, e.h, "/kbs", url.Values{"slug": {"prueba"}}, nil); res.StatusCode != http.StatusConflict {
		t.Errorf("crear dos veces = %d", res.StatusCode)
	}

	// Aislamiento: una nota de prueba no aparece al buscar en la KB por defecto.
	k, _ := e.reg.Get("prueba")
	if _, err := k.W.Write("test", "notas/solo-prueba.md", "# Zanahoria\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, body := get(t, e.h, "/k/prueba/buscar?q=zanahoria", nil); !strings.Contains(body, "solo-prueba") {
		t.Errorf("la búsqueda en prueba no encuentra su nota:\n%s", body)
	}
	if _, body := get(t, e.h, "/k/"+def+"/buscar?q=zanahoria", nil); strings.Contains(body, "solo-prueba") {
		t.Error("la nota de prueba aparece en la KB por defecto")
	}

	if res, _ := post(t, e.h, "/kbs/borrar", url.Values{"slug": {"prueba"}, "confirm": {"otra"}}, nil); res.StatusCode != http.StatusBadRequest || !e.exists("prueba") {
		t.Fatalf("borrar sin confirmar = %d, existe %v", res.StatusCode, e.exists("prueba"))
	}
	res, _ = post(t, e.h, "/kbs/borrar", url.Values{"slug": {"prueba"}, "confirm": {"prueba"}}, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/kbs" {
		t.Fatalf("borrar = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if e.exists("prueba") {
		t.Error("<data>/kbs/prueba sigue en disco")
	}
	if res, _ := get(t, e.h, "/k/prueba/", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("/k/prueba/ tras borrar = %d", res.StatusCode)
	}
	if res, _ := get(t, e.h, "/k/"+def+"/", nil); res.StatusCode != http.StatusOK {
		t.Errorf("la KB por defecto tras borrar = %d", res.StatusCode)
	}
	if res, _ := post(t, e.h, "/kbs/borrar", url.Values{"slug": {def}, "confirm": {def}}, nil); res.StatusCode != http.StatusConflict {
		t.Errorf("borrar la por defecto = %d", res.StatusCode)
	}
}

func TestKBsSlugInvalido(t *testing.T) {
	e := newKBsServer(t)
	for _, slug := range []string{"Prueba", "../x", "a/b", "", "-a", strings.Repeat("a", 41)} {
		if res, _ := post(t, e.h, "/kbs", url.Values{"slug": {slug}}, nil); res.StatusCode != http.StatusBadRequest {
			t.Errorf("slug %q = %d", slug, res.StatusCode)
		}
	}
	if got := e.reg.List(); len(got) != 1 {
		t.Errorf("KBs tras slugs inválidos = %v", got)
	}
}

func TestKBsCrearDesdeZip(t *testing.T) {
	e := newKBsServer(t)
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	f, _ := zw.Create("hola.md")
	f.Write([]byte("# Hola\n"))
	zw.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("slug", "dezip")
	mw.WriteField("origen", "zip")
	fw, _ := mw.CreateFormFile("zip", "base.zip")
	fw.Write(zb.Bytes())
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/kbs", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/k/dezip/" {
		t.Fatalf("crear desde zip = %d %q\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	k, ok := e.reg.Get("dezip")
	if !ok {
		t.Fatal("no está en el Registry")
	}
	if n, err := k.Ix.NoteCount(); err != nil || n != 1 {
		t.Errorf("notas = %d, %v", n, err)
	}
}
