// Package geo knows South Africa's provinces and the coordinates of its cities, towns and
// settlements, so venues can be grouped by province and events found by distance without
// calling an external geocoding service.
//
// The places come from the SouthAfricanCities dataset in data/ (see data/README.md),
// brought up to date with current town names and a few well-known places it lacks.
package geo

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// The nine provinces, as they are written officially.
const (
	EasternCape  = "Eastern Cape"
	FreeState    = "Free State"
	Gauteng      = "Gauteng"
	KwaZuluNatal = "KwaZulu-Natal"
	Limpopo      = "Limpopo"
	Mpumalanga   = "Mpumalanga"
	NorthWest    = "North West"
	NorthernCape = "Northern Cape"
	WesternCape  = "Western Cape"
)

// Provinces lists the provinces in alphabetical order.
var Provinces = []string{EasternCape, FreeState, Gauteng, KwaZuluNatal, Limpopo, Mpumalanga, NorthWest, NorthernCape, WesternCape}

// Place is a city, town or settlement with the coordinates of its centre.
type Place struct {
	Name      string
	Province  string
	Latitude  float64
	Longitude float64
	// Major marks the well-known cities and towns, which win when a name is ambiguous.
	Major bool
	// FormerNames are the names a renamed town had, such as Port Elizabeth for Gqeberha.
	FormerNames []string
}

var provinceAliases = map[string]string{
	"easterncape": EasternCape, "ec": EasternCape,
	"freestate": FreeState, "fs": FreeState, "orangefreestate": FreeState,
	"gauteng": Gauteng, "gp": Gauteng, "gt": Gauteng,
	"kwazulunatal": KwaZuluNatal, "kzn": KwaZuluNatal, "natal": KwaZuluNatal,
	"limpopo": Limpopo, "lp": Limpopo, "lim": Limpopo, "northernprovince": Limpopo,
	"mpumalanga": Mpumalanga, "mp": Mpumalanga,
	"northwest": NorthWest, "nw": NorthWest,
	"northerncape": NorthernCape, "nc": NorthernCape,
	"westerncape": WesternCape, "wc": WesternCape,
}

// NormalizeProvince returns the official name of a province written in any common form
// ("KZN", "kwazulu natal", "North-West"), or "" when it is not a South African province.
func NormalizeProvince(s string) string {
	return provinceAliases[key(s)]
}

