package kindle

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Mail es un correo con un adjunto.
type Mail struct {
	To         string
	Subject    string
	Filename   string
	Attachment []byte
}

// Resend envía correos por la API de Resend.
type Resend struct {
	APIKey string
	From   string // p. ej. "Scriba <kindle@dominio-verificado>"
	// Endpoint vacío = https://api.resend.com/emails; los tests apuntan a otro.
	Endpoint string
	HTTP     *http.Client // nil = cliente con Timeout de 30 s
}

type resendAttachment struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

type resendEmail struct {
	From        string             `json:"from"`
	To          []string           `json:"to"`
	Subject     string             `json:"subject"`
	Text        string             `json:"text"`
	Attachments []resendAttachment `json:"attachments"`
}

func (r *Resend) Send(ctx context.Context, m Mail) error {
	body, err := json.Marshal(resendEmail{
		From:    r.From,
		To:      []string{m.To},
		Subject: m.Subject,
		Text:    "Nota de Scriba. Va adjunta como EPUB.",
		Attachments: []resendAttachment{{
			Filename: m.Filename,
			Content:  base64.StdEncoding.EncodeToString(m.Attachment),
		}},
	})
	if err != nil {
		return err
	}
	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = "https://api.resend.com/emails"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")
	hc := r.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo contactar a Resend: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		var api struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(b, &api) == nil && api.Message != "" {
			return fmt.Errorf("Resend respondió %d: %s", res.StatusCode, api.Message)
		}
		return fmt.Errorf("Resend respondió %d: %s", res.StatusCode, bytes.TrimSpace(b))
	}
	return nil
}
