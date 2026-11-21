package mailer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// File writes each message to an .eml file instead of sending it. It is the default when
// no SMTP server is configured, so the platform works end to end on a laptop; open the
// files with any mail client to see exactly what customers would receive.
type File struct {
	dir    string
	from   string
	logger *slog.Logger
}

// NewFile returns a mailer that writes messages into dir.
func NewFile(dir, from string, logger *slog.Logger) (*File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create mail directory: %w", err)
	}
	return &File{dir: dir, from: from, logger: logger}, nil
}

var unsafeFileChars = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func (f *File) Send(_ context.Context, msg Message) error {
	now := time.Now()
	body, err := Build(f.from, msg, now)
	if err != nil {
		return err
	}
	slug := strings.Trim(unsafeFileChars.ReplaceAllString(strings.ToLower(msg.Subject), "-"), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	name := fmt.Sprintf("%s-%s.eml", now.Format("20060102-150405.000"), slug)
	path := filepath.Join(f.dir, name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write email file: %w", err)
	}
	f.logger.Info("email written to disk (no SMTP configured)", "to", strings.Join(msg.To, ","), "subject", msg.Subject, "file", path)
	return nil
}
