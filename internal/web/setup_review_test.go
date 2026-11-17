package web

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/duv0-x/scriba/internal/github"
)

func TestZipConHistorialGit(t *testing.T) {
	e := newSetupServer(t)
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for _, name := range []string{"nota.md", ".git/config"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte("x\n"))
	}
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

	loc := rec.Result().Header.Get("Location")
	if rec.Code != http.StatusSeeOther || loc != "/?aviso=historial" {
		t.Fatalf("status %d Location %q\n%s", rec.Code, loc, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(e.srv.V.Root, ".git", "config")); err == nil {
		t.Error("se importó el .git del zip")
	}
	if _, body := get(t, e.h, loc, nil); !strings.Contains(body, "El .zip traía historial de git (.git)") {
		t.Errorf("falta el aviso en la home:\n%s", body)
	}
	if _, body := get(t, e.h, "/", nil); strings.Contains(body, "historial de git") {
		t.Error("el aviso no debe salir sin el parámetro")
	}
}

func TestDesconectarDuranteClone(t *testing.T) {
	e := newSetupServer(t)
	connect(t, e)
	e.git.cloneStarted, e.git.cloneHold = make(chan struct{}), make(chan struct{})
	done := make(chan string)
	go func() {
		res, body := post(t, e.h, "/conectores/github/repo", url.Values{"repo": {"duv0-x/ia-kb"}}, nil)
		done <- res.Status + "\n" + body
	}()
	<-e.git.cloneStarted
	if res, _ := post(t, e.h, "/conectores/github/desconectar", nil, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("desconectar = %d", res.StatusCode)
	}
	close(e.git.cloneHold)
	out := <-done
	if !strings.Contains(out, "se desconectó durante la importación") {
		t.Errorf("respuesta del import:\n%s", out)
	}
	if _, ok, _ := e.srv.State.Connector("github"); ok {
		t.Fatal("el conector reapareció tras desconectar")
	}
	if !e.srv.vaultEmpty() || e.imported != 0 {
		t.Errorf("vault vacío %v, OnImported %d", e.srv.vaultEmpty(), e.imported)
	}
	if v, _, _ := e.srv.State.Setting("initialized"); v == "1" {
		t.Error("quedó initialized")
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(e.srv.V.Root), ".scriba-clone-*")); len(m) != 0 {
		t.Errorf("quedaron temporales: %v", m)
	}
}

// conVault deja una nota en el vault para que la importación sea un enlace.
func conVault(t *testing.T, e *setupEnv) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.srv.V.Root, "nota.md"), []byte("# Nota\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.git.isRepo = true
}

func TestEnlazar(t *testing.T) {
	t.Run("origin distinto", func(t *testing.T) {
		e := newSetupServer(t)
		connect(t, e)
		conVault(t, e)
		e.git.remote = "https://github.com/otro/repo.git"
		res, body := post(t, e.h, "/conectores/github/repo", url.Values{"repo": {"duv0-x/ia-kb"}}, nil)
		if res.StatusCode != http.StatusConflict || !strings.Contains(body, "Este vault apunta a https://github.com/otro/repo.git; no se puede enlazar a duv0-x/ia-kb") {
			t.Fatalf("status %d\n%s", res.StatusCode, body)
		}
		if c, _, _ := e.srv.State.Connector("github"); c.Repo != "" || e.git.setRemote != "" {
			t.Errorf("repo %q, setRemote %q", c.Repo, e.git.setRemote)
		}
	})
	t.Run("origin igual con otro formato", func(t *testing.T) {
		e := newSetupServer(t)
		connect(t, e)
		conVault(t, e)
		e.git.remote = "https://github.com/Duv0-X/IA-KB"
		if res, _ := post(t, e.h, "/conectores/github/repo", url.Values{"repo": {"duv0-x/ia-kb"}}, nil); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("status %d", res.StatusCode)
		}
		if c, _, _ := e.srv.State.Connector("github"); c.Repo != "duv0-x/ia-kb" || e.git.setRemote != "" {
			t.Errorf("repo %q, setRemote %q", c.Repo, e.git.setRemote)
		}
	})
	t.Run("sin origin", func(t *testing.T) {
		e := newSetupServer(t)
		connect(t, e)
		conVault(t, e)
		if res, _ := post(t, e.h, "/conectores/github/repo", url.Values{"repo": {"duv0-x/ia-kb"}}, nil); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("status %d", res.StatusCode)
		}
		if e.git.setRemote != "https://github.com/duv0-x/ia-kb.git" {
			t.Errorf("setRemote %q", e.git.setRemote)
		}
		if c, _, _ := e.srv.State.Connector("github"); c.Repo != "duv0-x/ia-kb" || c.Account != "duv0-x" {
			t.Errorf("conector %+v", c)
		}
	})
	t.Run("no es repo git", func(t *testing.T) {
		e := newSetupServer(t)
		connect(t, e)
		conVault(t, e)
		e.git.isRepo = false
		if res, _ := post(t, e.h, "/conectores/github/repo", url.Values{"repo": {"duv0-x/ia-kb"}}, nil); res.StatusCode != http.StatusConflict {
			t.Fatalf("status %d", res.StatusCode)
		}
	})
}

