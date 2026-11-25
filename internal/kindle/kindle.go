package kindle

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Config es la configuración del conector Kindle de la instancia.
type Config struct {
	To       string // correo de la Kindle
	From     string // remitente verificado en Resend
	APIKey   string // API key de Resend; secreta
	Endpoint string // vacío = API de Resend; para tests
}

// Send genera el EPUB de la nota y lo manda a la Kindle. El error es legible para el usuario
// y nunca contiene la API key.
func Send(ctx context.Context, n Note, c Config) error {
	if strings.TrimSpace(c.To) == "" || strings.TrimSpace(c.From) == "" || strings.TrimSpace(c.APIKey) == "" {
		return errors.New("el conector Kindle no está configurado")
	}
	data, name, err := BuildEPUB(n)
	if err != nil {
		return fmt.Errorf("no se pudo generar el EPUB: %w", err)
	}
	subject := n.Title
	if subject == "" {
		subject = n.Path
	}
	r := &Resend{APIKey: c.APIKey, From: c.From, Endpoint: c.Endpoint}
	err = r.Send(ctx, Mail{To: c.To, Subject: subject, Filename: name, Attachment: data})
	if err != nil {
		return errors.New(strings.ReplaceAll(err.Error(), c.APIKey, "[redactada]"))
	}
	return nil
}
