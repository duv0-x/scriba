package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kindleNoteEnv arma el Server de setup con una nota indexada y la base inicializada.
func kindleNoteEnv(t *testing.T) *setupEnv {
	t.Helper()
	e := newSetupServer(t)
	nota := "---\ntitle: \"Nota de prueba\"\ndate: \"2026-11-25\"\n---\n\n# Nota de prueba\n\nHola [[otra]] y [web](https://example.com).\n"
	if err := os.WriteFile(filepath.Join(e.srv.V.Root, "nota.md"), []byte(nota), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.Ix.Rebuild(e.srv.V); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.State.SetSetting("initialized", "1"); err != nil {
		t.Fatal(err)
	}
	return e
}

// saveKindle guarda el conector Kindle por el formulario real y exige el 303 de éxito.
func saveKindle(t *testing.T, e *setupEnv, key string) {
	t.Helper()
	res, body := post(t, e.h, "/conectores/kindle", url.Values{
		"to":   {"duvan_xyz@kindle.com"},
		"from": {"Scriba <kindle@ejemplo.com>"},
		"key":  {key},
	}, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("guardar kindle: got %d, want 303; body: %s", res.StatusCode, body)
	}
}

// resendReq es la última petición que recibió el Resend falso.
type resendReq struct {
	auth string
	body map[string]any
}

// fakeResend es una API de Resend de mentira: guarda la última petición y responde status + body.
func fakeResend(t *testing.T, status int, body string) (*httptest.Server, *resendReq) {
	t.Helper()
	got := &resendReq{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts, got
}

func TestKindleKeyNoVuelveAlHTML(t *testing.T) {
	e := newSetupServer(t)
	saveKindle(t, e, "re_SECRETA_123")

	res, body := get(t, e.h, "/conectores", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /conectores: got %d, want 200", res.StatusCode)
	}
	for _, want := range []string{"duvan_xyz@kindle.com", "Scriba &lt;kindle@ejemplo.com&gt;", "Configurada"} {
		if !strings.Contains(body, want) {
			t.Errorf("la página de conectores no contiene %q", want)
		}
	}
	if strings.Contains(body, "re_SECRETA_123") {
		t.Error("la API key aparece en el HTML")
	}

	c, ok, err := e.srv.State.Connector("kindle")
	if err != nil || !ok {
		t.Fatalf("conector kindle: ok=%v err=%v", ok, err)
	}
	if bytes.Contains(c.Secret, []byte("re_SECRETA_123")) {
		t.Error("el secreto guardado contiene la key en claro")
	}
}

func TestKindleValidacion(t *testing.T) {
	casos := []struct {
		name       string
		to, from   string
		key        string
		wantStatus int
		wantBody   string
	}{
		{"correo que no es kindle", "x@gmail.com", "Scriba <kindle@ejemplo.com>", "re_X", http.StatusBadRequest, ""},
		{"remitente sin forma", "duvan_xyz@kindle.com", "sin-arroba", "re_X", http.StatusBadRequest, ""},
		{"sin key ni conector previo", "duvan_xyz@kindle.com", "Scriba <kindle@ejemplo.com>", "", http.StatusBadRequest, "Falta la API key"},
		{"free.kindle.com vale", "yo@free.kindle.com", "Scriba <kindle@ejemplo.com>", "re_X", http.StatusSeeOther, ""},
	}
	for _, tc := range casos {
		t.Run(tc.name, func(t *testing.T) {
			e := newSetupServer(t)
			res, body := post(t, e.h, "/conectores/kindle", url.Values{
				"to": {tc.to}, "from": {tc.from}, "key": {tc.key},
			}, nil)
			if res.StatusCode != tc.wantStatus {
				t.Fatalf("got %d, want %d; body: %s", res.StatusCode, tc.wantStatus, body)
			}
			if tc.wantBody != "" && !strings.Contains(body, tc.wantBody) {
				t.Errorf("el cuerpo no contiene %q", tc.wantBody)
			}
			_, ok, err := e.srv.State.Connector("kindle")
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantStatus == http.StatusBadRequest && ok {
				t.Error("el conector se guardó pese al 400")
			}
			if tc.wantStatus == http.StatusSeeOther && !ok {
				t.Error("el conector no se guardó pese al 303")
			}
		})
	}
}

func TestKindleConservaKey(t *testing.T) {
	e := newSetupServer(t)
	saveKindle(t, e, "re_UNO")

	res, body := post(t, e.h, "/conectores/kindle", url.Values{
		"to":   {"otro@kindle.com"},
		"from": {"Scriba <kindle@ejemplo.com>"},
		"key":  {""},
	}, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("re-guardar con key vacía: got %d, want 303; body: %s", res.StatusCode, body)
	}
	cfg, ok, err := e.srv.kindleConfig()
	if err != nil || !ok {
		t.Fatalf("kindleConfig: ok=%v err=%v", ok, err)
	}
	if cfg.APIKey != "re_UNO" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "re_UNO")
	}
	if cfg.To != "otro@kindle.com" {
		t.Errorf("To = %q, want %q", cfg.To, "otro@kindle.com")
	}
}

