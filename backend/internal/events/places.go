package events

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/geo"
)

// OtherPlaces groups venues whose province isn't one of the nine, such as venues abroad.
const OtherPlaces = "Other places"

// LocationCity is a city with upcoming events, for the marketplace's location filter.
type LocationCity struct {
	Name       string   `json:"name"`
	EventCount int      `json:"event_count"`
	Latitude   *float64 `json:"latitude"`
	Longitude  *float64 `json:"longitude"`
}

// LocationProvince is a province and its cities with upcoming events, in alphabetical order.
type LocationProvince struct {
	Name       string         `json:"name"`
	EventCount int            `json:"event_count"`
	Cities     []LocationCity `json:"cities"`
}

// PublicLocations lists all nine provinces, including those with no upcoming events yet (so
// a new marketplace shows its whole coverage), and under each only the cities that have
// upcoming events. Both are in alphabetical order; venues outside the nine come last.
func (s *Service) PublicLocations(ctx context.Context) ([]LocationProvince, error) {
	key := "public:v" + s.publicVersion(ctx) + ":locations"
	var cached []LocationProvince
	if hit, _ := cache.GetJSON(ctx, s.cache, key, &cached); hit {
		return cached, nil
	}

	type cityKey struct{ province, name string }
	cities := map[cityKey]*LocationCity{}
	cityFor := func(province, name string, lat, lng *float64) *LocationCity {
		k := cityKey{province, strings.ToLower(name)}
		c, ok := cities[k]
		if !ok {
			c = &LocationCity{Name: name, Latitude: lat, Longitude: lng}
			cities[k] = c
		}
		return c
	}
	rows, err := s.db.QueryContext(ctx, `SELECT v.city, v.province, COUNT(DISTINCT e.id), AVG(v.latitude), AVG(v.longitude)
		FROM events e JOIN organisers o ON o.id = e.organiser_id JOIN venues v ON v.id = e.venue_id
		WHERE `+publicOnSale+` GROUP BY v.city, v.province`, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var city, province string
		var count int
		var lat, lng *float64
		if err := rows.Scan(&city, &province, &count, &lat, &lng); err != nil {
			return nil, err
		}
		if province = geo.NormalizeProvince(province); province == "" {
			province = OtherPlaces
		}
		cityFor(province, city, lat, lng).EventCount += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	byProvince := make(map[string]*LocationProvince, len(geo.Provinces)+1)
	for _, name := range geo.Provinces {
		byProvince[name] = &LocationProvince{Name: name, Cities: []LocationCity{}}
	}
	for k, c := range cities {
		p, ok := byProvince[k.province]
		if !ok {
			p = &LocationProvince{Name: k.province, Cities: []LocationCity{}}
			byProvince[k.province] = p
		}
		p.Cities = append(p.Cities, *c)
		p.EventCount += c.EventCount
	}
	out := make([]LocationProvince, 0, len(byProvince))
	for _, p := range byProvince {
		sort.Slice(p.Cities, func(i, j int) bool { return strings.ToLower(p.Cities[i].Name) < strings.ToLower(p.Cities[j].Name) })
		out = append(out, *p)
	}
	// Provinces alphabetically, with places outside the nine last.
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Name == OtherPlaces) != (out[j].Name == OtherPlaces) {
			return out[j].Name == OtherPlaces
		}
		return out[i].Name < out[j].Name
	})
	_ = cache.SetJSON(ctx, s.cache, key, out, publicListTTL)
	return out, nil
}

// NearbyFilter finds events around a point.
type NearbyFilter struct {
	Latitude  float64
	Longitude float64
	RadiusKM  float64
	Limit     int
}

// NearbyPublic returns upcoming events at venues within the radius, soonest first, each
// with its distance. Coordinates are rounded to about a kilometre before use, so the
// buyer's exact position is never stored, even in the cache.
func (s *Service) NearbyPublic(ctx context.Context, f NearbyFilter) ([]PublicEvent, error) {
	lat := math.Round(f.Latitude*100) / 100
	lng := math.Round(f.Longitude*100) / 100
	key := fmt.Sprintf("public:v%s:nearby:%.2f:%.2f:%.0f:%d", s.publicVersion(ctx), lat, lng, f.RadiusKM, f.Limit)
	var cached []PublicEvent
	if hit, _ := cache.GetJSON(ctx, s.cache, key, &cached); hit {
		return cached, nil
	}

	minLat, maxLat, minLng, maxLng := geo.BoundingBox(lat, lng, f.RadiusKM)
	const distance = `ST_Distance_Sphere(POINT(v.longitude, v.latitude), POINT(?, ?))`
	rows, err := s.db.QueryContext(ctx, `SELECT `+publicEventColumns+`, `+distance+` / 1000`+publicEventJoins+`
		WHERE `+publicOnSale+`
		AND v.latitude BETWEEN ? AND ? AND v.longitude BETWEEN ? AND ?
		AND `+distance+` <= ?
		ORDER BY e.starts_at, e.id LIMIT ?`,
		lng, lat, s.now(), minLat, maxLat, minLng, maxLng, lng, lat, f.RadiusKM*1000, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PublicEvent{}
	for rows.Next() {
		var km float64
		e, err := s.scanPublic(rows, &km)
		if err != nil {
			return nil, err
		}
		km = math.Round(km)
		e.DistanceKM = &km
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = cache.SetJSON(ctx, s.cache, key, out, publicListTTL)
	return out, nil
}

// Place is a city, town or settlement buyers and organisers can choose.
type Place struct {
	Name        string   `json:"name"`
	FormerNames []string `json:"former_names,omitempty"`
	Latitude    float64  `json:"latitude"`
	Longitude   float64  `json:"longitude"`
}

// PlacesIn lists a province's cities, towns and settlements in alphabetical order.
func PlacesIn(province string) ([]Place, bool) {
	list := geo.PlacesIn(province)
	if list == nil {
		return nil, false
	}
	out := make([]Place, len(list))
	for i, p := range list {
		out[i] = Place{Name: p.Name, FormerNames: p.FormerNames, Latitude: p.Latitude, Longitude: p.Longitude}
	}
	return out, true
}
