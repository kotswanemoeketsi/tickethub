package sales

import (
	"io"
	"net/http"
	"strconv"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
)

// Handler exposes the sales endpoints.
type Handler struct{ svc *Service }

// NewHandler returns the sales HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func writePDF(w http.ResponseWriter, data []byte, filename string) error {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	_, err := w.Write(data)
	return err
}

// ---- Public & customer ----------------------------------------------------------------

func (h *Handler) PaymentOptions(w http.ResponseWriter, r *http.Request) error {
	return httpx.OK(w, h.svc.Options())
}

func (h *Handler) Reserve(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	var in ReserveInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	o, err := h.svc.Reserve(r.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, o)
}

func (h *Handler) MyOrders(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	page := httpx.PageFrom(r)
	list, total, err := h.svc.MyOrders(r.Context(), p, page)
	if err != nil {
		return err
	}
	return httpx.Paginated(w, list, total, page)
}

func (h *Handler) MyOrder(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	o, err := h.svc.CustomerOrder(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in CheckoutInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.Checkout(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}

func (h *Handler) CancelMyOrder(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	o, err := h.svc.CancelOrder(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

func (h *Handler) VerifyPayment(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	o, err := h.svc.VerifyOrderPayment(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

func (h *Handler) RequestRefund(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in RefundRequestInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	ref, err := h.svc.RequestRefund(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, ref)
}

func (h *Handler) MyTickets(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	list, err := h.svc.MyTickets(r.Context(), p, httpx.QueryString(r, "when") == "past")
	if err != nil {
		return err
	}
	return httpx.List(w, list)
}

// OrderPDF downloads all valid tickets of an order (customer or staff).
func (h *Handler) OrderPDF(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	data, name, err := h.svc.OrderPDF(r.Context(), p, id, httpx.QueryInt64(r, "ticket_id"))
	if err != nil {
		return err
	}
	return writePDF(w, data, name)
}

func (h *Handler) UpdateAttendee(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in AttendeeInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	t, err := h.svc.UpdateAttendee(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, t)
}

// Webhook receives gateway notifications. The body is read raw because the signature is
// computed over the exact bytes sent.
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		return apperr.BadRequest("Could not read webhook body.")
	}
	if err := h.svc.HandleWebhook(r.Context(), r.PathValue("provider"), r.Header, body); err != nil {
		return err
	}
	return httpx.OK(w, map[string]bool{"received": true})
}

// ---- Administration -------------------------------------------------------------------

func (h *Handler) ListOrders(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	page := httpx.PageFrom(r)
	list, total, err := h.svc.ListOrders(r.Context(), p, OrderFilter{
		EventID: httpx.QueryInt64(r, "event_id"), OrganiserID: httpx.QueryInt64(r, "organiser_id"),
		Status: httpx.QueryString(r, "status"), Source: httpx.QueryString(r, "source"), Query: httpx.QueryString(r, "q"),
	}, page)
	if err != nil {
		return err
	}
	return httpx.Paginated(w, list, total, page)
}

func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	o, err := h.svc.AdminOrder(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

func (h *Handler) ResendTickets(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.ResendTickets(r.Context(), p, id); err != nil {
		return err
	}
	return httpx.OK(w, map[string]string{"message": "The tickets will be emailed again shortly."})
}

func (h *Handler) CancelOrder(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	o, err := h.svc.AdminCancelOrder(r.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

func (h *Handler) CreateRefund(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in RefundRequestInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	ref, err := h.svc.CreateRefund(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, ref)
}

func (h *Handler) IssueComplimentary(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in ComplimentaryInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	o, err := h.svc.IssueComplimentary(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, o)
}

func (h *Handler) EventAttendees(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	page := httpx.PageFrom(r)
	list, total, err := h.svc.EventAttendees(r.Context(), p, id, AttendeeFilter{
		Query: httpx.QueryString(r, "q"), CheckinState: httpx.QueryString(r, "checkin_state"), TicketTypeID: httpx.QueryInt64(r, "ticket_type_id"),
	}, page)
	if err != nil {
		return err
	}
	return httpx.Paginated(w, list, total, page)
}

func (h *Handler) ListPayments(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	page := httpx.PageFrom(r)
	list, total, err := h.svc.ListPayments(r.Context(), p, PaymentFilter{
		Status: httpx.QueryString(r, "status"), Method: httpx.QueryString(r, "method"), Query: httpx.QueryString(r, "q"),
		OrganiserID: httpx.QueryInt64(r, "organiser_id"),
	}, page)
	if err != nil {
		return err
	}
	return httpx.Paginated(w, list, total, page)
}

func (h *Handler) ConfirmEFT(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in ConfirmEFTInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	pay, err := h.svc.ConfirmEFT(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, pay)
}

func (h *Handler) ListRefunds(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	page := httpx.PageFrom(r)
	list, total, err := h.svc.ListRefunds(r.Context(), p, RefundFilter{
		Status: httpx.QueryString(r, "status"), Query: httpx.QueryString(r, "q"), EventID: httpx.QueryInt64(r, "event_id"),
		OrganiserID: httpx.QueryInt64(r, "organiser_id"),
	}, page)
	if err != nil {
		return err
	}
	return httpx.Paginated(w, list, total, page)
}

// ReviewRefund returns a handler approving (true) or rejecting (false) a refund request.
func (h *Handler) ReviewRefund(approve bool) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, _ := auth.MustPrincipal(r)
		id, err := httpx.PathID(r, "id")
		if err != nil {
			return err
		}
		var in struct {
			Note string `json:"note"`
		}
		if r.ContentLength > 0 {
			if err := httpx.DecodeJSON(w, r, &in); err != nil {
				return err
			}
		}
		ref, err := h.svc.ReviewRefund(r.Context(), p, id, approve, in.Note)
		if err != nil {
			return err
		}
		return httpx.OK(w, ref)
	}
}

func (h *Handler) ProcessRefund(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in ProcessRefundInput
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	ref, err := h.svc.ProcessRefund(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, ref)
}
