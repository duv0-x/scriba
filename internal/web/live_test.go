package web

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duv0-x/scriba/internal/events"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/vault"
)

type liveEnv struct {
	srv  *Server
	h    http.Handler
	root string
}

func newLiveServer(t *testing.T, withState bool) liveEnv {
	t.Helper()
	root := t.TempDir()
	write := func(p, s string) {
		abs := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notes/a.md", "# A\n\nPrimer párrafo.\n\n| x | y |\n|---|---|\n| 1 | 2 |\n")
	write("notes/b.md", "# B\n\nOtra nota.\n")
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(t.TempDir(), "scriba.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	if err := ix.Rebuild(v); err != nil {
		t.Fatal(err)
	}
	srv := &Server{KBs: oneKB(&kbs.KB{V: v, Ix: ix, Bus: events.NewBus(), Presence: events.NewPresence(time.Minute, nil)})}
	if withState {
		st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		srv.State = st
	}
	return liveEnv{srv: srv, h: srv.Handler(), root: root}
}

func TestEventsSSE(t *testing.T) {
	e := newLiveServer(t, false)
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/k/principal/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	sc := bufio.NewScanner(res.Body)
	next := func(name string) string {
		t.Helper()
		for sc.Scan() {
			if sc.Text() == "event: "+name && sc.Scan() {
				return sc.Text()
			}
		}
		t.Fatalf("no llegó %q: %v", name, sc.Err())
		return ""
	}
	if d := next("presence"); d != "data: []" {
		t.Fatalf("presence inicial = %q", d)
	}
	ev := events.Event{Kind: events.Changed, Path: "notes/a.md", Agent: "claude", At: time.Now(), Hash: "h1"}
	e.srv.KBs.Default().Presence.Observe(ev)
	e.srv.KBs.Default().Bus.Publish(ev)
	if d := next("changed"); d != `data: {"path":"notes/a.md","agent":"claude","kind":"changed","hash":"h1"}` {
		t.Fatalf("changed = %q", d)
	}
	if d := next("presence"); !strings.Contains(d, `"agent":"claude"`) || !strings.Contains(d, `"path":"notes/a.md"`) {
		t.Fatalf("presence = %q", d)
	}
}

func TestEventsPresenciaExpira(t *testing.T) {
	e := newLiveServer(t, false)
	e.srv.KBs.Default().Presence = events.NewPresence(300*time.Millisecond, nil)
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/k/principal/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	next := func(name string) string {
		t.Helper()
		for sc.Scan() {
			if sc.Text() == "event: "+name && sc.Scan() {
				return sc.Text()
			}
		}
		t.Fatalf("no llegó %q: %v", name, sc.Err())
		return ""
	}
	if d := next("presence"); d != "data: []" {
		t.Fatalf("presence inicial = %q", d)
	}
	ev := events.Event{Kind: events.Changed, Path: "notes/a.md", Agent: "claude", At: time.Now()}
	e.srv.KBs.Default().Presence.Observe(ev)
	e.srv.KBs.Default().Bus.Publish(ev)
	if d := next("changed"); !strings.Contains(d, `"agent":"claude"`) {
		t.Fatalf("changed = %q", d)
	}
	if d := next("presence"); !strings.Contains(d, `"agent":"claude"`) {
		t.Fatalf("presence con agente = %q", d)
	}
	// Sin publicar nada más, la ventana vence y el stream avisa con una presencia vacía.
	start := time.Now()
	if d := next("presence"); d != "data: []" {
		t.Fatalf("presence al expirar = %q", d)
	}
	if dt := time.Since(start); dt > 2*time.Second {
		t.Fatalf("la presencia tardó %v en expirar", dt)
	}
}

func TestEventsSinBus(t *testing.T) {
	e := newLiveServer(t, false)
	e.srv.KBs.Default().Bus = nil
	if res, _ := get(t, e.srv.Handler(), "/k/principal/events", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestSinLeer(t *testing.T) {
	e := newLiveServer(t, true)
	// Primera vista: baseline y lectura registrada, sin marcas.
	_, body := get(t, e.h, "/k/principal/n/notes/a.md", nil)
	if strings.Contains(body, `class="chg"`) || strings.Contains(body, bannerChanged) {
		t.Fatal("la primera vista no debe marcar cambios")
	}
	if !strings.Contains(body, `data-note="notes/a.md"`) {
		t.Error("falta data-note en el lector")
	}
	if _, ok, err := e.srv.State.LastRead(testKB, "default", "notes/a.md"); err != nil || !ok {
		t.Fatalf("LastRead = %v %v", ok, err)
	}

	abs := filepath.Join(e.root, "notes/a.md")
	if err := os.WriteFile(abs, []byte("# A\n\nPrimer párrafo.\n\n| x | y |\n|---|---|\n| 1 | 2 |\n\nBloque nuevo del agente.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Folios (desde otra nota) muestra el punto de a.md.
	_, body = get(t, e.h, "/k/principal/n/notes/b.md", nil)
	if !strings.Contains(body, `data-path="notes/a.md" class="unread"`) {
		t.Error("Folios no marca notes/a.md como sin leer")
	}
	if strings.Contains(body, `data-path="notes/b.md" class="unread"`) {
		t.Error("notes/b.md no debe salir sin leer")
	}

	_, body = get(t, e.h, "/k/principal/n/notes/a.md", nil)
	if strings.Count(body, `class="chg"`) != 1 || !strings.Contains(body, `<div class="chg"><p>Bloque nuevo del agente.</p>`) {
		t.Fatalf("segunda vista: quiero .chg solo en el bloque nuevo\n%s", body)
	}
	if !strings.Contains(body, bannerChanged) || !strings.Contains(body, "<table>") {
		t.Error("falta el banner o la tabla")
	}
	if _, body = get(t, e.h, "/k/principal/n/notes/a.md", nil); strings.Contains(body, `class="chg"`) || strings.Contains(body, bannerChanged) {
		t.Error("tercera vista: no debe quedar nada marcado")
	}

	// Fragmento: solo la prosa, con la marca, y también marca leído.
	if err := os.WriteFile(abs, []byte("# A\n\nPrimer párrafo.\n\nOtro bloque.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, frag := get(t, e.h, "/k/principal/n/notes/a.md?fragmento=1", nil)
	if strings.Contains(frag, "<html") || !strings.Contains(frag, `<div class="chg"><p>Otro bloque.</p>`) {
		t.Fatalf("fragmento = %s", frag)
	}
	if _, body = get(t, e.h, "/k/principal/n/notes/a.md", nil); strings.Contains(body, `class="chg"`) {
		t.Error("el fragmento no marcó la nota como leída")
	}
}

func TestNotaNuevaDesdeBaseline(t *testing.T) {
	e := newLiveServer(t, true)
	get(t, e.h, "/k/principal/n/notes/a.md", nil) // fija el baseline
	time.Sleep(10 * time.Millisecond)
	abs := filepath.Join(e.root, "notes/b.md")
	if err := os.WriteFile(abs, []byte("# B\n\nCambiada.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, e.h, "/k/principal/n/notes/b.md", nil)
	if !strings.Contains(body, bannerNew) || strings.Contains(body, `class="chg"`) {
		t.Fatal("nota nueva: quiero el banner sin resaltar bloques")
	}
}

func TestLectorSinState(t *testing.T) {
	e := newLiveServer(t, false)
	res, body := get(t, e.h, "/k/principal/n/notes/a.md", nil)
	if res.StatusCode != 200 || strings.Contains(body, `class="chg"`) {
		t.Fatalf("status %d", res.StatusCode)
	}
	if _, frag := get(t, e.h, "/k/principal/n/notes/a.md?fragmento=1", nil); strings.Contains(frag, "<html") || !strings.Contains(frag, "Primer párrafo") {
		t.Fatalf("fragmento = %s", frag)
	}
}

func TestMdBlocks(t *testing.T) {
	md := "Uno\n\n```\na\n\nb\n```\n\n- ítem\n\n  segundo párrafo\n- otro\n\nFin"
	got := mdBlocks(md)
	want := []string{"Uno", "```\na\n\nb\n```", "- ítem\n\n  segundo párrafo\n- otro", "Fin"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("mdBlocks = %q", got)
	}
}

// La instantánea leída se compara con la misma transformación que el contenido nuevo: un H1 o
// un frontmatter pegados al primer párrafo no lo marcan como cambiado.
func TestDiffH1YFrontmatterPegados(t *testing.T) {
	cases := []struct{ name, before, after, chg string }{
		{"h1", "# C\nLinea uno.\n\nLinea dos.", "# C\nLinea uno.\n\nLinea dos cambiada.", "Linea dos cambiada."},
		{"frontmatter", "---\ntitle: D\n---\nIntro.\n\nB.", "---\ntitle: D\n---\nIntro.\n\nB cambiado.", "B cambiado."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newLiveServer(t, true)
			abs := filepath.Join(e.root, "notes/c.md")
			if err := os.WriteFile(abs, []byte(c.before), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := e.srv.KBs.Default().Ix.Rebuild(e.srv.KBs.Default().V); err != nil {
				t.Fatal(err)
			}
			get(t, e.h, "/k/principal/n/notes/c.md", nil)
			if err := os.WriteFile(abs, []byte(c.after), 0o644); err != nil {
				t.Fatal(err)
			}
			_, frag := get(t, e.h, "/k/principal/n/notes/c.md?fragmento=1", nil)
			if strings.Count(frag, `class="chg"`) != 1 || !strings.Contains(frag, `<div class="chg"><p>`+c.chg+`</p>`) {
				t.Fatalf("quiero 1 bloque marcado (%q)\n%s", c.chg, frag)
			}
		})
	}
}

func TestLiveJSNavegacionParcial(t *testing.T) {
	e := newLiveServer(t, false)
	_, js := get(t, e.h, "/static/live.js", nil)
	compact := strings.Join(strings.Fields(js), "")
	for _, want := range []string{
		`constpath=location.pathname;`,
		`fetch(path+"?fragmento=1"`,
		`if(path!==location.pathname)return;`,
		`prose.innerHTML=html;window.htmx?.process(prose);play(prose);`,
		`constdone=()=>window.htmx?.process(prose);`,
		`caret.remove();done();`,
	} {
		if !strings.Contains(compact, want) {
			t.Errorf("live.js sin %q", want)
		}
	}
}
