package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

func TestConectorOAuthDePuntaAPunta(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "vault")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Con una nota la base cuenta como inicializada y la web no redirige a la bienvenida.
	if err := os.WriteFile(filepath.Join(root, "nota.md"), []byte("# Nota\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, serveOpts{data: dir, clientID: "x", tz: "UTC", ln: ln})
	}()
	t.Cleanup(func() { cancel(); <-done })
	base := "http://" + ln.Addr().String()

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// approve hace de dueño: abre el consentimiento y pulsa Aprobar.
	approve := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		u, err := url.Parse(args.URL)
		if err != nil {
			return nil, err
		}
		page, err := http.Get(args.URL)
		if err != nil {
			return nil, err
		}
		page.Body.Close()
		if page.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("consentimiento: %d", page.StatusCode)
		}
		form := u.Query()
		form.Set("agent", "claude")
		form.Set("decision", "approve")
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+u.Path, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		res, err := noRedirect.Do(req)
		if err != nil {
			return nil, err
		}
		res.Body.Close()
		loc, err := res.Location()
		if err != nil {
			return nil, fmt.Errorf("aprobar: %d %w", res.StatusCode, err)
		}
		return &auth.AuthorizationResult{Code: loc.Query().Get("code"), State: loc.Query().Get("state"), Iss: loc.Query().Get("iss")}, nil
	}
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "prueba", RedirectURIs: []string{"http://127.0.0.1:1/callback"}}},
		AuthorizationCodeFetcher: approve,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "1"}, nil).
		Connect(ctx, &sdk.StreamableClientTransport{Endpoint: base + "/mcp", OAuthHandler: handler}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 8 {
		t.Fatalf("tools = %d, quería 8", len(tools.Tools))
	}
}
