package ai

import (
	"net/http"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
)

// Handler serves the AI endpoints.
type Handler struct{ svc *Service }

// NewHandler returns the AI handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Features tells the web app which AI features to offer.
func (h *Handler) Features(w http.ResponseWriter, _ *http.Request) error {
	on := h.svc.Enabled()
	return httpx.OK(w, map[string]bool{"ai_search": on, "ai_event_writer": on, "ai_event_review": on})
}

// InterpretSearch turns a natural-language search into filters.
func (h *Handler) InterpretSearch(w http.ResponseWriter, r *http.Request) error {
	client := "ip:" + httpx.ClientMetaFrom(r.Context()).IP
	if p := auth.FromContext(r.Context()); p != nil {
		client = "user:" + itoa(p.UserID)
	}
	filters, err := h.svc.InterpretSearch(r.Context(), client, httpx.QueryString(r, "q"))
	if err != nil {
		return err
	}
	return httpx.OK(w, filters)
}

// DraftEvent writes a first version of an event listing.
func (h *Handler) DraftEvent(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.MustPrincipal(r)
	if err != nil {
		return err
	}
	var in DraftInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	draft, err := h.svc.DraftEvent(r.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, draft)
}

// ReviewEvent runs the pre-check of an event.
func (h *Handler) ReviewEvent(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.MustPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	review, err := h.svc.ReviewEvent(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, review)
}

// LatestReview returns the stored pre-check of an event.
func (h *Handler) LatestReview(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.MustPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	review, err := h.svc.LatestReview(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, review)
}
