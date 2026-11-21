// Package catalog manages reference data used by events: categories and venues.
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/geo"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/slug"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/validate"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Category classifies events (Music, Business, Sport...).
type Category struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	EventCount  int    `json:"event_count"`
}

// Venue is a place where events happen. A venue without an organiser is shared platform-wide.
type Venue struct {
	ID          int64     `json:"id"`
	OrganiserID *int64    `json:"organiser_id"`
	Name        string    `json:"name"`
	AddressLine string    `json:"address_line"`
	City        string    `json:"city"`
	Province    string    `json:"province"`
	Country     string    `json:"country"`
	PostalCode  string    `json:"postal_code"`
	Latitude    *float64  `json:"latitude"`
	Longitude   *float64  `json:"longitude"`
	Capacity    *int      `json:"capacity"`
	CreatedAt   time.Time `json:"created_at"`
}

// Handler serves category and venue endpoints. The logic is small enough that the handler
// talks to the database directly.
type Handler struct {
	db  *database.DB
	now clock.Func
}

// NewHandler returns the catalogue handler.
func NewHandler(db *database.DB, now clock.Func) *Handler { return &Handler{db: db, now: now} }

// ---- Categories -----------------------------------------------------------------------

// ListCategories is public: buyers filter the marketplace by category.
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.db.QueryContext(r.Context(), `SELECT c.id, c.name, c.slug, c.description,
		(SELECT COUNT(*) FROM events e WHERE e.category_id = c.id AND e.status = 'published' AND e.ends_at > ?)
		FROM event_categories c ORDER BY c.name`, h.now())
	if err != nil {
		return err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.Description, &c.EventCount); err != nil {
			return err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return httpx.List(w, out)
}

type categoryInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (in *categoryInput) validate() error {
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	v := validate.New()
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 100)
	v.MaxLen("description", in.Description, 255)
	return v.Err()
}

