package organisers

import (
	"net/http"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
)

// Handler exposes organiser endpoints.
type Handler struct{ svc *Service }

// NewHandler returns the organiser HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Apply(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.MustPrincipal(r)
	if err != nil {
		return err
	}
	var in Input
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	o, err := h.svc.Apply(r.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, o)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	page := httpx.PageFrom(r)
	list, total, err := h.svc.List(r.Context(), Filter{Status: httpx.QueryString(r, "status"), Query: httpx.QueryString(r, "q")}, page)
	if err != nil {
		return err
	}
	return httpx.Paginated(w, list, total, page)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	o, err := h.svc.Get(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	var in Input
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	o, err := h.svc.Create(r.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, o)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in Input
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	o, err := h.svc.Update(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

// SetStatus returns a handler moving the organiser to status.
func (h *Handler) SetStatus(status string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, _ := auth.MustPrincipal(r)
		id, err := httpx.PathID(r, "id")
		if err != nil {
			return err
		}
		o, err := h.svc.SetStatus(r.Context(), p, id, status)
		if err != nil {
			return err
		}
		return httpx.OK(w, o)
	}
}

func (h *Handler) Members(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	members, err := h.svc.Members(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.List(w, members)
}

func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in MemberInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	m, err := h.svc.AddMember(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, m)
}

func (h *Handler) UpdateMember(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	userID, err := httpx.PathID(r, "userID")
	if err != nil {
		return err
	}
	var in struct {
		RoleID int64 `json:"role_id"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	m, err := h.svc.UpdateMemberRole(r.Context(), p, id, userID, in.RoleID)
	if err != nil {
		return err
	}
	return httpx.OK(w, m)
}

func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	userID, err := httpx.PathID(r, "userID")
	if err != nil {
		return err
	}
	if err := h.svc.RemoveMember(r.Context(), p, id, userID); err != nil {
		return err
	}
	return httpx.NoContent(w)
}
