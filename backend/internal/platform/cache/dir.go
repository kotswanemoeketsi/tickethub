package cache

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Dir is a Cache kept as files in a directory. Unlike Memory it survives restarts and is
// shared by every process on the machine (for example `gotaticket seed` and `gotaticket
// serve`), which is what the sandbox payment gateway needs when Redis is not configured.
// Increments are atomic within one process only, so Dir is not meant for rate limiting.
type Dir struct {
	path string
	mu   sync.Mutex
}

// NewDir returns a cache stored under path, creating the directory if needed.
func NewDir(path string) (*Dir, error) {
	if err := os.MkdirAll(path, 0o750); err != nil {
		return nil, fmt.Errorf("cache directory: %w", err)
	}
	return &Dir{path: path}, nil
}

// Each file holds the expiry (Unix nanoseconds, 0 for none) on the first line, then the value.
func (d *Dir) file(key string) string { return filepath.Join(d.path, hex.EncodeToString([]byte(key))) }

func (d *Dir) Get(_ context.Context, key string) ([]byte, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.getLocked(key)
}

func (d *Dir) getLocked(key string) ([]byte, bool, error) {
	raw, err := os.ReadFile(d.file(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	for i, b := range raw {
		if b != '\n' {
			continue
		}
		expires, err := strconv.ParseInt(string(raw[:i]), 10, 64)
		if err != nil {
			break
		}
		if expires != 0 && time.Now().UnixNano() > expires {
			_ = os.Remove(d.file(key))
			return nil, false, nil
		}
		return raw[i+1:], true, nil
	}
	// A damaged file is treated as a miss.
	_ = os.Remove(d.file(key))
	return nil, false, nil
}

func (d *Dir) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.setLocked(key, value, ttl)
}

func (d *Dir) setLocked(key string, value []byte, ttl time.Duration) error {
	var expires int64
	if ttl > 0 {
		expires = time.Now().Add(ttl).UnixNano()
	}
	content := append([]byte(strconv.FormatInt(expires, 10)+"\n"), value...)
	// Write to a temporary file and rename it, so readers never see a half-written value.
	tmp, err := os.CreateTemp(d.path, ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), d.file(key))
}

func (d *Dir) Delete(_ context.Context, keys ...string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, k := range keys {
		if err := os.Remove(d.file(k)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (d *Dir) Incr(_ context.Context, key string, ttl time.Duration) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw, ok, err := d.getLocked(key)
	if err != nil {
		return 0, err
	}
	var n int64
	if ok {
		n, _ = strconv.ParseInt(string(raw), 10, 64)
	}
	n++
	if err := d.setLocked(key, []byte(strconv.FormatInt(n, 10)), ttl); err != nil {
		return 0, err
	}
	return n, nil
}

func (d *Dir) Close() error { return nil }
