package httpx

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLargeListCompressesWhenAccepted(t *testing.T) {
	items := []string{"Durban", "Gqeberha"}
	decode := func(body io.Reader) []string {
		var out struct{ Data []string }
		if err := json.NewDecoder(body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.Data
	}

	for _, c := range []struct {
		accept string
		gzip   bool
	}{{"gzip, deflate, br", true}, {"br;q=1.0, gzip;q=0.8", true}, {"gzip;q=0", false}, {"", false}} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", c.accept)
		w := httptest.NewRecorder()
		if err := LargeList(w, r, items); err != nil {
			t.Fatal(err)
		}
		var body io.Reader = w.Body
		if got := w.Header().Get("Content-Encoding") == "gzip"; got != c.gzip {
			t.Fatalf("Accept-Encoding %q: gzip = %v", c.accept, got)
		}
		if c.gzip {
			zr, err := gzip.NewReader(w.Body)
			if err != nil {
				t.Fatal(err)
			}
			body = zr
		}
		if got := decode(body); len(got) != 2 || got[1] != "Gqeberha" {
			t.Fatalf("Accept-Encoding %q: %v", c.accept, got)
		}
	}
}