// majorPlaces are the well-known cities and towns, under their current names. They are
// marked Major in the gazetteer and added to it when the dataset lacks them. Coordinates
// are town centres, which is plenty for "events near you".
var majorPlaces = []struct {
	Name, Province      string
	Latitude, Longitude float64
}{
	// Eastern Cape
	{"Bhisho", EasternCape, -32.8470, 27.4422},
	{"East London", EasternCape, -33.0153, 27.9116},
	{"Gqeberha", EasternCape, -33.9608, 25.6022},
	{"Graaff-Reinet", EasternCape, -32.2522, 24.5308},
	{"Jeffreys Bay", EasternCape, -34.0507, 24.9076},
	{"Kariega", EasternCape, -33.7576, 25.3971},
	{"Komani", EasternCape, -31.8976, 26.8753},
	{"Makhanda", EasternCape, -33.3042, 26.5328},
	{"Mthatha", EasternCape, -31.5889, 28.7844},
	{"Port Alfred", EasternCape, -33.5906, 26.8910},
	{"Qonce", EasternCape, -32.8833, 27.4000},
	// Free State
	{"Bethlehem", FreeState, -28.2308, 28.3071},
	{"Bloemfontein", FreeState, -29.0852, 26.1596},
	{"Clarens", FreeState, -28.5167, 28.4167},
	{"Harrismith", FreeState, -28.2725, 29.1295},
	{"Kroonstad", FreeState, -27.6504, 27.2349},
	{"Parys", FreeState, -26.9033, 27.4560},
	{"Phuthaditjhaba", FreeState, -28.5242, 28.8158},
	{"Sasolburg", FreeState, -26.8136, 27.8170},
	{"Welkom", FreeState, -27.9774, 26.7351},
	// Gauteng
	{"Alberton", Gauteng, -26.2672, 28.1222},
	{"Benoni", Gauteng, -26.1885, 28.3206},
	{"Boksburg", Gauteng, -26.2125, 28.2626},
	{"Centurion", Gauteng, -25.8601, 28.1890},
	{"Fourways", Gauteng, -26.0167, 28.0059},
	{"Germiston", Gauteng, -26.2309, 28.1772},
	{"Johannesburg", Gauteng, -26.2041, 28.0473},
	{"Kempton Park", Gauteng, -26.1003, 28.2331},
	{"Krugersdorp", Gauteng, -26.0855, 27.7759},
	{"Mamelodi", Gauteng, -25.7200, 28.3950},
	{"Midrand", Gauteng, -25.9992, 28.1263},
	{"Pretoria", Gauteng, -25.7479, 28.2293},
	{"Randburg", Gauteng, -26.0936, 28.0064},
	{"Roodepoort", Gauteng, -26.1625, 27.8725},
	{"Sandton", Gauteng, -26.1076, 28.0567},
	{"Soweto", Gauteng, -26.2485, 27.8540},
	{"Springs", Gauteng, -26.2547, 28.4428},
	{"Tembisa", Gauteng, -25.9964, 28.2268},
	{"Vanderbijlpark", Gauteng, -26.7110, 27.8378},
	{"Vereeniging", Gauteng, -26.6731, 27.9261},
	// KwaZulu-Natal
	{"Ballito", KwaZuluNatal, -29.5390, 31.2140},
	{"Durban", KwaZuluNatal, -29.8587, 31.0218},
	{"Empangeni", KwaZuluNatal, -28.7620, 31.8933},
	{"Eshowe", KwaZuluNatal, -28.8936, 31.4693},
	{"Howick", KwaZuluNatal, -29.4778, 30.2283},
	{"Ladysmith", KwaZuluNatal, -28.5539, 29.7784},
	{"Margate", KwaZuluNatal, -30.8636, 30.3705},
	{"Newcastle", KwaZuluNatal, -27.7580, 29.9318},
	{"Pietermaritzburg", KwaZuluNatal, -29.6006, 30.3794},
	{"Pinetown", KwaZuluNatal, -29.8167, 30.8667},
	{"Port Shepstone", KwaZuluNatal, -30.7414, 30.4550},
	{"Richards Bay", KwaZuluNatal, -28.7807, 32.0383},
	{"Umhlanga", KwaZuluNatal, -29.7270, 31.0817},
	{"Vryheid", KwaZuluNatal, -27.7695, 30.7916},
	// Limpopo
	{"Bela-Bela", Limpopo, -24.8849, 28.2934},
	{"Hoedspruit", Limpopo, -24.3546, 30.9566},
	{"Lephalale", Limpopo, -23.6732, 27.7489},
	{"Makhado", Limpopo, -23.0434, 29.9047},
	{"Modimolle", Limpopo, -24.7000, 28.4060},
	{"Mokopane", Limpopo, -24.1944, 29.0097},
	{"Musina", Limpopo, -22.3524, 30.0360},
	{"Phalaborwa", Limpopo, -23.9430, 31.1411},
	{"Polokwane", Limpopo, -23.9045, 29.4689},
	{"Thohoyandou", Limpopo, -22.9456, 30.4849},
	{"Tzaneen", Limpopo, -23.8332, 30.1635},
	// Mpumalanga
	{"Barberton", Mpumalanga, -25.7860, 31.0530},
	{"Dullstroom", Mpumalanga, -25.4167, 30.1000},
	{"eMalahleni", Mpumalanga, -25.8713, 29.2332},
	{"Ermelo", Mpumalanga, -26.5333, 29.9833},
	{"Graskop", Mpumalanga, -24.9333, 30.8333},
	{"Hazyview", Mpumalanga, -25.0447, 31.1270},
	{"Mbombela", Mpumalanga, -25.4753, 30.9694},
	{"Middelburg", Mpumalanga, -25.7751, 29.4648},
	{"Sabie", Mpumalanga, -25.0981, 30.7794},
	{"Secunda", Mpumalanga, -26.5160, 29.1700},
	{"Standerton", Mpumalanga, -26.9330, 29.2410},
	{"White River", Mpumalanga, -25.3317, 31.0115},
	// North West
	{"Brits", NorthWest, -25.6347, 27.7802},
	{"Hartbeespoort", NorthWest, -25.7436, 27.8960},
	{"Klerksdorp", NorthWest, -26.8521, 26.6667},
	{"Lichtenburg", NorthWest, -26.1520, 26.1597},
	{"Mahikeng", NorthWest, -25.8560, 25.6403},
	{"Potchefstroom", NorthWest, -26.7145, 27.0970},
	{"Rustenburg", NorthWest, -25.6676, 27.2421},
	{"Sun City", NorthWest, -25.3346, 27.0927},
	{"Vryburg", NorthWest, -26.9566, 24.7284},
	{"Zeerust", NorthWest, -25.5389, 26.0750},
	// Northern Cape
	{"Calvinia", NorthernCape, -31.4707, 19.7760},
	{"Colesberg", NorthernCape, -30.7190, 25.0970},
	{"De Aar", NorthernCape, -30.6497, 24.0122},
	{"Kathu", NorthernCape, -27.6950, 23.0490},
	{"Kimberley", NorthernCape, -28.7282, 24.7499},
	{"Kuruman", NorthernCape, -27.4524, 23.4325},
	{"Springbok", NorthernCape, -29.6643, 17.8865},
	{"Sutherland", NorthernCape, -32.3946, 20.6610},
	{"Upington", NorthernCape, -28.4478, 21.2561},
	// Western Cape
	{"Bellville", WesternCape, -33.9000, 18.6333},
	{"Cape Town", WesternCape, -33.9249, 18.4241},
	{"Durbanville", WesternCape, -33.8320, 18.6470},
	{"Franschhoek", WesternCape, -33.9133, 19.1169},
	{"George", WesternCape, -33.9630, 22.4617},
	{"Hermanus", WesternCape, -34.4187, 19.2345},
	{"Knysna", WesternCape, -34.0363, 23.0471},
	{"Langebaan", WesternCape, -33.0928, 18.0336},
	{"Mossel Bay", WesternCape, -34.1831, 22.1460},
	{"Oudtshoorn", WesternCape, -33.5907, 22.2014},
	{"Paarl", WesternCape, -33.7342, 18.9621},
	{"Plettenberg Bay", WesternCape, -34.0527, 23.3716},
	{"Somerset West", WesternCape, -34.0757, 18.8433},
	{"Stellenbosch", WesternCape, -33.9321, 18.8602},
	{"Swellendam", WesternCape, -34.0230, 20.4417},
	{"Worcester", WesternCape, -33.6465, 19.4485},
}

