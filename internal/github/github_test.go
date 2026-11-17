package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "gho_supersecreto123"

// servidor crea un Client apuntando WebURL y APIURL al mismo httptest.Server.
func servidor(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), WebURL: srv.URL, APIURL: srv.URL}
}

func TestStartDevice(t *testing.T) {
	c := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/login/device/code" {
			t.Errorf("petición inesperada %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		if r.FormValue("client_id") != "cid" || r.FormValue("scope") != "repo" {
			t.Errorf("form = %v", r.Form)
		}
		fmt.Fprint(w, `{"device_code":"dc","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","interval":5,"expires_in":900}`)
	})
	dc, err := c.StartDevice(context.Background(), "cid", "repo")
	if err != nil {
		t.Fatal(err)
	}
	want := DeviceCode{DeviceCode: "dc", UserCode: "ABCD-1234", VerificationURI: "https://github.com/login/device", Interval: 5, ExpiresIn: 900}
	if dc != want {
		t.Fatalf("dc = %+v", dc)
	}
}

func TestStartDeviceSinClientID(t *testing.T) {
	_, err := New().StartDevice(context.Background(), "", "repo")
	if err == nil || !strings.Contains(err.Error(), "falta el client_id") {
		t.Fatalf("err = %v", err)
	}
}

func TestPollToken(t *testing.T) {
	casos := []struct {
		nombre, resp string
		wantErr      error
		wantMsg      string
		want         Token
	}{
		{"pending", `{"error":"authorization_pending"}`, ErrPending, "", Token{}},
		{"slow_down", `{"error":"slow_down","interval":10}`, ErrSlowDown, "", Token{}},
		{"expired", `{"error":"expired_token"}`, ErrExpired, "", Token{}},
		{"denied", `{"error":"access_denied"}`, ErrDenied, "", Token{}},
		{"desconocido", `{"error":"incorrect_client_credentials","error_description":"client_id inválido"}`, nil, "client_id inválido", Token{}},
		{"exito", `{"access_token":"` + testToken + `","token_type":"bearer","scope":"repo"}`, nil, "", Token{AccessToken: testToken, Scope: "repo"}},
	}
	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			c := servidor(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/login/oauth/access_token" ||
					r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" ||
					r.FormValue("device_code") != "dc" || r.FormValue("client_id") != "cid" {
					t.Errorf("petición inesperada %s %v", r.URL.Path, r.Form)
				}
				fmt.Fprint(w, tc.resp)
			})
			tok, err := c.PollToken(context.Background(), "cid", "dc")
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, quiero %v", err, tc.wantErr)
				}
			case tc.wantMsg != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
					t.Fatalf("err = %v", err)
				}
			default:
				if err != nil || tok != tc.want {
					t.Fatalf("tok = %+v, err = %v", tok, err)
				}
			}
		})
	}
}

func revisarHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	for k, v := range map[string]string{
		"Authorization":        "Bearer " + testToken,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
		"User-Agent":           "scriba",
	} {
		if got := r.Header.Get(k); got != v {
			t.Errorf("%s = %q, quiero %q", k, got, v)
		}
	}
}

func TestUser(t *testing.T) {
	c := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		revisarHeaders(t, r)
		if r.URL.Path != "/user" {
			t.Errorf("path = %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"login":"duv0-x","id":1}`)
	})
	login, err := c.User(context.Background(), testToken)
	if err != nil || login != "duv0-x" {
		t.Fatalf("login = %q, err = %v", login, err)
	}
}

func TestReposPaginado(t *testing.T) {
	var base string
	c := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		revisarHeaders(t, r)
		q := r.URL.Query()
		if q.Get("per_page") != "100" || q.Get("sort") != "updated" || q.Get("affiliation") != "owner,collaborator,organization_member" {
			t.Errorf("query = %v", q)
		}
		if q.Get("page") == "" {
			w.Header().Set("Link", `<`+base+`/user/repos?per_page=100&sort=updated&affiliation=owner,collaborator,organization_member&page=2>; rel="next", <`+base+`/user/repos?page=2>; rel="last"`)
			fmt.Fprint(w, `[{"full_name":"duv0-x/a","private":true,"default_branch":"main","clone_url":"https://github.com/duv0-x/a.git"}]`)
			return
		}
		fmt.Fprint(w, `[{"full_name":"duv0-x/b","private":false,"default_branch":"master","clone_url":"https://github.com/duv0-x/b.git"}]`)
	})
	base = c.APIURL
	repos, err := c.Repos(context.Background(), testToken)
	if err != nil {
		t.Fatal(err)
	}
	want := []Repo{
		{FullName: "duv0-x/a", Private: true, DefaultBranch: "main", CloneURL: "https://github.com/duv0-x/a.git"},
		{FullName: "duv0-x/b", Private: false, DefaultBranch: "master", CloneURL: "https://github.com/duv0-x/b.git"},
	}
	if len(repos) != 2 || repos[0] != want[0] || repos[1] != want[1] {
		t.Fatalf("repos = %+v", repos)
	}
}

func TestReposLinkAOtroHost(t *testing.T) {
	c := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://evil.example/user/repos?page=2>; rel="next"`)
		fmt.Fprint(w, `[]`)
	})
	if _, err := c.Repos(context.Background(), testToken); err == nil {
		t.Fatal("se esperaba error al seguir un Link a otro host")
	}
}

func TestNoAutorizadoSinToken(t *testing.T) {
	c := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
	})
	_, err := c.User(context.Background(), testToken)
	if err == nil {
		t.Fatal("se esperaba error con 401")
	}
	if strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %q", err)
	}
	if _, err := c.Repos(context.Background(), testToken); err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("Repos err = %v", err)
	}
}
