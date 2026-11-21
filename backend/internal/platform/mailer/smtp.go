package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"time"
)

// TLSMode selects how the SMTP connection is secured.
type TLSMode string

const (
	TLSNone     TLSMode = "none"     // plain connection (local catch-all servers such as Mailpit)
	TLSStartTLS TLSMode = "starttls" // upgrade with STARTTLS, usually port 587
	TLSImplicit TLSMode = "tls"      // TLS from the first byte, usually port 465
)

// SMTPConfig configures the SMTP mailer.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	TLS      TLSMode
	From     string
	Timeout  time.Duration
}

// SMTP delivers mail through an SMTP relay.
type SMTP struct {
	cfg SMTPConfig
}

// NewSMTP returns an SMTP mailer.
func NewSMTP(cfg SMTPConfig) *SMTP {
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	return &SMTP{cfg: cfg}
}

func (s *SMTP) Send(ctx context.Context, msg Message) error {
	body, err := Build(s.cfg.From, msg, time.Now())
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(s.cfg.From)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Timeout: s.cfg.Timeout}
	var conn net.Conn
	if s.cfg.TLS == TLSImplicit {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	deadline := time.Now().Add(s.cfg.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()

	if s.cfg.TLS == TLSStartTLS {
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, to := range msg.To {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", to, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return client.Quit()
}
