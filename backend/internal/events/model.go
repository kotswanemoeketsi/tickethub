// Package events manages events, their ticket types and promotional codes, the approval
// workflow, and the public marketplace listing.
package events

import (
	"time"
)

// Event statuses as stored. "Completed" is derived from the end date rather than stored,
// so it can never drift from the calendar.
const (
	StatusDraft         = "draft"
	StatusPendingReview = "pending_review"
	StatusPublished     = "published"
	StatusRejected      = "rejected"
	StatusCancelled     = "cancelled"
)

// Event phases add the time dimension to published events.
const (
	PhaseUpcoming  = "upcoming"
	PhaseLive      = "live"
	PhaseCompleted = "completed"
)

// Phase returns the status shown to users: published events become upcoming, live or
// completed depending on the time.
func Phase(status string, startsAt, endsAt, now time.Time) string {
	if status != StatusPublished {
		return status
	}
	switch {
	case now.Before(startsAt):
		return PhaseUpcoming
	case now.Before(endsAt):
		return PhaseLive
	default:
		return PhaseCompleted
	}
}

// VenueSummary is the venue information embedded in events.
type VenueSummary struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AddressLine string `json:"address_line"`
	City        string `json:"city"`
	Province    string `json:"province"`
	Country     string `json:"country"`
}

// Stats summarises an event's sales and attendance.
type Stats struct {
	TicketsSold       int   `json:"tickets_sold"`
	TicketsReserved   int   `json:"tickets_reserved"`
	TicketsAvailable  int   `json:"tickets_available"`
	CheckedIn         int   `json:"checked_in"`
	NetRevenueCents   int64 `json:"net_revenue_cents"`
	OrdersPaid        int   `json:"orders_paid"`
	TicketTypesActive int   `json:"ticket_types"`
}

// Event is the administrative view of an event.
type Event struct {
	ID                 int64         `json:"id"`
	OrganiserID        int64         `json:"organiser_id"`
	OrganiserName      string        `json:"organiser_name"`
	CategoryID         *int64        `json:"category_id"`
	CategoryName       string        `json:"category_name"`
	Venue              *VenueSummary `json:"venue"`
	Title              string        `json:"title"`
	Slug               string        `json:"slug"`
	Summary            string        `json:"summary"`
	Description        string        `json:"description"`
	ImageURL           string        `json:"image_url"`
	StartsAt           time.Time     `json:"starts_at"`
	EndsAt             time.Time     `json:"ends_at"`
	StartsAtLocal      string        `json:"starts_at_local"`
	EndsAtLocal        string        `json:"ends_at_local"`
	Timezone           string        `json:"timezone"`
	Currency           string        `json:"currency"`
	Capacity           int           `json:"capacity"`
	Status             string        `json:"status"`
	Phase              string        `json:"phase"`
	AllowReentry       bool          `json:"allow_reentry"`
	RefundsAllowed     bool          `json:"refunds_allowed"`
	RefundCutoffHours  int           `json:"refund_cutoff_hours"`
	ReviewNotes        string        `json:"review_notes"`
	SubmittedAt        *time.Time    `json:"submitted_at"`
	ReviewedAt         *time.Time    `json:"reviewed_at"`
	PublishedAt        *time.Time    `json:"published_at"`
	CancelledAt        *time.Time    `json:"cancelled_at"`
	CancellationReason string        `json:"cancellation_reason"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
	Stats              Stats         `json:"stats"`
}

// Ticket type categories, used for cross-event "sales by ticket category" reporting.
var TicketCategories = []string{"general", "vip", "early_bird", "student", "group", "complimentary", "other"}

// Sale statuses for ticket types.
const (
	SaleOnSale    = "on_sale"
	SaleScheduled = "scheduled"
	SaleEnded     = "ended"
	SaleSoldOut   = "sold_out"
	SaleHidden    = "hidden"
)

// TicketType is a priced allocation of tickets for an event (VIP, General, Early Bird...).
type TicketType struct {
	ID                int64      `json:"id"`
	EventID           int64      `json:"event_id"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	Category          string     `json:"category"`
	PriceCents        int64      `json:"price_cents"`
	QuantityTotal     int        `json:"quantity_total"`
	QuantitySold      int        `json:"quantity_sold"`
	QuantityReserved  int        `json:"quantity_reserved"`
	QuantityAvailable int        `json:"quantity_available"`
	MinPerOrder       int        `json:"min_per_order"`
	MaxPerOrder       int        `json:"max_per_order"`
	SalesStartAt      *time.Time `json:"sales_start_at"`
	SalesEndAt        *time.Time `json:"sales_end_at"`
	SalesStartAtLocal string     `json:"sales_start_at_local"`
	SalesEndAtLocal   string     `json:"sales_end_at_local"`
	IsHidden          bool       `json:"is_hidden"`
	SortOrder         int        `json:"sort_order"`
	SaleStatus        string     `json:"sale_status"`
}

// SaleStatus derives whether a ticket type can be bought right now.
func SaleStatus(tt TicketType, eventRemaining int, eventEndsAt, now time.Time) string {
	switch {
	case tt.IsHidden:
		return SaleHidden
	case tt.SalesStartAt != nil && now.Before(*tt.SalesStartAt):
		return SaleScheduled
	case (tt.SalesEndAt != nil && !now.Before(*tt.SalesEndAt)) || !now.Before(eventEndsAt):
		return SaleEnded
	case tt.QuantityTotal-tt.QuantitySold-tt.QuantityReserved <= 0 || eventRemaining <= 0:
		return SaleSoldOut
	default:
		return SaleOnSale
	}
}

// Promotion is a discount code for an event.
type Promotion struct {
	ID            int64      `json:"id"`
	EventID       int64      `json:"event_id"`
	Code          string     `json:"code"`
	Description   string     `json:"description"`
	DiscountType  string     `json:"discount_type"`
	DiscountValue int64      `json:"discount_value"`
	MaxUses       *int       `json:"max_uses"`
	TimesUsed     int        `json:"times_used"`
	StartsAt      *time.Time `json:"starts_at"`
	EndsAt        *time.Time `json:"ends_at"`
	StartsAtLocal string     `json:"starts_at_local"`
	EndsAtLocal   string     `json:"ends_at_local"`
	IsActive      bool       `json:"is_active"`
	TicketTypeIDs []int64    `json:"ticket_type_ids"`
	CreatedAt     time.Time  `json:"created_at"`
}

// localInput formats t as a wall-clock value for <input type="datetime-local"> in tz.
func localInput(t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format("2006-01-02T15:04")
}

func localInputPtr(t *time.Time, tz string) string {
	if t == nil {
		return ""
	}
	return localInput(*t, tz)
}
