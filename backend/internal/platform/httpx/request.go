package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
)

// MaxJSONBodyBytes bounds JSON request bodies.
const MaxJSONBodyBytes = 1 << 20

// DecodeJSON decodes the request body into dst, rejecting malformed or oversized bodies.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &maxBytesErr):
			return err
		case errors.Is(err, io.EOF):
			return apperr.BadRequest("Request body must not be empty.")
		case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
			return apperr.BadRequest("Request body contains malformed JSON.")
		case errors.As(err, &typeErr):
			field := typeErr.Field
			if field == "" {
				field = "body"
			}
			return apperr.FieldError(field, "has the wrong type")
		default:
			return apperr.BadRequest("Request body could not be decoded.")
		}
	}
	return nil
}

// PathID parses a positive integer path parameter such as {id}.
func PathID(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.NotFound("Resource")
	}
	return id, nil
}

// QueryString returns a trimmed query parameter.
func QueryString(r *http.Request, name string) string {
	return strings.TrimSpace(r.URL.Query().Get(name))
}

// QueryInt64 returns an integer query parameter, or 0 when absent or invalid.
func QueryInt64(r *http.Request, name string) int64 {
	v, err := strconv.ParseInt(QueryString(r, name), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// QueryDate parses an optional YYYY-MM-DD query parameter in loc. When endOfDay is true the
// returned instant is the start of the following day, which suits exclusive upper bounds.
func QueryDate(r *http.Request, name string, loc *time.Location, endOfDay bool) (*time.Time, error) {
	raw := QueryString(r, name)
	if raw == "" {
		return nil, nil
	}
	t, err := time.ParseInLocation("2006-01-02", raw, loc)
	if err != nil {
		return nil, apperr.FieldError(name, "must be a date in YYYY-MM-DD format")
	}
	if endOfDay {
		t = t.AddDate(0, 0, 1)
	}
	utc := t.UTC()
	return &utc, nil
}

// Page is a requested page of a list.
type Page struct {
	Number int
	Size   int
}

// Offset returns the SQL OFFSET for the page.
func (p Page) Offset() int { return (p.Number - 1) * p.Size }

// PageFrom reads ?page= and ?per_page= with sensible bounds.
func PageFrom(r *http.Request) Page {
	page := int(QueryInt64(r, "page"))
	if page < 1 {
		page = 1
	}
	size := int(QueryInt64(r, "per_page"))
	switch {
	case size < 1:
		size = 20
	case size > 100:
		size = 100
	}
	return Page{Number: page, Size: size}
}
