package geo

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeProvince(t *testing.T) {
	cases := map[string]string{
		"KZN":           KwaZuluNatal,
		"KwaZulu Natal": KwaZuluNatal,
		"North-West":    NorthWest,
		" gauteng ":     Gauteng,
		"Western Cape":  WesternCape,
		"Ontario":       "",
		"":              "",
	}
	for in, want := range cases {
		if got := NormalizeProvince(in); got != want {
			t.Errorf("NormalizeProvince(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDatasetLoads(t *testing.T) {
	all := Places()
	if len(all) < 12000 {
		t.Fatalf("only %d places loaded", len(all))
	}
	known := map[string]bool{}
	for _, p := range Provinces {
		known[p] = true
	}
	type placeKey struct{ province, name string }
	seen := map[placeKey]bool{}
	for i, p := range all {
		if !known[p.Province] {
			t.Fatalf("%s has unknown province %q", p.Name, p.Province)
		}
		if p.Latitude > -21.5 || p.Latitude < -35.5 || p.Longitude < 16 || p.Longitude > 33.5 {
			t.Fatalf("%s is outside South Africa: %v, %v", p.Name, p.Latitude, p.Longitude)
		}
		if strings.ContainsRune(p.Name, utf8.RuneError) {
			t.Fatalf("%q has a broken character", p.Name)
		}
		k := placeKey{p.Province, key(p.Name)}
		if seen[k] {
			t.Fatalf("%s appears twice in %s", p.Name, p.Province)
		}
		seen[k] = true
		if i > 0 && all[i-1].Province == p.Province && !lessName(all[i-1].Name, p.Name) {
			t.Fatalf("%s should sort before %s", p.Name, all[i-1].Name)
		}
	}
	for _, province := range Provinces {
		if len(PlacesIn(province)) < 100 {
			t.Fatalf("%s has only %d places", province, len(PlacesIn(province)))
		}
	}
	if PlacesIn("Atlantis") != nil {
		t.Fatal("an unknown province has no places")
	}
}

func TestLookup(t *testing.T) {
	cases := []struct{ city, province, wantName, wantProvince string }{
		{"Port Elizabeth", "", "Gqeberha", EasternCape},
		{"Nelspruit", "", "Mbombela", Mpumalanga},
		{"Umhlanga Rocks", "KZN", "Umhlanga", KwaZuluNatal},
		{"Ballito", "", "Ballito", KwaZuluNatal},            // missing from the dataset, added
		{"Pretoria", "", "Pretoria", Gauteng},               // not the Eastern Cape village
		{"Witbank", "Mpumalanga", "eMalahleni", Mpumalanga}, // renamed town
		{"Witbank", "Eastern Cape", "Witbank", EasternCape}, // the village keeps its name
		{"Sutherland", "Northern Cape", "Sutherland", NorthernCape},
		{"Grootfontein", "Free State", "Grootfontein", FreeState}, // a small place from the dataset
		{"Daniëlskuil", "", "Daniëlskuil", NorthernCape},          // accent repaired
		{"Tsetse", "North West", "Tsetse", NorthWest},             // broken letter made plain
		{"Danielskuil", "", "Daniëlskuil", NorthernCape},          // typed without the accent
	}
	for _, c := range cases {
		p, ok := Lookup(c.city, c.province)
		if !ok || p.Name != c.wantName || p.Province != c.wantProvince {
			t.Errorf("Lookup(%q, %q) = %+v, %v; want %s, %s", c.city, c.province, p, ok, c.wantName, c.wantProvince)
		}
	}
	if _, ok := Lookup("Durban", "Gauteng"); ok {
		t.Fatal("a city in another province must not match")
	}
	if _, ok := Lookup("Atlantis City", ""); ok {
		t.Fatal("unknown places must not match")
	}
	for _, p := range PlacesIn(EasternCape) {
		if p.Name == "Port Elizabeth" || p.Name == "Grahamstown" {
			t.Fatalf("%s should be listed under its current name", p.Name)
		}
		if p.Name == "Gqeberha" && (len(p.FormerNames) != 1 || p.FormerNames[0] != "Port Elizabeth") {
			t.Fatalf("Gqeberha's former names: %v", p.FormerNames)
		}
	}
}

func TestDistance(t *testing.T) {
	jhb, _ := Lookup("Johannesburg", "")
	pta, _ := Lookup("Pretoria", "")
	cpt, _ := Lookup("Cape Town", "")
	if d := DistanceKM(jhb.Latitude, jhb.Longitude, pta.Latitude, pta.Longitude); d < 50 || d > 60 {
		t.Fatalf("Johannesburg to Pretoria = %.1f km", d)
	}
	if d := DistanceKM(jhb.Latitude, jhb.Longitude, cpt.Latitude, cpt.Longitude); math.Abs(d-1265) > 20 {
		t.Fatalf("Johannesburg to Cape Town = %.1f km", d)
	}
	minLat, maxLat, minLng, maxLng := BoundingBox(jhb.Latitude, jhb.Longitude, 200)
	if pta.Latitude < minLat || pta.Latitude > maxLat || pta.Longitude < minLng || pta.Longitude > maxLng {
		t.Fatal("Pretoria must fall inside a 200 km box around Johannesburg")
	}
}

func BenchmarkBuild(b *testing.B) {
	for b.Loop() {
		if _, err := build(datasetJSON); err != nil {
			b.Fatal(err)
		}
	}
}
