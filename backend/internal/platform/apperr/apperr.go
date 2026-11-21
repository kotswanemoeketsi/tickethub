// Package apperr defines typed application errors that map cleanly onto HTTP responses.
//
// Services return *Error values for every failure a client can act on. Anything else is
// treated as an internal error: it is logged and the client receives a generic message.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is an error whose code and message are safe to show to API clients.
type Error struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// New builds an Error with an explicit status and machine-readable code.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func BadRequest(message string) *Error {
	return New(http.StatusBadRequest, "bad_request", message)
}

func Unauthorized(message string) *Error {
	return New(http.StatusUnauthorized, "unauthorized", message)
}

func Forbidden(message string) *Error {
	return New(http.StatusForbidden, "forbidden", message)
}

// NotFound reports a missing resource. It is also used when a caller may not know that a
// resource exists (for example another organiser's event), so existence is not leaked.
func NotFound(resource string) *Error {
	return New(http.StatusNotFound, "not_found", resource+" not found")
}

// Conflict reports a request that clashes with the current state (sold out, wrong status).
func Conflict(code, message string) *Error {
	return New(http.StatusConflict, code, message)
}

func TooManyRequests(message string) *Error {
	return New(http.StatusTooManyRequests, "rate_limited", message)
}

// Validation reports field-level input errors.
func Validation(fields map[string]string) *Error {
	return &Error{
		Status:  http.StatusUnprocessableEntity,
		Code:    "validation_failed",
		Message: "Some fields are invalid.",
		Fields:  fields,
	}
}

// FieldError is a shortcut for a validation error on a single field.
func FieldError(field, message string) *Error {
	return Validation(map[string]string{field: message})
}

// As extracts an *Error from an error chain.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// IsCode reports whether err is an *Error with the given code.
func IsCode(err error, code string) bool {
	e, ok := As(err)
	return ok && e.Code == code
}
