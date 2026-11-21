// Package storage stores uploaded files. The local implementation writes to disk and is
// served by the API; the interface makes it straightforward to move to object storage.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/random"
)

// ErrUnsupportedType is returned for files that are not an accepted image type.
var ErrUnsupportedType = errors.New("unsupported file type")

// imageExtensions maps sniffed content types to file extensions.
var imageExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// Local stores files beneath a directory and exposes them under a public URL prefix.
type Local struct {
	dir       string
	urlPrefix string
}

// NewLocal creates the storage directory if needed.
func NewLocal(dir, urlPrefix string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create upload directory: %w", err)
	}
	return &Local{dir: dir, urlPrefix: strings.TrimSuffix(urlPrefix, "/")}, nil
}

// SaveImage validates that r contains an image (by content, not by file name) and stores it
// under a random name. It returns the public URL.
func (l *Local) SaveImage(_ context.Context, r io.Reader, maxBytes int64) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("file is larger than %d bytes", maxBytes)
	}
	contentType := http.DetectContentType(data)
	ext, ok := imageExtensions[contentType]
	if !ok {
		return "", ErrUnsupportedType
	}
	name := random.Hex(16) + ext
	if err := writeAtomic(filepath.Join(l.dir, name), data); err != nil {
		return "", err
	}
	return l.urlPrefix + "/" + name, nil
}

// Handler serves stored files. Directory listings are disabled.
func (l *Local) Handler() http.Handler {
	fs := http.FileServer(noDirFS{http.Dir(l.dir)})
	return http.StripPrefix(l.urlPrefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		fs.ServeHTTP(w, r)
	}))
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// noDirFS hides directories so http.FileServer cannot list them.
type noDirFS struct{ fs http.FileSystem }

func (n noDirFS) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if stat.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}
