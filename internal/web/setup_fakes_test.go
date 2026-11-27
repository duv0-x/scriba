package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duv0-x/scriba/internal/github"
	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/index"
	"github.com/duv0-x/scriba/internal/kbs"
	"github.com/duv0-x/scriba/internal/state"
	"github.com/duv0-x/scriba/internal/vault"
)

const testKB = "principal"

// fakeKBs es un Registry de tests: la primera KB es la por defecto; no crea ni borra.
type fakeKBs struct{ list []*kbs.KB }

func oneKB(k *kbs.KB) *fakeKBs {
	if k.Slug == "" {
		k.Slug = testKB
	}
	return &fakeKBs{list: []*kbs.KB{k}}
}

func (f *fakeKBs) Get(slug string) (*kbs.KB, bool) {
	for _, k := range f.list {
		if k.Slug == slug {
			return k, true
		}
	}
	return nil, false
}

func (f *fakeKBs) Default() *kbs.KB { return f.list[0] }

func (f *fakeKBs) List() []string {
	out := make([]string, len(f.list))
	for i, k := range f.list {
		out[i] = k.Slug
	}
	return out
}

func (f *fakeKBs) Create(string, func(string) error) (*kbs.KB, error) {
	return nil, errors.New("fakeKBs: no soportado")
}

func (f *fakeKBs) Delete(string) error { return errors.New("fakeKBs: no soportado") }

type fakeGitHub struct {
	pollErr error
	token   string
	starts  int
}

func (f *fakeGitHub) StartDevice(context.Context, string, string) (github.DeviceCode, error) {
	f.starts++
	return github.DeviceCode{DeviceCode: "DEVICE-SECRETO-123", UserCode: "ABCD-1234",
		VerificationURI: "https://github.com/login/device", Interval: 5, ExpiresIn: 900}, nil
}

func (f *fakeGitHub) PollToken(_ context.Context, _, dc string) (github.Token, error) {
	if dc != "DEVICE-SECRETO-123" {
		return github.Token{}, github.ErrExpired
	}
	if f.pollErr != nil {
		return github.Token{}, f.pollErr
	}
	return github.Token{AccessToken: f.token}, nil
}

func (f *fakeGitHub) User(context.Context, string) (string, error) { return "duv0-x", nil }

func (f *fakeGitHub) Repos(context.Context, string) ([]github.Repo, error) {
	return []github.Repo{{FullName: "duv0-x/ia-kb", Private: true}}, nil
}

type fakeGit struct {
	calls              []string
	cloneURL, cloneTok string
	isRepo             bool
	remote, setRemote  string
	// Si cloneHold no es nil, Clone avisa en cloneStarted y espera a que se cierre cloneHold.
	cloneStarted, cloneHold chan struct{}
}

func (f *fakeGit) IsRepo(string) bool            { return f.isRepo }
func (f *fakeGit) Init(string) error             { f.calls = append(f.calls, "init"); return nil }
func (f *fakeGit) Remote(string) (string, error) { return f.remote, nil }
func (f *fakeGit) SetRemote(_, u string) error   { f.setRemote = u; return nil }
func (f *fakeGit) Clone(_ context.Context, u, dir string, c gitvault.Cred) error {
	if f.cloneHold != nil {
		close(f.cloneStarted)
		<-f.cloneHold
	}
	f.calls = append(f.calls, "clone")
	f.cloneURL, f.cloneTok = u, c.Token
	return os.WriteFile(filepath.Join(dir, "nota.md"), []byte("# Nota\n"), 0o644)
}
func (f *fakeGit) CommitAll(string, string, string) (string, bool, error) {
	f.calls = append(f.calls, "commit")
	return "abc", true, nil
}

type setupEnv struct {
	h   http.Handler
	srv *Server
	kb  *kbs.KB
	gh  *fakeGitHub
	git *fakeGit
}

// newSetupServer arma un Server con vault vacío, índice vacío y State real.
func newSetupServer(t *testing.T) *setupEnv {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "vault")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(filepath.Join(dir, "scriba.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	if err := ix.Rebuild(v); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &setupEnv{gh: &fakeGitHub{token: "gho_TOKENSECRETO"}, git: &fakeGit{}}
	e.kb = &kbs.KB{V: v, Ix: ix}
	e.srv = &Server{KBs: oneKB(e.kb), State: st, Key: bytes.Repeat([]byte{7}, 32),
		GitHub: e.gh, Git: e.git, ClientID: "cid"}
	e.h = e.srv.Handler()
	return e
}

// post envía un formulario same-origin (salvo que hdr diga otra cosa).
func post(t *testing.T, h http.Handler, target string, form url.Values, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if hdr == nil {
		hdr = map[string]string{"Sec-Fetch-Site": "same-origin"}
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result(), rec.Body.String()
}
