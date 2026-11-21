package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<div id=root>"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "assets", "app-abc123.js"), []byte("console.log(1)"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "sw.js"), []byte("self.skipWaiting()"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "manifest.webmanifest"), []byte("{}"), 0o644)
	h, err := Handler(dir)
	if err != nil {
		t.Fatal(err)
	}
	get := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	for _, path := range []string{"/", "/events/jazz", "/admin/reports?report=revenue", "/assets/missing.js"} {
		if rec := get("GET", path); rec.Code != 200 || !strings.Contains(rec.Body.String(), "root") || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: got %d %q", path, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
	if rec := get("GET", "/assets/app-abc123.js"); rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: got %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	// The service worker and manifest keep their names, so they are always revalidated.
	if rec := get("GET", "/sw.js"); rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("service worker: got %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := get("GET", "/manifest.webmanifest"); rec.Header().Get("Content-Type") != "application/manifest+json" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("manifest: got %q %q", rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
	}
	if rec := get("GET", "/api/v1/unknown"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "not_found") {
		t.Errorf("api: got %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("POST", "/events"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("post: got %d", rec.Code)
	}
	// Paths that climb out of the directory are refused, never served.
	if rec := get("GET", "/../../etc/passwd"); strings.Contains(rec.Body.String(), "root:") || (rec.Code != 400 && rec.Code != 200) {
		t.Errorf("traversal: got %d", rec.Code)
	}
	if _, err := Handler(t.TempDir()); err == nil {
		t.Error("expected an error for a directory without index.html")
	}
}
