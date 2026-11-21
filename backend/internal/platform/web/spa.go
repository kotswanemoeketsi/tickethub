// Package web serves the built React app, so a single process (and a single container)
// can run the whole platform: the API under /api and the website everywhere else.
package web

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
)

func init() {
	// Not every system's MIME table knows the web app manifest.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// Handler serves files from dir. Paths that are not files are client-side routes, so
// they get index.html; unknown /api paths still get a JSON 404 rather than the app.
func Handler(dir string) (http.Handler, error) {
	if _, err := os.Stat(path.Join(dir, "index.html")); err != nil {
		return nil, errors.New("web directory has no index.html: " + dir)
	}
	root := os.DirFS(dir)
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			httpx.WriteError(w, r, apperr.NotFound("Endpoint"))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(root, name); name == "" || err != nil || info.IsDir() {
			// A client-side route: always fetch the latest app shell.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, root, "index.html")
			return
		}
		if strings.HasPrefix(name, "assets/") {
			// Built assets carry a content hash in their names, so they never change.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// Everything else keeps its name between releases: the service worker, the app
			// manifest and the icons. Browsers must check for a newer copy every time, or an
			// installed app could keep running an old release.
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	}), nil
}