// renamed maps names in the dataset, which dates from 2017, to the current official names,
// per province. An empty new name drops the entry because the current name is already
// listed separately.
var renamed = map[string]map[string]string{
	EasternCape: {
		"Port Elizabeth": "Gqeberha", "Grahamstown": "Makhanda", "Uitenhage": "Kariega",
		"Queenstown": "Komani", "King William's Town": "Qonce", "Umtata": "Mthatha", "Bisho": "",
	},
	KwaZuluNatal: {"Umhlanga Rocks": "Umhlanga"},
	Limpopo: {
		"Pietersburg": "", "Warmbad": "Bela-Bela", "Louis Trichardt": "Makhado", "Potgietersrus": "Mokopane",
		"Nylstroom": "Modimolle", "Ellisras": "Lephalale", "Messina": "Musina",
	},
	Mpumalanga: {"Nelspruit": "Mbombela", "Witbank": "eMalahleni"},
	NorthWest:  {"Mafikeng": "Mahikeng"},
}

// accented fixes names whose accented letter is broken in the dataset (stored as U+FFFD),
// where the correct spelling is certain. Others fall back to the plain letter.
var accented = map[string]string{
	"aasvoelkop": "Aasvoëlkop", "assvoelkop": "Assvoëlkop", "belgie": "België", "breerivier": "Breërivier",
	"danielskuil": "Daniëlskuil", "droefontein": "Droëfontein", "droevlakte": "Droëvlakte", "droevlei": "Droëvlei",
	"gluckstadt": "Glückstadt", "hoekraal": "Hoëkraal", "hoeveld": "Hoëveld", "klein droehout": "Klein Droëhout",
	"kransvoelleegte": "Kransvoëlleegte", "morelig": "Môrelig", "moreson": "Môreson", "morester": "Môrester",
	"skandinawiedrif": "Skandinawiëdrif", "vergelee": "Vergeleë", "voelfontein": "Voëlfontein", "voelklip": "Voëlklip",
	"water-fouche": "Water-fouché", "wilhelmshohe": "Wilhelmshöhe", "wydgelee": "Wydgeleë",
}

