// Package mailer sends email over SMTP, or writes it to disk during local development.
package mailer

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/random"
)

// Attachment is a file attached to a message.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message is an email with a plain-text body, an optional HTML body and attachments.
type Message struct {
	To          []string
	Subject     string
	Text        string
	HTML        string
	Attachments []Attachment
}

// Mailer delivers messages.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Build renders msg as an RFC 5322 message with MIME parts.
func Build(from string, msg Message, now time.Time) ([]byte, error) {
	if len(msg.To) == 0 {
		return nil, fmt.Errorf("mailer: message has no recipients")
	}
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("mailer: invalid from address: %w", err)
	}

	var buf bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	header("From", fromAddr.String())
	header("To", strings.Join(msg.To, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", fmt.Sprintf("<%s@%s>", random.Hex(12), domainOf(fromAddr.Address)))
	header("MIME-Version", "1.0")

	mixed := multipart.NewWriter(&buf)
	header("Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", mixed.Boundary()))
	buf.WriteString("\r\n")

	altBody, altBoundary, err := alternativePart(msg)
	if err != nil {
		return nil, err
	}
	part, err := mixed.CreatePart(textproto.MIMEHeader{
		"Content-Type": {fmt.Sprintf("multipart/alternative; boundary=%q", altBoundary)},
	})
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(altBody); err != nil {
		return nil, err
	}

	for _, a := range msg.Attachments {
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		part, err := mixed.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {fmt.Sprintf("%s; name=%q", ct, a.Filename)},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {fmt.Sprintf("attachment; filename=%q", a.Filename)},
		})
		if err != nil {
			return nil, err
		}
		if err := writeBase64(part, a.Data); err != nil {
			return nil, err
		}
	}
	if err := mixed.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func alternativePart(msg Message) ([]byte, string, error) {
	var buf bytes.Buffer
	alt := multipart.NewWriter(&buf)
	bodies := []struct{ contentType, body string }{{"text/plain; charset=utf-8", msg.Text}}
	if msg.HTML != "" {
		bodies = append(bodies, struct{ contentType, body string }{"text/html; charset=utf-8", msg.HTML})
	}
	for _, b := range bodies {
		part, err := alt.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {b.contentType},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return nil, "", err
		}
		if err := writeBase64(part, []byte(b.body)); err != nil {
			return nil, "", err
		}
	}
	if err := alt.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), alt.Boundary(), nil
}

// writeBase64 writes data base64-encoded in 76-character lines as RFC 2045 requires.
func writeBase64(w interface{ Write([]byte) (int, error) }, data []byte) error {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		if _, err := w.Write([]byte(encoded[:76] + "\r\n")); err != nil {
			return err
		}
		encoded = encoded[76:]
	}
	_, err := w.Write([]byte(encoded + "\r\n"))
	return err
}

func domainOf(addr string) string {
	if at := strings.LastIndex(addr, "@"); at >= 0 {
		return addr[at+1:]
	}
	return "localhost"
}