func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) error {
	var in categoryInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	var c Category
	err := h.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		now := h.now()
		res, err := tx.ExecContext(r.Context(), `INSERT INTO event_categories (name, slug, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			in.Name, slug.Make(in.Name), in.Description, now, now)
		if database.IsDuplicateKey(err) {
			return apperr.FieldError("name", "a category with this name already exists")
		}
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		c = Category{ID: id, Name: in.Name, Slug: slug.Make(in.Name), Description: in.Description}
		return audit.Record(r.Context(), tx, now, audit.Entry{Action: "category.created", EntityType: "category", EntityID: id, Summary: "Category " + in.Name + " created"})
	})
	if err != nil {
		return err
	}
	return httpx.Created(w, c)
}

func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in categoryInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	err = h.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		now := h.now()
		res, err := tx.ExecContext(r.Context(), `UPDATE event_categories SET name = ?, slug = ?, description = ?, updated_at = ? WHERE id = ?`,
			in.Name, slug.Make(in.Name), in.Description, now, id)
		if database.IsDuplicateKey(err) {
			return apperr.FieldError("name", "a category with this name already exists")
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if err := tx.QueryRowContext(r.Context(), `SELECT id FROM event_categories WHERE id = ?`, id).Scan(&id); errors.Is(err, sql.ErrNoRows) {
				return apperr.NotFound("Category")
			}
		}
		return audit.Record(r.Context(), tx, now, audit.Entry{Action: "category.updated", EntityType: "category", EntityID: id, Summary: "Category renamed to " + in.Name})
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, Category{ID: id, Name: in.Name, Slug: slug.Make(in.Name), Description: in.Description})
}

func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	err = h.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		var used int
		if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM events WHERE category_id = ?`, id).Scan(&used); err != nil {
			return err
		}
		if used > 0 {
			return apperr.Conflict("category_in_use", fmt.Sprintf("%d event(s) use this category; move them to another category first.", used))
		}
		res, err := tx.ExecContext(r.Context(), `DELETE FROM event_categories WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return apperr.NotFound("Category")
		}
		return audit.Record(r.Context(), tx, h.now(), audit.Entry{Action: "category.deleted", EntityType: "category", EntityID: id, Summary: "Category deleted"})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// ---- Venues ---------------------------------------------------------------------------

const selectVenue = `SELECT id, organiser_id, name, address_line, city, province, country, postal_code, latitude, longitude, capacity, created_at FROM venues`

func scanVenue(row interface{ Scan(...any) error }) (Venue, error) {
	var v Venue
	err := row.Scan(&v.ID, &v.OrganiserID, &v.Name, &v.AddressLine, &v.City, &v.Province, &v.Country, &v.PostalCode, &v.Latitude, &v.Longitude, &v.Capacity, &v.CreatedAt)
	return v, err
}

// ListVenues returns shared venues plus the venues of organisers the user can see.
func (h *Handler) ListVenues(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.MustPrincipal(r)
	if err != nil {
		return err
	}
	scope := p.Scope(rbac.EventsView)
	clause, args := scope.SQL("organiser_id")
	where := "(organiser_id IS NULL OR " + clause + ")"
	if orgID := httpx.QueryInt64(r, "organiser_id"); orgID > 0 {
		where += " AND (organiser_id IS NULL OR organiser_id = ?)"
		args = append(args, orgID)
	}
	if q := httpx.QueryString(r, "q"); q != "" {
		where += " AND (name LIKE ? OR city LIKE ?)"
		args = append(args, database.EscapeLike(q), database.EscapeLike(q))
	}
	rows, err := h.db.QueryContext(r.Context(), selectVenue+" WHERE "+where+" ORDER BY name LIMIT 500", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var out []Venue
	for rows.Next() {
		v, err := scanVenue(rows)
		if err != nil {
			return err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return httpx.List(w, out)
}

type venueInput struct {
	OrganiserID int64    `json:"organiser_id"`
	Name        string   `json:"name"`
	AddressLine string   `json:"address_line"`
	City        string   `json:"city"`
	Province    string   `json:"province"`
	Country     string   `json:"country"`
	PostalCode  string   `json:"postal_code"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
	Capacity    *int     `json:"capacity"`
}

func (in *venueInput) validate() error {
	for _, f := range []*string{&in.Name, &in.AddressLine, &in.City, &in.Province, &in.Country, &in.PostalCode} {
		*f = strings.TrimSpace(*f)
	}
	if in.Country == "" {
		in.Country = "South Africa"
	}
	v := validate.New()
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 150)
	v.MaxLen("address_line", in.AddressLine, 255)
	v.Required("city", in.City)
	v.MaxLen("city", in.City, 100)
	v.MaxLen("province", in.Province, 100)
	v.MaxLen("country", in.Country, 100)
	v.MaxLen("postal_code", in.PostalCode, 20)
	if in.Capacity != nil {
		v.Range("capacity", int64(*in.Capacity), 1, 1_000_000)
	}
	switch {
	case (in.Latitude == nil) != (in.Longitude == nil):
		v.Add("latitude", "enter both the latitude and the longitude, or neither")
	case in.Latitude != nil && (*in.Latitude < -90 || *in.Latitude > 90):
		v.Add("latitude", "must be between -90 and 90")
	case in.Longitude != nil && (*in.Longitude < -180 || *in.Longitude > 180):
		v.Add("longitude", "must be between -180 and 180")
	}
	loc := resolveLocation(in.City, in.Province, in.Country)
	if isSouthAfrica(in.Country) && loc.Province == "" {
		if in.Province == "" {
			v.Add("province", "choose a province")
		} else {
			v.Add("province", "choose one of the nine provinces")
		}
	}
	if err := v.Err(); err != nil {
		return err
	}
	in.City, in.Province = loc.City, loc.Province
	if in.Latitude == nil {
		in.Latitude, in.Longitude = loc.Latitude, loc.Longitude
	}
	return nil
}

// location is where a venue is, as far as the gazetteer can tell.
type location struct {
	City, Province      string
	Latitude, Longitude *float64
}

func isSouthAfrica(country string) bool {
	return strings.EqualFold(strings.TrimSpace(country), "South Africa")
}

// resolveLocation tidies a South African venue's city and province and finds its
// coordinates: "port elizabeth, EC" becomes Gqeberha, Eastern Cape. The province is
// returned empty when it isn't one of the nine; places outside South Africa, and towns the
// gazetteer doesn't know, are returned as given without coordinates.
func resolveLocation(city, province, country string) location {
	if !isSouthAfrica(country) {
		return location{City: city, Province: province}
	}
	out := location{City: city, Province: geo.NormalizeProvince(province)}
	if province != "" && out.Province == "" {
		return out
	}
	if place, ok := geo.Lookup(city, out.Province); ok {
		lat, lng := place.Latitude, place.Longitude
		out.City, out.Province, out.Latitude, out.Longitude = place.Name, place.Province, &lat, &lng
	}
	return out
}

