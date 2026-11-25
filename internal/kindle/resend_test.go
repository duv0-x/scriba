package kindle

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendSend(t *testing.T) {
	var (
		gotMethod string
		gotAuth   string
		gotCT     string
		gotBody   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("body no es JSON: %v", err)
		}
		w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()

	r := &Resend{APIKey: "re_secreta", From: "Scriba <k@ej.com>", Endpoint: srv.URL}
	attachment := []byte("PK\x03\x04epub")
	err := r.Send(context.Background(), Mail{
		To:         "yo@kindle.com",
		Subject:    "Plan",
		Filename:   "Plan.epub",
		Attachment: attachment,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("método = %q, quiero POST", gotMethod)
	}
	if gotAuth != "Bearer re_secreta" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotBody["from"] != "Scriba <k@ej.com>" {
		t.Errorf("from = %v", gotBody["from"])
	}
	to, _ := gotBody["to"].([]any)
	if len(to) != 1 || to[0] != "yo@kindle.com" {
		t.Errorf("to = %v", gotBody["to"])
	}
	if gotBody["subject"] != "Plan" {
		t.Errorf("subject = %v", gotBody["subject"])
	}
	atts, _ := gotBody["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("attachments = %v", gotBody["attachments"])
	}
	att, _ := atts[0].(map[string]any)
	if att["filename"] != "Plan.epub" {
		t.Errorf("filename = %v", att["filename"])
	}
	content, _ := att["content"].(string)
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		t.Fatalf("content no es base64: %v", err)
	}
	if string(decoded) != string(attachment) {
		t.Errorf("adjunto = %q, quiero %q", decoded, attachment)
	}
}

func TestResendError403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"statusCode":403,"message":"The kindle.com domain is not verified","name":"validation_error"}`))
	}))
	defer srv.Close()

	r := &Resend{APIKey: "re_secreta", From: "Scriba <k@ej.com>", Endpoint: srv.URL}
	err := r.Send(context.Background(), Mail{To: "yo@kindle.com", Subject: "Plan", Filename: "Plan.epub"})
	if err == nil {
		t.Fatal("esperaba error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "403") {
		t.Errorf("error sin 403: %q", msg)
	}
	if !strings.Contains(msg, "domain is not verified") {
		t.Errorf("error sin el mensaje de Resend: %q", msg)
	}
	if strings.Contains(msg, "re_secreta") {
		t.Errorf("el error filtra la API key: %q", msg)
	}
}

func TestResendErrorTextoPlano(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	r := &Resend{APIKey: "re_secreta", From: "Scriba <k@ej.com>", Endpoint: srv.URL}
	err := r.Send(context.Background(), Mail{To: "yo@kindle.com", Subject: "Plan", Filename: "Plan.epub"})
	if err == nil {
		t.Fatal("esperaba error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "500") || !strings.Contains(msg, "boom") {
		t.Errorf("error = %q, quiero 500 y boom", msg)
	}
}