func TestKindleBotonSoloConConector(t *testing.T) {
	e := kindleNoteEnv(t)

	_, body := get(t, e.h, "/n/nota.md", nil)
	if strings.Contains(body, "Enviar a Kindle") {
		t.Error("el botón aparece sin conector configurado")
	}

	saveKindle(t, e, "re_SECRETA_123")
	_, body = get(t, e.h, "/n/nota.md", nil)
	if !strings.Contains(body, "Enviar a Kindle") {
		t.Error("el botón no aparece con el conector configurado")
	}
	if !strings.Contains(body, `hx-post="/kindle/nota.md"`) {
		t.Error("el botón no apunta a /kindle/nota.md")
	}
}

func TestKindleEnviar(t *testing.T) {
	e := kindleNoteEnv(t)
	saveKindle(t, e, "re_SECRETA_123")
	ts, got := fakeResend(t, http.StatusOK, `{"id":"x"}`)
	e.srv.KindleEndpoint = ts.URL

	res, body := post(t, e.h, "/kindle/nota.md", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", res.StatusCode)
	}
	if !strings.Contains(body, "Enviado a tu Kindle") {
		t.Errorf("el cuerpo no contiene el estado de enviado: %s", body)
	}
	if got.auth != "Bearer re_SECRETA_123" {
		t.Errorf("Authorization = %q, want %q", got.auth, "Bearer re_SECRETA_123")
	}
	to, _ := got.body["to"].([]any)
	if len(to) != 1 || to[0] != "duvan_xyz@kindle.com" {
		t.Errorf("to = %v, want [duvan_xyz@kindle.com]", got.body["to"])
	}
	atts, _ := got.body["attachments"].([]any)
	if len(atts) == 0 {
		t.Error("el correo salió sin adjunto EPUB")
	}
}

func TestKindleEnviarError403(t *testing.T) {
	e := kindleNoteEnv(t)
	saveKindle(t, e, "re_SECRETA_123")
	ts, _ := fakeResend(t, http.StatusForbidden, `{"message":"The domain is not verified"}`)
	e.srv.KindleEndpoint = ts.URL

	res, body := post(t, e.h, "/kindle/nota.md", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", res.StatusCode)
	}
	for _, want := range []string{`role="alert"`, "403", "not verified"} {
		if !strings.Contains(body, want) {
			t.Errorf("el cuerpo no contiene %q: %s", want, body)
		}
	}
	if strings.Contains(body, "re_SECRETA_123") {
		t.Error("la API key aparece en el mensaje de error")
	}
}

func TestKindleRutas(t *testing.T) {
	e := kindleNoteEnv(t)

	res, _ := get(t, e.h, "/kindle/nota.md", nil)
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /kindle/nota.md: got %d, want 405", res.StatusCode)
	}

	res, body := post(t, e.h, "/kindle/nota.md", nil, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "no está configurado") {
		t.Errorf("sin conector: got %d, want 200 con aviso; body: %s", res.StatusCode, body)
	}

	saveKindle(t, e, "re_SECRETA_123")
	res, _ = post(t, e.h, "/kindle/no-existe.md", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("nota inexistente: got %d, want 404", res.StatusCode)
	}

	res, _ = post(t, e.h, "/kindle/nota.md", nil, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site: got %d, want 403", res.StatusCode)
	}
}

func TestKindlePruebaYDesconectar(t *testing.T) {
	e := newSetupServer(t)
	saveKindle(t, e, "re_SECRETA_123")
	ts, _ := fakeResend(t, http.StatusOK, `{"id":"x"}`)
	e.srv.KindleEndpoint = ts.URL

	res, body := post(t, e.h, "/conectores/kindle/prueba", nil, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Enviado a tu Kindle") {
		t.Errorf("prueba: got %d, want 200 con estado enviado; body: %s", res.StatusCode, body)
	}

	res, _ = post(t, e.h, "/conectores/kindle/desconectar", nil, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("desconectar: got %d, want 303", res.StatusCode)
	}
	_, ok, err := e.srv.State.Connector("kindle")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("el conector sigue guardado tras desconectar")
	}

	_, body = get(t, e.h, "/conectores", nil)
	if !strings.Contains(body, "Desconectado") {
		t.Error("la página no muestra el conector como desconectado")
	}
}
