// Package slug builds URL-friendly identifiers.
package slug

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/random"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// accents folds common accented Latin letters to ASCII so "Café" becomes "cafe".
var accents = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "æ", "ae",
	"ç", "c", "č", "c", "è", "e", "é", "e", "ê", "e", "ë", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "ñ", "n",
	"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o", "œ", "oe",
	"š", "s", "ß", "ss", "ù", "u", "ú", "u", "û", "u", "ü", "u",
	"ý", "y", "ÿ", "y", "ž", "z", "ł", "l", "&", " and ",
)

// Make converts s into a lower-case, hyphen-separated slug ("Café Jazz Night" → "cafe-jazz-night").
func Make(s string) string {
	out := nonAlnum.ReplaceAllString(accents.Replace(strings.ToLower(s)), "-")
	out = strings.Trim(out, "-")
	if len(out) > 80 {
		out = strings.Trim(out[:80], "-")
	}
	return out
}

// Unique returns base, or base with a short random suffix, such that no row in table has
// that value in column. table and column must be trusted identifiers, never user input.
func Unique(ctx context.Context, q database.Queryer, table, column, base string) (string, error) {
	if base == "" {
		base = strings.ToLower(random.Code(6))
	}
	candidate := base
	for attempt := 0; attempt < 5; attempt++ {
		var exists int
		err := q.QueryRowContext(ctx, fmt.Sprintf("SELECT 1 FROM %s WHERE %s = ? LIMIT 1", table, column), candidate).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = base + "-" + strings.ToLower(random.Code(4))
	}
	return "", fmt.Errorf("could not find a unique slug for %q", base)
}
