package seed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArtworkRenders(t *testing.T) {
	dir := os.Getenv("ARTWORK_PREVIEW_DIR")
	for _, cat := range []string{"Music", "Festivals", "Comedy", "Theatre", "Food & Drink", "Business & Tech", "Sport", "Arts & Culture"} {
		data, err := eventArtwork(cat, "Preview "+cat)
		if err != nil || len(data) < 10_000 {
			t.Fatalf("%s: %d bytes, %v", cat, len(data), err)
		}
		if dir != "" {
			_ = os.WriteFile(filepath.Join(dir, cat+".jpg"), data, 0o644)
		}
	}
}