// repairName returns a usable spelling of a dataset name: the name itself, a known
// accented spelling, or the name with each broken letter replaced by its plain form.
func repairName(accentCity, plain string) string {
	if !strings.ContainsRune(accentCity, utf8.RuneError) {
		return accentCity
	}
	if fixed, ok := accented[strings.ToLower(strings.TrimSpace(plain))]; ok {
		return fixed
	}
	broken, base := []rune(accentCity), []rune(plain)
	if len(broken) != len(base) {
		return ""
	}
	for i, r := range broken {
		if r == utf8.RuneError {
			broken[i] = base[i]
		}
	}
	return string(broken)
}

// cityAliases are informal and former names people still type, mapped to current names.
var cityAliases = map[string]string{
	"portelizabeth": "Gqeberha", "pe": "Gqeberha",
	"grahamstown":      "Makhanda",
	"uitenhage":        "Kariega",
	"queenstown":       "Komani",
	"kingwilliamstown": "Qonce",
	"umtata":           "Mthatha",
	"bisho":            "Bhisho",
	"nelspruit":        "Mbombela",
	"witbank":          "eMalahleni",
	"mafikeng":         "Mahikeng", "mafeking": "Mahikeng",
	"louistrichardt": "Makhado",
	"pietersburg":    "Polokwane",
	"potgietersrus":  "Mokopane",
	"warmbad":        "Bela-Bela", "warmbaths": "Bela-Bela",
	"nylstroom":     "Modimolle",
	"ellisras":      "Lephalale",
	"messina":       "Musina",
	"umhlangarocks": "Umhlanga",
	"joburg":        "Johannesburg", "jozi": "Johannesburg", "jhb": "Johannesburg",
	"tshwane": "Pretoria", "pta": "Pretoria",
	"pmb": "Pietermaritzburg", "maritzburg": "Pietermaritzburg",
	"ct": "Cape Town", "kaapstad": "Cape Town",
	"hartebeespoort": "Hartbeespoort", "harties": "Hartbeespoort",
	"plett": "Plettenberg Bay",
	"jbay":  "Jeffreys Bay",
}

//go:embed data/SouthAfricanCities.json
var datasetJSON []byte

type gazetteer struct {
	all        []Place
	byProvince map[string][]Place
	// byName holds every place with a given name key, majors first.
	byName map[string][]Place
}

var load = sync.OnceValue(func() *gazetteer {
	g, err := build(datasetJSON)
	if err != nil {
		// The dataset is embedded at build time, so this only fails if the file is broken;
		// the package tests catch that before release.
		panic(err)
	}
	return g
})

