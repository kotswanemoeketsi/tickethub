package reports

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-pdf/fpdf"
)

// ToCSV renders the table as UTF-8 CSV with a byte-order mark so Excel detects the encoding.
func (t *Table) ToCSV() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 byte-order mark
	w := csv.NewWriter(&buf)
	header := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = c.Label
	}
	if err := w.Write(header); err != nil {
		return nil, err
	}
	writeRow := func(row []any) error {
		record := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			if i < len(row) {
				record[i] = sanitizeCSV(t.plain(c, row[i]))
			}
		}
		return w.Write(record)
	}
	for _, row := range t.Rows {
		if err := writeRow(row); err != nil {
			return nil, err
		}
	}
	if t.Totals != nil {
		if err := writeRow(t.Totals); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// sanitizeCSV neutralises values that spreadsheet software would execute as formulas
// (CSV injection), e.g. a customer named "=HYPERLINK(...)".
func sanitizeCSV(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		if _, err := fmt.Sscanf(v, "%g", new(float64)); err == nil {
			return v // a plain negative number is fine
		}
		return "'" + v
	}
	return v
}

// ---- Excel (XLSX) -----------------------------------------------------------------------

// Cell style indexes defined in xlsxStyles.
const (
	styleDefault = iota
	styleHeader
	styleMoney
	styleDateTime
	stylePercent
	styleInt
	styleTitle
	styleTotalMoney
	styleTotalInt
	styleTotalText
	styleDate
)

const xlsxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<numFmts count="4"><numFmt numFmtId="164" formatCode="#,##0.00"/><numFmt numFmtId="165" formatCode="yyyy-mm-dd hh:mm"/><numFmt numFmtId="166" formatCode="0.0%"/><numFmt numFmtId="167" formatCode="yyyy-mm-dd"/></numFmts>
<fonts count="3"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="14"/><name val="Calibri"/></font></fonts>
<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FFEEF2FF"/><bgColor indexed="64"/></patternFill></fill></fills>
<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>
<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>
<cellXfs count="11">
<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>
<xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/>
<xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>
<xf numFmtId="165" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>
<xf numFmtId="166" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>
<xf numFmtId="3" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>
<xf numFmtId="0" fontId="2" fillId="0" borderId="0" xfId="0" applyFont="1"/>
<xf numFmtId="164" fontId="1" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1"/>
<xf numFmtId="3" fontId="1" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1"/>
<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>
<xf numFmtId="167" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>
</cellXfs>
<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>
</styleSheet>`

const xlsxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
</Types>`

const xlsxRootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`

const xlsxWorkbookRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`

