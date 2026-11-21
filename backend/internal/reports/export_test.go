package reports

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"
)

func sampleTable() *Table {
	return &Table{
		Name: "sample", Title: "Sample report", Description: "Test", Currency: "ZAR", loc: time.UTC, GeneratedAt: "now",
		Columns: []Column{{"name", "Name", KindText}, {"qty", "Qty", KindInt}, {"amount", "Amount", KindMoney},
			{"share", "Share", KindPercent}, {"at", "At", KindDateTime}},
		Rows: [][]any{
			{"=HYPERLINK(\"http://evil\")", int64(1200), int64(123456), 0.25, time.Date(2026, 3, 1, 18, 30, 0, 0, time.UTC)},
			{"Café & <Bar>", int64(3), int64(-500), 0.0, nil},
		},
		Totals: []any{"Total", int64(1203), int64(122956), nil, nil},
	}
}

func TestCSVExportNeutralisesFormulas(t *testing.T) {
	out, err := sampleTable().ToCSV()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.HasPrefix(text, "\xEF\xBB\xBF") {
		t.Fatal("CSV should start with a UTF-8 byte-order mark")
	}
	if !strings.Contains(text, `"'=HYPERLINK(""http://evil"")"`) {
		t.Fatalf("formula was not neutralised:\n%s", text)
	}
	if !strings.Contains(text, "1234.56") || !strings.Contains(text, "-5.00") || !strings.Contains(text, "25.0") {
		t.Fatalf("numbers not formatted for spreadsheets:\n%s", text)
	}
}

func TestXLSXExportIsWellFormed(t *testing.T) {
	out, err := sampleTable().ToXLSX()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"[Content_Types].xml": false, "_rels/.rels": false, "xl/workbook.xml": false,
		"xl/_rels/workbook.xml.rels": false, "xl/styles.xml": false, "xl/worksheets/sheet1.xml": false}
	for _, f := range zr.File {
		if _, ok := want[f.Name]; !ok {
			continue
		}
		want[f.Name] = true
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		dec := xml.NewDecoder(bytes.NewReader(body))
		for {
			if _, err := dec.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s is not well-formed XML: %v", f.Name, err)
			}
		}
		if f.Name == "xl/worksheets/sheet1.xml" && !strings.Contains(string(body), "Café &amp; &lt;Bar&gt;") {
			t.Fatal("text was not XML-escaped")
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing %s", name)
		}
	}
}

func TestPDFExport(t *testing.T) {
	out, err := sampleTable().ToPDF("GotATicket")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
}

func TestCellRefAndSerial(t *testing.T) {
	cases := map[[2]int]string{{0, 1}: "A1", {25, 3}: "Z3", {26, 4}: "AA4", {701, 2}: "ZZ2", {702, 9}: "AAA9"}
	for in, want := range cases {
		if got := cellRef(in[0], in[1]); got != want {
			t.Errorf("cellRef(%d,%d) = %s, want %s", in[0], in[1], got, want)
		}
	}
	if got := excelSerial(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)); got != 46023.5 {
		t.Errorf("excelSerial = %v, want 46023.5", got)
	}
}