// build turns the dataset into the gazetteer: official province names, current town
// names, the well-known places added and duplicates dropped.
func build(raw []byte) (*gazetteer, error) {
	var rows []struct {
		Name      string  `json:"AccentCity"`
		Plain     string  `json:"City"`
		Province  string  `json:"ProvinceName"`
		Latitude  float64 `json:"Latitude"`
		Longitude float64 `json:"Longitude"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("read places dataset: %w", err)
	}
	type placeKey struct{ province, name string }
	seen := map[placeKey]int{}
	var all []Place
	add := func(p Place) {
		k := placeKey{p.Province, key(p.Name)}
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = len(all)
		all = append(all, p)
	}

	// Well-known places first, so they win over a dataset entry of the same name, which
	// keeps names unique per province.
	for _, m := range majorPlaces {
		add(Place{Name: m.Name, Province: m.Province, Latitude: m.Latitude, Longitude: m.Longitude, Major: true})
	}
	for _, r := range rows {
		province := NormalizeProvince(r.Province)
		name := strings.TrimSpace(repairName(r.Name, r.Plain))
		if province == "" || name == "" {
			continue
		}
		if current, ok := renamed[province][name]; ok {
			if current == "" {
				continue
			}
			name = current
		}
		add(Place{Name: name, Province: province, Latitude: r.Latitude, Longitude: r.Longitude})
	}

	former := map[placeKey][]string{}
	for province, names := range renamed {
		for old, current := range names {
			if current != "" {
				k := placeKey{province, key(current)}
				former[k] = append(former[k], old)
			}
		}
	}
	for i := range all {
		if names := former[placeKey{all[i].Province, key(all[i].Name)}]; names != nil {
			sort.Strings(names)
			all[i].FormerNames = names
		}
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Province != all[j].Province {
			return all[i].Province < all[j].Province
		}
		return lessName(all[i].Name, all[j].Name)
	})
	g := &gazetteer{all: all, byProvince: map[string][]Place{}, byName: map[string][]Place{}}
	for _, p := range all {
		g.byProvince[p.Province] = append(g.byProvince[p.Province], p)
		k := key(p.Name)
		if p.Major {
			g.byName[k] = append([]Place{p}, g.byName[k]...)
		} else {
			g.byName[k] = append(g.byName[k], p)
		}
	}
	return g, nil
}

// Lookup finds a place by name, including former names such as "Port Elizabeth".
// With a province it only matches places in that province; without one it prefers the
// well-known place, so "Pretoria" is the city in Gauteng and not the village in the
// Eastern Cape.
func Lookup(city, province string) (Place, bool) {
	g := load()
	k := key(city)
	candidates := g.byName[k]
	// A former name usually means the renamed town, though a village elsewhere may still
	// carry it ("Witbank" is eMalahleni, but also a village in the Eastern Cape).
	if alias, ok := cityAliases[k]; ok {
		candidates = append(append([]Place(nil), g.byName[key(alias)]...), candidates...)
	}
	want := NormalizeProvince(province)
	for _, p := range candidates {
		if want == "" || p.Province == want {
			return p, true
		}
	}
	return Place{}, false
}

// Places returns every known place, sorted by province and then by name.
func Places() []Place {
	return append([]Place(nil), load().all...)
}

// PlacesIn returns a province's places in alphabetical order, or nil for an unknown
// province.
func PlacesIn(province string) []Place {
	return append([]Place(nil), load().byProvince[NormalizeProvince(province)]...)
}

// lessName orders place names alphabetically, ignoring case.
func lessName(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

const earthRadiusKM = 6371.0

// DistanceKM is the great-circle distance between two points.
func DistanceKM(lat1, lng1, lat2, lng2 float64) float64 {
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKM * math.Asin(math.Sqrt(a))
}

// BoundingBox returns the latitude and longitude ranges that contain every point within
// radiusKM of the centre, so a query can use an index before measuring exact distances.
func BoundingBox(lat, lng, radiusKM float64) (minLat, maxLat, minLng, maxLng float64) {
	dLat := radiusKM / 111.0
	// Near the poles a degree of longitude shrinks towards zero; clamp to avoid dividing by it.
	dLng := radiusKM / (111.0 * math.Max(math.Cos(lat*math.Pi/180), 0.01))
	return lat - dLat, lat + dLat, lng - dLng, lng + dLng
}

// plainLetter drops the accents used in South African place names, so "Daniëlskuil"
// and "Danielskuil" match.
var plainLetter = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

// key folds a name to plain lowercase letters only, so "Port Elizabeth", "port-elizabeth"
// and "PORT ELIZABETH" all match.
func key(s string) string {
	var b strings.Builder
	for _, r := range plainLetter.Replace(strings.ToLower(s)) {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