func TestCrearBaseConcurrente(t *testing.T) {
	e := newSetupServer(t)
	var wg sync.WaitGroup
	codes := make([]int, 2)
	bodies := make([]string, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, body := post(t, e.h, "/bienvenida/nueva", nil, nil)
			codes[i], bodies[i] = res.StatusCode, body
		}()
	}
	wg.Wait()
	created, rejected := 0, 0
	for i := range 2 {
		switch {
		case codes[i] == http.StatusSeeOther:
			created++
		case codes[i] == http.StatusConflict && strings.Contains(bodies[i], "La base ya tiene notas"):
			rejected++
		}
	}
	if created != 1 || rejected != 1 || e.imported != 1 {
		t.Fatalf("codes %v, OnImported %d", codes, e.imported)
	}
}

func TestSinSecretosEnHTML(t *testing.T) {
	e := newSetupServer(t)
	var bodies []string
	add := func(_ *http.Response, b string) { bodies = append(bodies, b) }
	post(t, e.h, "/conectores/github/iniciar", nil, nil)
	add(get(t, e.h, "/conectores", nil))
	e.gh.pollErr = github.ErrPending
	add(get(t, e.h, "/conectores/github/estado", nil))
	e.gh.pollErr = nil
	add(get(t, e.h, "/conectores/github/estado", nil))
	add(get(t, e.h, "/conectores", nil))
	// Segunda pestaña sondeando cuando ya terminó: redirige, no falla.
	res, b := get(t, e.h, "/conectores/github/estado", nil)
	add(res, b)
	if res.Header.Get("HX-Redirect") != "/conectores" {
		t.Errorf("otra pestaña: HX-Redirect %q", res.Header.Get("HX-Redirect"))
	}
	post(t, e.h, "/conectores/github/repo", url.Values{"repo": {"duv0-x/ia-kb"}}, nil)
	_, b = get(t, e.h, "/conectores", nil)
	add(nil, b)
	if strings.Contains(b, "Último push") || !strings.Contains(b, "Se activa cuando los agentes empiecen a escribir") {
		t.Errorf("tarjeta conectada:\n%s", b)
	}
	for _, b := range bodies {
		if strings.Contains(b, "DEVICE-SECRETO-123") || strings.Contains(b, e.gh.token) {
			t.Fatalf("el HTML filtra el device_code o el token:\n%s", b)
		}
	}
}

func TestSinClientID(t *testing.T) {
	e := newSetupServer(t)
	e.srv.ClientID = ""
	_, body := get(t, e.h, "/conectores", nil)
	if !strings.Contains(body, "Falta configurar el client_id de la OAuth App de Scriba (ver README)") ||
		!strings.Contains(body, `<button class="btn primary" type="button" disabled>Conectar con GitHub</button>`) ||
		strings.Contains(body, `action="/conectores/github/iniciar"`) {
		t.Fatalf("tarjeta sin client_id:\n%s", body)
	}
	post(t, e.h, "/conectores/github/iniciar", nil, nil)
	if e.gh.starts != 0 {
		t.Fatalf("llamó a GitHub %d veces", e.gh.starts)
	}
}

func TestGitVaultRemote(t *testing.T) {
	dir := t.TempDir()
	if err := (GitVault{}).Init(dir); err != nil {
		t.Fatal(err)
	}
	if u, err := (GitVault{}).Remote(dir); err != nil || u != "" {
		t.Fatalf("sin origin: %q %v", u, err)
	}
	const want = "https://github.com/duv0-x/ia-kb.git"
	if err := (GitVault{}).SetRemote(dir, want); err != nil {
		t.Fatal(err)
	}
	if u, err := (GitVault{}).Remote(dir); err != nil || u != want {
		t.Fatalf("origin: %q %v", u, err)
	}
}
