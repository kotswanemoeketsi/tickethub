package app_test

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/catalog"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/geo"
)

type venueView struct {
	City      string   `json:"city"`
	Province  string   `json:"province"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

func (e *env) moveVenue(token string, venueID int64, city, province string) venueView {
	e.t.Helper()
	var v venueView
	e.must(200, e.request("PUT", fmt.Sprintf("/api/v1/admin/venues/%d", venueID), token, map[string]any{
		"name": "Hall", "city": city, "province": province,
	})).decode(e.t, &v)
	return v
}

// Venues get a tidy province and coordinates from their city, the marketplace lists
// provinces with their cities alphabetically, and events can be found by province and by
// distance.
func TestLocationsAndNearbyEvents(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	jhb := e.publishedEvent(admin, 72*time.Hour, 50, ticketType("General", 10000, 50))
	cpt := e.publishedEvent(admin, 96*time.Hour, 50, ticketType("General", 10000, 50))
	pe := e.publishedEvent(admin, 120*time.Hour, 50, ticketType("General", 10000, 50))

	if v := e.moveVenue(admin, cpt.VenueID, "cape town", "WC"); v.City != "Cape Town" || v.Province != "Western Cape" || v.Latitude == nil {
		t.Fatalf("Cape Town venue: %+v", v)
	}
	if v := e.moveVenue(admin, pe.VenueID, "Port Elizabeth", ""); v.City != "Gqeberha" || v.Province != "Eastern Cape" || v.Longitude == nil {
		t.Fatalf("former city name: %+v", v)
	}
	e.must(422, e.request("PUT", fmt.Sprintf("/api/v1/admin/venues/%d", pe.VenueID), admin, map[string]any{
		"name": "Hall", "city": "Gqeberha", "province": "Atlantis",
	}))
	e.must(422, e.request("PUT", fmt.Sprintf("/api/v1/admin/venues/%d", pe.VenueID), admin, map[string]any{
		"name": "Hall", "city": "Gqeberha", "latitude": -33.9,
	}))
	// A South African town the platform doesn't know needs its province.
	e.must(422, e.request("POST", "/api/v1/admin/venues", admin, map[string]any{"name": "Barn", "city": "Nowhereton"}))
	var barn venueView
	e.must(201, e.request("POST", "/api/v1/admin/venues", admin, map[string]any{"name": "Barn", "city": "Nowhereton", "province": "Free State"})).decode(t, &barn)
	if barn.Province != "Free State" || barn.Latitude != nil {
		t.Fatalf("unknown town: %+v", barn)
	}

	var provinces []struct {
		Name       string `json:"name"`
		EventCount int    `json:"event_count"`
		Cities     []struct {
			Name       string `json:"name"`
			EventCount int    `json:"event_count"`
		} `json:"cities"`
	}
	e.must(200, e.request("GET", "/api/v1/public/locations", "", nil)).decode(t, &provinces)
	names := []string{}
	counts := map[string]int{}
	for _, p := range provinces {
		names = append(names, p.Name)
		cities := []string{}
		for _, c := range p.Cities {
			cities = append(cities, strings.ToLower(c.Name))
			counts[c.Name] = c.EventCount
		}
		if !slices.IsSorted(cities) {
			t.Fatalf("%s cities are not alphabetical: %v", p.Name, cities)
		}
	}
	// All nine provinces are listed, even without events, but only cities with events.
	for _, want := range geo.Provinces {
		if !slices.Contains(names, want) {
			t.Fatalf("province %s missing from %v", want, names)
		}
	}
	if !slices.IsSorted(names) {
		t.Fatalf("provinces are not alphabetical: %v", names)
	}
	for _, p := range provinces {
		if p.Name == "Limpopo" && (p.EventCount != 0 || p.Cities == nil || len(p.Cities) != 0) {
			t.Fatalf("a province without events should have a count of 0 and an empty city list: %+v", p)
		}
	}
	if counts["Johannesburg"] < 1 || counts["Cape Town"] != 1 || counts["Gqeberha"] != 1 || len(counts) != 3 {
		t.Fatalf("event counts: %v", counts)
	}

	// Every city and town in a province, alphabetically, for the forms.
	var places []struct {
		Name     string  `json:"name"`
		Latitude float64 `json:"latitude"`
	}
	e.must(200, e.request("GET", "/api/v1/public/places?province="+url.QueryEscape("KwaZulu-Natal"), "", nil)).decode(t, &places)
	placeNames := []string{}
	for _, p := range places {
		placeNames = append(placeNames, strings.ToLower(p.Name))
	}
	if len(places) < 1000 || !slices.IsSorted(placeNames) || !slices.Contains(placeNames, "ballito") || !slices.Contains(placeNames, "umhlanga") {
		t.Fatalf("KwaZulu-Natal places: %d, sorted %v", len(places), slices.IsSorted(placeNames))
	}
	e.must(422, e.request("GET", "/api/v1/public/places?province=Atlantis", "", nil))

	ids := func(path string) []int64 {
		var list []struct {
			ID int64 `json:"id"`
		}
		e.must(200, e.request("GET", path, "", nil)).decode(t, &list)
		out := []int64{}
		for _, ev := range list {
			out = append(out, ev.ID)
		}
		return out
	}
	if got := ids("/api/v1/public/events?province=" + url.QueryEscape("Western Cape")); !slices.Equal(got, []int64{cpt.EventID}) {
		t.Fatalf("Western Cape events: %v", got)
	}

	// Other tests leave events in Johannesburg, so this one uses a town of its own: Cullinan
	// is about 30 km from Pretoria, Johannesburg about 55 km.
	if v := e.moveVenue(admin, jhb.VenueID, "Cullinan", "Gauteng"); v.Latitude == nil {
		t.Fatalf("Cullinan venue: %+v", v)
	}
	var near []struct {
		ID         int64    `json:"id"`
		DistanceKM *float64 `json:"distance_km"`
	}
	e.must(200, e.request("GET", "/api/v1/public/nearby?lat=-25.7479&lng=28.2293&radius_km=45", "", nil)).decode(t, &near)
	found := map[int64]float64{}
	for _, ev := range near {
		if ev.DistanceKM == nil {
			t.Fatal("nearby events must include their distance")
		}
		found[ev.ID] = *ev.DistanceKM
	}
	if d, ok := found[jhb.EventID]; !ok || d < 20 || d > 40 {
		t.Fatalf("Cullinan event from Pretoria: %v", found)
	}
	for id, d := range found {
		if d > 45 {
			t.Fatalf("event %d is %v km away, outside the 45 km radius", id, d)
		}
	}
	if _, ok := found[cpt.EventID]; ok {
		t.Fatal("Cape Town is not near Pretoria")
	}
	if got := ids("/api/v1/public/nearby?lat=-33.95&lng=18.45&radius_km=50"); !slices.Equal(got, []int64{cpt.EventID}) {
		t.Fatalf("near Cape Town: %v", got)
	}
	e.must(422, e.request("GET", "/api/v1/public/nearby?lng=28.2", "", nil))
	e.must(422, e.request("GET", "/api/v1/public/nearby?lat=-25&lng=28&radius_km=5000", "", nil))
}

// Venues saved before locations were tracked are tidied and placed on the map at start-up.
func TestBackfillVenueLocations(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 72*time.Hour, 50, ticketType("General", 10000, 50))
	if _, err := e.app.DB.ExecContext(e.ctx, `UPDATE venues SET city = 'durban', province = 'kzn', latitude = NULL, longitude = NULL WHERE id = ?`, fx.VenueID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.DB.ExecContext(e.ctx, `INSERT INTO venues (name, city, province, country, created_at, updated_at) VALUES ('Abroad', 'Gaborone', 'South-East', 'Botswana', NOW(), NOW())`); err != nil {
		t.Fatal(err)
	}
	n, err := catalog.BackfillVenueLocations(e.ctx, e.app.DB, e.clock.Now())
	if err != nil || n != 1 {
		t.Fatalf("backfill: %d venues, %v", n, err)
	}
	var city, province string
	var lat *float64
	if err := e.app.DB.QueryRowContext(e.ctx, `SELECT city, province, latitude FROM venues WHERE id = ?`, fx.VenueID).Scan(&city, &province, &lat); err != nil {
		t.Fatal(err)
	}
	if city != "Durban" || province != "KwaZulu-Natal" || lat == nil {
		t.Fatalf("after backfill: %s, %s, %v", city, province, lat)
	}
	if n, err := catalog.BackfillVenueLocations(e.ctx, e.app.DB, e.clock.Now()); err != nil || n != 0 {
		t.Fatalf("second run should change nothing: %d, %v", n, err)
	}
}
