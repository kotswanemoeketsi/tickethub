package httpx

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
)

// envelope is the shape of every JSON response: {"data": ...} or {"error": ...}.
type envelope struct {
	Data  any           `json:"data,omitempty"`
	Meta  *PageMeta     `json:"meta,omitempty"`
	Error *apperr.Error `json:"error,omitempty"`
}

// PageMeta describes a page of a paginated list.
type PageMeta struct {
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Total   int64 `json:"total"`
}

// JSON writes v as {"data": v} with the given status.
func JSON(w http.ResponseWriter, status int, v any) error {
	return writeJSON(w, status, envelope{Data: v})
}

// OK writes a 200 response.
func OK(w http.ResponseWriter, v any) error { return JSON(w, http.StatusOK, v) }

// Created writes a 201 response.
func Created(w http.ResponseWriter, v any) error { return JSON(w, http.StatusCreated, v) }

// NoContent writes a 204 response.
func NoContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Paginated writes a list with pagination metadata. A nil slice is written as [].
func Paginated[T any](w http.ResponseWriter, items []T, total int64, page Page) error {
	if items == nil {
		items = []T{}
	}
	return writeJSON(w, http.StatusOK, envelope{
		Data: items,
		Meta: &PageMeta{Page: page.Number, PerPage: page.Size, Total: total},
	})
}

// List writes a non-paginated list, normalising nil to [].
func List[T any](w http.ResponseWriter, items []T) error {
	if items == nil {
		items = []T{}
	}
	return OK(w, items)
}

// LargeList writes a 200 list response, gzip-compressed when the client accepts it. Use it
// for big, rarely changing lists; reverse proxies usually compress the rest.
func LargeList[T any](w http.ResponseWriter, r *http.Request, items []T) error {
	if items == nil {
		items = []T{}
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsGzip(r) {
		return OK(w, items)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Encoding", "gzip")
	w.WriteHeader(http.StatusOK)
	zw := gzip.NewWriter(w)
	if err := json.NewEncoder(zw).Encode(envelope{Data: items}); err != nil {
		zw.Close()
		return err
	}
	return zw.Close()
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") && strings.ReplaceAll(strings.TrimSpace(params), " ", "") != "q=0" {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, body envelope) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}

// WriteError renders err. Application errors are returned as-is; anything else is logged
// with the request ID and reported to the client as a generic internal error.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		err = apperr.New(http.StatusRequestEntityTooLarge, "payload_too_large", "The request body is too large.")
	}

	appErr, ok := apperr.As(err)
	if !ok {
		slog.ErrorContext(r.Context(), "unhandled error",
			"error", err, "method", r.Method, "path", r.URL.Path, "request_id", RequestIDFrom(r.Context()))
		appErr = apperr.New(http.StatusInternalServerError, "internal_error", "Something went wrong. Please try again.")
	}
	_ = writeJSON(w, appErr.Status, envelope{Error: appErr})
}
