package events

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/storage"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Handler exposes event endpoints.
type Handler struct {
	svc     *Service
	storage *storage.Local
	loc     *time.Location
}

// NewHandler returns the events HTTP handler.
func NewHandler(svc *Service, store *storage.Local) *Handler {
	loc, err := time.LoadLocation(svc.cfg.DefaultTimezone)
	if err != nil {
		loc = time.UTC
	}
	return &Handler{svc: svc, storage: store, loc: loc}
}

// ---- Public ---------------------------------------------------------------------------

func (h *Handler) ListPublic(w http.ResponseWriter, r *http.Request) error {
	from, err := httpx.QueryDate(r, "from", h.loc, false)
	if err != nil {
		return err
	}
	to, err := httpx.QueryDate(r, "to", h.loc, true)
	if err != nil {
		return err
	}
	page := httpx.PageFrom(r)
	items, total, err := h.svc.ListPublic(r.Context(), PublicFilter{
		Query: httpx.QueryString(r, "q"), Category: httpx.QueryString(r, "category"), City: httpx.QueryString(r, "city"),
		Province: httpx.QueryString(r, "province"), MaxPriceCents: max(httpx.QueryInt64(r, "max_price"), 0) * 100, From: from, To: to,
		Sort: httpx.QueryString(r, "sort"),
	}, page)
	if err != nil {
		return err
	}
	return httpx.Paginated(w, items, total, page)
}

func (h *Handler) PublicLocations(w http.ResponseWriter, r *http.Request) error {
	locations, err := h.svc.PublicLocations(r.Context())
	if err != nil {
		return err
	}
	return httpx.List(w, locations)
}

// PublicPlaces lists every city and town in a province. The list only changes when the
// application is upgraded, so browsers may keep it for a day.
func (h *Handler) PublicPlaces(w http.ResponseWriter, r *http.Request) error {
	province := httpx.QueryString(r, "province")
	places, ok := PlacesIn(province)
	if !ok {
		return apperr.FieldError("province", "choose one of the nine provinces")
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	return httpx.LargeList(w, r, places)
}

// Nearby radius limits, in kilometres.
const (
	defaultNearbyRadiusKM = 200
	maxNearbyRadiusKM     = 500
	defaultNearbyLimit    = 12
	maxNearbyLimit        = 24
)

func (h *Handler) PublicNearby(w http.ResponseWriter, r *http.Request) error {
	v := validate.New()
	coordinate := func(name string, limit float64) float64 {
		raw := httpx.QueryString(r, name)
		f, err := strconv.ParseFloat(raw, 64)
		switch {
		case raw == "":
			v.Add(name, "is required")
		case err != nil || math.IsNaN(f) || f < -limit || f > limit:
			v.Add(name, fmt.Sprintf("must be a number between -%.0f and %.0f", limit, limit))
		}
		return f
	}
	f := NearbyFilter{Latitude: coordinate("lat", 90), Longitude: coordinate("lng", 180), RadiusKM: defaultNearbyRadiusKM, Limit: defaultNearbyLimit}
	if raw := httpx.QueryString(r, "radius_km"); raw != "" {
		radius, err := strconv.ParseFloat(raw, 64)
		if err != nil || radius < 1 || radius > maxNearbyRadiusKM {
			v.Add("radius_km", fmt.Sprintf("must be between 1 and %d", maxNearbyRadiusKM))
		}
		f.RadiusKM = radius
	}
	if n := httpx.QueryInt64(r, "limit"); n > 0 {
		f.Limit = int(min(n, maxNearbyLimit))
	}
	if err := v.Err(); err != nil {
		return err
	}
	items, err := h.svc.NearbyPublic(r.Context(), f)
	if err != nil {
		return err
	}
	return httpx.List(w, items)
}

func (h *Handler) GetPublic(w http.ResponseWriter, r *http.Request) error {
	e, err := h.svc.GetPublic(r.Context(), r.PathValue("slug"))
	if err != nil {
		return err
	}
	return httpx.OK(w, e)
}

// ---- Administration -------------------------------------------------------------------

func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	page := httpx.PageFrom(r)
	list, total, err := h.svc.List(r.Context(), p, Filter{
		Status: httpx.QueryString(r, "status"), OrganiserID: httpx.QueryInt64(r, "organiser_id"),
		CategoryID: httpx.QueryInt64(r, "category_id"), Query: httpx.QueryString(r, "q"),
	}, page)
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
	e, err := h.svc.Authorize(r.Context(), p, id, rbac.EventsView)
	if err != nil {
		return err
	}
	return httpx.OK(w, e)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	var in Input
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	e, err := h.svc.Create(r.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, e)
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
	e, err := h.svc.Update(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, e)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.Delete(r.Context(), p, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

type notesInput struct {
	Notes  string `json:"notes"`
	Reason string `json:"reason"`
}

// Lifecycle returns a handler for submit, withdraw, approve, reject and cancel.
func (h *Handler) Lifecycle(action string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, _ := auth.MustPrincipal(r)
		id, err := httpx.PathID(r, "id")
		if err != nil {
			return err
		}
		var in notesInput
		if r.ContentLength > 0 {
			if err := httpx.DecodeJSON(w, r, &in); err != nil {
				return err
			}
		}
		var e Event
		switch action {
		case "submit":
			e, err = h.svc.Submit(r.Context(), p, id)
		case "withdraw":
			e, err = h.svc.Withdraw(r.Context(), p, id)
		case "approve":
			e, err = h.svc.Approve(r.Context(), p, id, in.Notes)
		case "reject":
			e, err = h.svc.Reject(r.Context(), p, id, in.Notes)
		case "cancel":
			e, err = h.svc.Cancel(r.Context(), p, id, in.Reason)
		default:
			return apperr.NotFound("Action")
		}
		if err != nil {
			return err
		}
		return httpx.OK(w, e)
	}
}

func (h *Handler) ListTicketTypes(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	types, err := h.svc.TicketTypes(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.List(w, types)
}

func (h *Handler) CreateTicketType(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in TicketTypeInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	t, err := h.svc.CreateTicketType(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, t)
}

func (h *Handler) UpdateTicketType(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in TicketTypeInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	t, err := h.svc.UpdateTicketType(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, t)
}

func (h *Handler) DeleteTicketType(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.DeleteTicketType(r.Context(), p, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (h *Handler) ListPromotions(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	list, err := h.svc.Promotions(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.List(w, list)
}

func (h *Handler) CreatePromotion(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in PromotionInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	promo, err := h.svc.CreatePromotion(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, promo)
}

func (h *Handler) UpdatePromotion(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in PromotionInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	promo, err := h.svc.UpdatePromotion(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, promo)
}

func (h *Handler) DeletePromotion(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.DeletePromotion(r.Context(), p, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

const maxImageBytes = 5 << 20

// UploadImage stores an event image sent as multipart field "file".
func (h *Handler) UploadImage(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxImageBytes+64<<10)
	file, _, err := r.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return apperr.FieldError("file", "must be 5 MB or smaller")
		}
		return apperr.FieldError("file", "is required")
	}
	defer file.Close()
	url, err := h.storage.SaveImage(r.Context(), file, maxImageBytes)
	if errors.Is(err, storage.ErrUnsupportedType) {
		return apperr.FieldError("file", "must be a JPEG, PNG, WebP or GIF image")
	}
	if err != nil {
		return apperr.FieldError("file", err.Error())
	}
	return httpx.Created(w, map[string]string{"url": url})
}
