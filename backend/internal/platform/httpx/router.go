// Package httpx contains the HTTP plumbing shared by every module: an error-returning
// handler type, a small router built on net/http's pattern matching, request decoding,
// JSON responses and common middleware.
package httpx

import (
	"net/http"
	"strings"
)

// HandlerFunc is an HTTP handler that returns an error instead of writing it. Errors are
// rendered centrally by WriteError so every endpoint reports failures the same way.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// ServeHTTP implements http.Handler.
func (h HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		WriteError(w, r, err)
	}
}

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first one listed runs first.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Router registers routes on an http.ServeMux with shared prefixes and middleware.
type Router struct {
	mux    *http.ServeMux
	prefix string
	mws    []Middleware
}

// NewRouter returns an empty router.
func NewRouter() *Router { return &Router{mux: http.NewServeMux()} }

// Group returns a router that shares the mux but adds a path prefix and middleware.
func (r *Router) Group(prefix string, mws ...Middleware) *Router {
	return &Router{
		mux:    r.mux,
		prefix: r.prefix + prefix,
		mws:    append(append([]Middleware{}, r.mws...), mws...),
	}
}

// Handle registers h for method and path (net/http pattern syntax, e.g. "/events/{id}").
func (r *Router) Handle(method, path string, h http.Handler, mws ...Middleware) {
	all := append(append([]Middleware{}, r.mws...), mws...)
	full := r.prefix + path
	if full != "/" {
		full = strings.TrimSuffix(full, "/")
	}
	r.mux.Handle(method+" "+full, Chain(h, all...))
}

func (r *Router) GET(path string, h HandlerFunc, mws ...Middleware) {
	r.Handle(http.MethodGet, path, h, mws...)
}

func (r *Router) POST(path string, h HandlerFunc, mws ...Middleware) {
	r.Handle(http.MethodPost, path, h, mws...)
}

func (r *Router) PUT(path string, h HandlerFunc, mws ...Middleware) {
	r.Handle(http.MethodPut, path, h, mws...)
}

func (r *Router) DELETE(path string, h HandlerFunc, mws ...Middleware) {
	r.Handle(http.MethodDelete, path, h, mws...)
}

// Mount registers a plain handler for every method under a path prefix (e.g. static files).
func (r *Router) Mount(pathPrefix string, h http.Handler) {
	r.mux.Handle(r.prefix+pathPrefix, Chain(h, r.mws...))
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.mux.ServeHTTP(w, req) }
