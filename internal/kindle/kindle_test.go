package kindle

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendSinConfig(t *testing.T) {
	err := Send(context.Background(), Note{Title: "x"}, Config{To: "a@kindle.com"})
	if err == nil || !strings.Contains(err.Error(), "no está configurado") {
		t.Errorf("error = %v, quiero que mencione %q", err, "no está configurado")
	}
}

func TestSendEPUB(t *testing.T) {
	var req struct {
		To          []string `json:"to"`
		Subject     string   `json:"subject"`
		Attachments []struct {
			Filename string `json:"filename"`
			Content  string `json:"content"`
		} `json:"attachments"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decodificar JSON: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := Note{Title: "ADR-012: Kindle", Date: "2026-11-25", Path: "docs/adr/x.md", Body: "<p>hola</p>"}
	c := Config{To: "yo@kindle.com", From: "Scriba <k@ej.com>", APIKey: "re_secreta", Endpoint: srv.URL}
	if err := Send(context.Background(), n, c); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(req.To) != 1 || req.To[0] != "yo@kindle.com" {
		t.Errorf("to = %v, quiero [yo@kindle.com]", req.To)
	}
	if req.Subject != "ADR-012: Kindle" {
		t.Errorf("subject = %q, quiero %q", req.Subject, "ADR-012: Kindle")
	}
	if len(req.Attachments) != 1 {
		t.Fatalf("attachments = %d, quiero 1", len(req.Attachments))
	}
	if req.Attachments[0].Filename != "ADR-012 Kindle.epub" {
		t.Errorf("filename = %q, quiero %q", req.Attachments[0].Filename, "ADR-012 Kindle.epub")
	}
	data, err := base64.StdEncoding.DecodeString(req.Attachments[0].Content)
	if err != nil {
		t.Fatalf("base64 del adjunto: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if zr.File[0].Name != "mimetype" {
		t.Errorf("primera entrada = %q, quiero %q", zr.File[0].Name, "mimetype")
	}
}

func TestSendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"statusCode":403,"message":"invalid api key re_secreta"}`))
	}))
	defer srv.Close()

	c := Config{To: "yo@kindle.com", From: "Scriba <k@ej.com>", APIKey: "re_secreta", Endpoint: srv.URL}
	err := Send(context.Background(), Note{Title: "x"}, c)
	if err == nil {
		t.Fatal("Send no devolvió error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, quiero que contenga %q", err, "403")
	}
	if !strings.Contains(err.Error(), "[redactada]") {
		t.Errorf("error = %q, quiero que contenga %q", err, "[redactada]")
	}
	if strings.Contains(err.Error(), "re_secreta") {
		t.Errorf("error = %q, no debe contener la API key", err)
	}
}
