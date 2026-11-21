// Package reports builds the business reports and exports them to CSV, Excel and PDF.
//
// Every report produces a Table; exporters only know about tables, so adding a report
// never requires touching the export code.
package reports

import (
	"fmt"
	"strconv"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/money"
)

// Column kinds control formatting and alignment.
const (
	KindText     = "text"
	KindInt      = "int"
	KindMoney    = "money"   // int64 cents
	KindPercent  = "percent" // float64 fraction, 0.25 = 25%
	KindDateTime = "datetime"
	KindDate     = "date"
)

// Column describes one report column.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// Table is a rendered report.
type Table struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Currency    string   `json:"currency"`
	Columns     []Column `json:"columns"`
	Rows        [][]any  `json:"rows"`
	Totals      []any    `json:"totals,omitempty"`
	GeneratedAt string   `json:"generated_at"`
	loc         *time.Location
}

// display formats a cell for text outputs (PDF and on-screen).
func (t *Table) display(col Column, v any) string {
	if v == nil {
		return ""
	}
	switch col.Kind {
	case KindMoney:
		return money.Format(toInt64(v), t.Currency)
	case KindPercent:
		return fmt.Sprintf("%.1f%%", toFloat(v)*100)
	case KindInt:
		return groupThousands(toInt64(v))
	case KindDateTime:
		if tm, ok := v.(time.Time); ok {
			return tm.In(t.loc).Format("2006-01-02 15:04")
		}
	case KindDate:
		if tm, ok := v.(time.Time); ok {
			return tm.Format("2006-01-02")
		}
	}
	return fmt.Sprint(v)
}

// plain formats a cell for CSV: numbers without symbols or grouping so spreadsheets parse them.
func (t *Table) plain(col Column, v any) string {
	if v == nil {
		return ""
	}
	switch col.Kind {
	case KindMoney:
		c := toInt64(v)
		sign := ""
		if c < 0 {
			sign, c = "-", -c
		}
		return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
	case KindPercent:
		return strconv.FormatFloat(toFloat(v)*100, 'f', 1, 64)
	case KindInt:
		return strconv.FormatInt(toInt64(v), 10)
	}
	return t.display(col, v)
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := n < 0
	if neg {
		s = s[1:]
	}
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func ratio(part, whole int64) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}
