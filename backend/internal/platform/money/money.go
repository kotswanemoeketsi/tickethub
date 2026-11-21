// Package money formats integer minor-unit amounts for people.
package money

import (
	"fmt"
	"strconv"
	"strings"
)

var symbols = map[string]string{"ZAR": "R", "USD": "$", "EUR": "€", "GBP": "£", "BWP": "P", "NAD": "N$"}

// Format renders cents as e.g. "R 1,250.00" (or "KES 1,250.00" for currencies without a
// known symbol). The same convention is used by the web app so amounts read identically
// in the browser, on PDFs and in emails.
func Format(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	whole := strconv.FormatInt(cents/100, 10)
	var grouped strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(r)
	}
	prefix := symbols[strings.ToUpper(currency)]
	if prefix == "" {
		prefix = strings.ToUpper(currency)
	}
	return fmt.Sprintf("%s%s %s.%02d", sign, prefix, grouped.String(), cents%100)
}