// BackfillVenueLocations fills in the province and coordinates of venues saved before
// locations were tracked, or whose details were entered in another form. It is safe to
// run repeatedly and touches only venues that still need it.
func BackfillVenueLocations(ctx context.Context, db *database.DB, now time.Time) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, city, province, country, latitude IS NULL FROM venues
		WHERE latitude IS NULL OR province NOT IN (`+strings.TrimSuffix(strings.Repeat("?, ", len(geo.Provinces)), ", ")+`)`, anySlice(geo.Provinces)...)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id                      int64
		city, province, country string
		noCoordinates           bool
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.city, &p.province, &p.country, &p.noCoordinates); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	updated := 0
	for _, p := range list {
		loc := resolveLocation(p.city, p.province, p.country)
		if loc.Province == "" {
			// Unknown or foreign: keep what the organiser entered.
			loc.Province = p.province
		}
		if !p.noCoordinates {
			loc.Latitude, loc.Longitude = nil, nil
		}
		if loc.City == p.city && loc.Province == p.province && loc.Latitude == nil {
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE venues SET city = ?, province = ?, latitude = COALESCE(latitude, ?), longitude = COALESCE(longitude, ?), updated_at = ? WHERE id = ?`,
			loc.City, loc.Province, loc.Latitude, loc.Longitude, now, p.id); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func anySlice(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// canManageVenue checks venue ownership: shared venues need the platform permission.
func canManageVenue(p *auth.Principal, organiserID *int64) bool {
	if organiserID == nil || *organiserID == 0 {
		return p.HasPlatform(rbac.VenuesManage)
	}
	return p.CanFor(*organiserID, rbac.VenuesManage)
}

func (h *Handler) CreateVenue(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	var in venueInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	var owner *int64
	if in.OrganiserID > 0 {
		owner = &in.OrganiserID
	}
	if !canManageVenue(p, owner) {
		return auth.ErrForbidden()
	}
	var venue Venue
	err := h.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		now := h.now()
		res, err := tx.ExecContext(r.Context(), `INSERT INTO venues (organiser_id, name, address_line, city, province, country, postal_code, latitude, longitude, capacity, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, database.NullIfZero(in.OrganiserID), in.Name, in.AddressLine, in.City, in.Province, in.Country, in.PostalCode, in.Latitude, in.Longitude, in.Capacity, now, now)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		venue, err = scanVenue(tx.QueryRowContext(r.Context(), selectVenue+" WHERE id = ?", id))
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, now, audit.Entry{Action: "venue.created", EntityType: "venue", EntityID: id, OrganiserID: in.OrganiserID, Summary: "Venue " + in.Name + " created"})
	})
	if err != nil {
		return err
	}
	return httpx.Created(w, venue)
}

func (h *Handler) loadVenueForUpdate(r *http.Request, tx *sql.Tx, p *auth.Principal, id int64) (Venue, error) {
	v, err := scanVenue(tx.QueryRowContext(r.Context(), selectVenue+" WHERE id = ? FOR UPDATE", id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, apperr.NotFound("Venue")
	}
	if err != nil {
		return v, err
	}
	if !canManageVenue(p, v.OrganiserID) {
		return v, apperr.NotFound("Venue")
	}
	return v, nil
}

func (h *Handler) UpdateVenue(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in venueInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	var venue Venue
	err = h.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		existing, err := h.loadVenueForUpdate(r, tx, p, id)
		if err != nil {
			return err
		}
		now := h.now()
		if _, err := tx.ExecContext(r.Context(), `UPDATE venues SET name = ?, address_line = ?, city = ?, province = ?, country = ?, postal_code = ?, latitude = ?, longitude = ?, capacity = ?, updated_at = ? WHERE id = ?`,
			in.Name, in.AddressLine, in.City, in.Province, in.Country, in.PostalCode, in.Latitude, in.Longitude, in.Capacity, now, id); err != nil {
			return err
		}
		venue, err = scanVenue(tx.QueryRowContext(r.Context(), selectVenue+" WHERE id = ?", id))
		if err != nil {
			return err
		}
		var orgID int64
		if existing.OrganiserID != nil {
			orgID = *existing.OrganiserID
		}
		return audit.Record(r.Context(), tx, now, audit.Entry{Action: "venue.updated", EntityType: "venue", EntityID: id, OrganiserID: orgID, Summary: "Venue " + in.Name + " updated"})
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, venue)
}

func (h *Handler) DeleteVenue(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	err = h.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		existing, err := h.loadVenueForUpdate(r, tx, p, id)
		if err != nil {
			return err
		}
		var used int
		if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM events WHERE venue_id = ?`, id).Scan(&used); err != nil {
			return err
		}
		if used > 0 {
			return apperr.Conflict("venue_in_use", fmt.Sprintf("%d event(s) use this venue and it cannot be deleted.", used))
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM venues WHERE id = ?`, id); err != nil {
			return err
		}
		var orgID int64
		if existing.OrganiserID != nil {
			orgID = *existing.OrganiserID
		}
		return audit.Record(r.Context(), tx, h.now(), audit.Entry{Action: "venue.deleted", EntityType: "venue", EntityID: id, OrganiserID: orgID, Summary: "Venue " + existing.Name + " deleted"})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}