const xlsxWorkbook = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="Report" sheetId="1" r:id="rId1"/></sheets>
</workbook>`

// ToXLSX renders the table as an Excel workbook with typed cells: amounts and dates stay
// numeric so they can be summed, sorted and filtered.
func (t *Table) ToXLSX() ([]byte, error) {
	var sheet bytes.Buffer
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	sheet.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	const headerRow = 4
	sheet.WriteString(fmt.Sprintf(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="%d" topLeftCell="A%d" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`, headerRow, headerRow+1))
	sheet.WriteString(`<cols>`)
	for i, c := range t.Columns {
		width := max(12, min(50, utf8.RuneCountInString(c.Label)+4))
		if c.Kind == KindText {
			width = max(width, 24)
		}
		sheet.WriteString(fmt.Sprintf(`<col min="%d" max="%d" width="%d" customWidth="1"/>`, i+1, i+1, width))
	}
	sheet.WriteString(`</cols><sheetData>`)

	writeText := func(ref, text string, style int) {
		sheet.WriteString(fmt.Sprintf(`<c r="%s" t="inlineStr" s="%d"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, xmlEscape(text)))
	}
	sheet.WriteString(`<row r="1">`)
	writeText("A1", t.Title, styleTitle)
	sheet.WriteString(`</row><row r="2">`)
	writeText("A2", t.Description, styleDefault)
	sheet.WriteString(`</row>`)

	sheet.WriteString(fmt.Sprintf(`<row r="%d">`, headerRow))
	for i, c := range t.Columns {
		writeText(cellRef(i, headerRow), c.Label, styleHeader)
	}
	sheet.WriteString(`</row>`)

	writeRow := func(rowNum int, row []any, totals bool) {
		sheet.WriteString(fmt.Sprintf(`<row r="%d">`, rowNum))
		for i, c := range t.Columns {
			if i >= len(row) || row[i] == nil {
				continue
			}
			ref := cellRef(i, rowNum)
			v := row[i]
			switch c.Kind {
			case KindMoney:
				style := styleMoney
				if totals {
					style = styleTotalMoney
				}
				sheet.WriteString(fmt.Sprintf(`<c r="%s" s="%d"><v>%s</v></c>`, ref, style, t.plain(c, v)))
			case KindInt:
				style := styleInt
				if totals {
					style = styleTotalInt
				}
				sheet.WriteString(fmt.Sprintf(`<c r="%s" s="%d"><v>%d</v></c>`, ref, style, toInt64(v)))
			case KindPercent:
				sheet.WriteString(fmt.Sprintf(`<c r="%s" s="%d"><v>%g</v></c>`, ref, stylePercent, toFloat(v)))
			case KindDateTime, KindDate:
				tm, ok := v.(time.Time)
				if !ok {
					continue
				}
				style, wall := styleDateTime, tm.In(t.loc)
				if c.Kind == KindDate {
					style, wall = styleDate, tm
				}
				sheet.WriteString(fmt.Sprintf(`<c r="%s" s="%d"><v>%s</v></c>`, ref, style, strconv.FormatFloat(excelSerial(wall), 'f', -1, 64)))
			default:
				style := styleDefault
				if totals {
					style = styleTotalText
				}
				writeText(ref, t.display(c, v), style)
			}
		}
		sheet.WriteString(`</row>`)
	}
	rowNum := headerRow + 1
	for _, row := range t.Rows {
		writeRow(rowNum, row, false)
		rowNum++
	}
	if t.Totals != nil {
		writeRow(rowNum, t.Totals, true)
	}
	sheet.WriteString(`</sheetData></worksheet>`)

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	files := []struct{ name, body string }{
		{"[Content_Types].xml", xlsxContentTypes},
		{"_rels/.rels", xlsxRootRels},
		{"xl/workbook.xml", xlsxWorkbook},
		{"xl/_rels/workbook.xml.rels", xlsxWorkbookRels},
		{"xl/styles.xml", xlsxStyles},
		{"xl/worksheets/sheet1.xml", sheet.String()},
	}
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// cellRef converts a zero-based column and one-based row into "A1" notation.
func cellRef(col, row int) string {
	name := ""
	for col >= 0 {
		name = string(rune('A'+col%26)) + name
		col = col/26 - 1
	}
	return fmt.Sprintf("%s%d", name, row)
}

// excelSerial converts a wall-clock time into Excel's day-number representation, at
// whole-second precision.
func excelSerial(t time.Time) float64 {
	wall := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	seconds := int64(wall.Sub(epoch) / time.Second)
	return float64(seconds/86400) + float64(seconds%86400)/86400
}

func xmlEscape(s string) string {
	var b strings.Builder
	// Drop characters that are not allowed in XML 1.0 at all.
	clean := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r != 0xFFFE && r != 0xFFFF) {
			return r
		}
		return -1
	}, s)
	_ = xml.EscapeText(&b, []byte(clean))
	return b.String()
}

// ---- PDF ------------------------------------------------------------------------------

// ToPDF renders the table as a landscape A4 document with a repeated header row.
func (t *Table) ToPDF(appName string) ([]byte, error) {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.SetTitle(t.Title, true)
	pdf.SetCreator(appName, true)
	pdf.SetMargins(10, 10, 10)
	pdf.SetAutoPageBreak(false, 10)
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pageW, pageH := pdf.GetPageSize()
	usable := pageW - 20
	const rowH = 6.5

	// Size columns from their content, then scale to the page width.
	pdf.SetFont("Helvetica", "", 8.5)
	widths := make([]float64, len(t.Columns))
	for i, c := range t.Columns {
		pdf.SetFont("Helvetica", "B", 8.5)
		w := pdf.GetStringWidth(tr(c.Label)) + 4
		pdf.SetFont("Helvetica", "", 8.5)
		for r, row := range t.Rows {
			if r > 300 {
				break
			}
			if i < len(row) {
				w = max(w, pdf.GetStringWidth(tr(t.display(c, row[i])))+4)
			}
		}
		widths[i] = min(w, 70)
	}
	total := 0.0
	for _, w := range widths {
		total += w
	}
	for i := range widths {
		widths[i] = widths[i] * usable / total
	}

	header := func() {
		pdf.SetFillColor(238, 242, 255)
		pdf.SetTextColor(55, 48, 163)
		pdf.SetFont("Helvetica", "B", 8.5)
		for i, c := range t.Columns {
			pdf.CellFormat(widths[i], rowH, fit(pdf, tr(c.Label), widths[i]), "", 0, align(c), true, 0, "")
		}
		pdf.Ln(rowH)
		pdf.SetTextColor(24, 24, 27)
	}
	footer := func() {
		pdf.SetY(pageH - 8)
		pdf.SetFont("Helvetica", "", 7.5)
		pdf.SetTextColor(113, 113, 122)
		pdf.CellFormat(usable/2, 4, tr(appName+" · "+t.Title), "", 0, "L", false, 0, "")
		pdf.CellFormat(usable/2, 4, fmt.Sprintf("Page %d", pdf.PageNo()), "", 0, "R", false, 0, "")
		pdf.SetTextColor(24, 24, 27)
	}

	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 15)
	pdf.CellFormat(usable, 8, tr(t.Title), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(82, 82, 91)
	pdf.MultiCell(usable, 4.5, tr(t.Description+" · Generated "+t.GeneratedAt), "", "L", false)
	pdf.Ln(2)
	header()

	drawRow := func(row []any, bold bool, shade bool) {
		if pdf.GetY()+rowH > pageH-12 {
			footer()
			pdf.AddPage()
			header()
		}
		style := ""
		if bold {
			style = "B"
		}
		pdf.SetFont("Helvetica", style, 8.5)
		if shade {
			pdf.SetFillColor(250, 250, 250)
		}
		for i, c := range t.Columns {
			text := ""
			if i < len(row) {
				text = t.display(c, row[i])
			}
			pdf.CellFormat(widths[i], rowH, fit(pdf, tr(text), widths[i]), "", 0, align(c), shade, 0, "")
		}
		pdf.Ln(rowH)
	}
	for i, row := range t.Rows {
		drawRow(row, false, i%2 == 1)
	}
	if len(t.Rows) == 0 {
		pdf.SetFont("Helvetica", "I", 9)
		pdf.CellFormat(usable, rowH*2, "No data for the selected filters.", "", 1, "C", false, 0, "")
	}
	if t.Totals != nil {
		pdf.SetDrawColor(212, 212, 216)
		pdf.Line(10, pdf.GetY(), 10+usable, pdf.GetY())
		drawRow(t.Totals, true, false)
	}
	footer()

	if err := pdf.Error(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func align(c Column) string {
	switch c.Kind {
	case KindMoney, KindInt, KindPercent:
		return "R"
	}
	return "L"
}

// fit truncates text with an ellipsis so it fits within width.
func fit(pdf *fpdf.Fpdf, text string, width float64) string {
	limit := width - 2
	if pdf.GetStringWidth(text) <= limit {
		return text
	}
	ellipsis := "\x85" // "…" in cp1252
	for len(text) > 0 && pdf.GetStringWidth(text+ellipsis) > limit {
		text = text[:len(text)-1]
	}
	return text + ellipsis
}
