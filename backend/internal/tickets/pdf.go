package tickets

import (
	"bytes"
	"fmt"

	"github.com/go-pdf/fpdf"
	"github.com/skip2/go-qrcode"
)

// PrintableTicket is everything printed on one ticket.
type PrintableTicket struct {
	EventTitle      string
	DateText        string // e.g. "Sat 5 Dec 2026, 18:00 – 23:00 (SAST)"
	VenueName       string
	VenueAddress    string
	TicketTypeName  string
	HolderName      string
	Code            string
	OrderReference  string
	PriceText       string
	IsComplimentary bool
	QRPayload       string
}

// brand colour (indigo-600)
const brandR, brandG, brandB = 79, 70, 229

// RenderPDF renders one A4 page per ticket. The QR code is drawn as vector squares so it
// stays sharp at any zoom level and prints cleanly.
func RenderPDF(appName string, items []PrintableTicket) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetTitle(appName+" tickets", true)
	pdf.SetCreator(appName, true)
	pdf.SetAutoPageBreak(false, 0)
	tr := pdf.UnicodeTranslatorFromDescriptor("") // UTF-8 → cp1252 for the core fonts

	for _, t := range items {
		pdf.AddPage()
		const left, top, width = 15.0, 15.0, 180.0

		// Header band
		pdf.SetFillColor(brandR, brandG, brandB)
		pdf.RoundedRect(left, top, width, 18, 3, "12", "F")
		pdf.SetTextColor(255, 255, 255)
		pdf.SetFont("Helvetica", "B", 15)
		pdf.SetXY(left+6, top+4)
		pdf.CellFormat(100, 10, tr(appName), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 11)
		label := "E-TICKET"
		if t.IsComplimentary {
			label = "COMPLIMENTARY TICKET"
		}
		pdf.SetXY(left+width-86, top+4)
		pdf.CellFormat(80, 10, label, "", 0, "R", false, 0, "")

		// Card body
		pdf.SetDrawColor(228, 228, 231)
		pdf.SetLineWidth(0.3)
		pdf.RoundedRect(left, top+18, width, 94, 3, "34", "D")

		pdf.SetTextColor(24, 24, 27)
		pdf.SetFont("Helvetica", "B", 17)
		pdf.SetXY(left+6, top+25)
		pdf.MultiCell(110, 7.5, tr(t.EventTitle), "", "L", false)

		y := pdf.GetY() + 2
		pdf.SetFont("Helvetica", "", 10.5)
		pdf.SetTextColor(63, 63, 70)
		pdf.SetXY(left+6, y)
		pdf.MultiCell(110, 5.5, tr(t.DateText), "", "L", false)
		pdf.SetXY(left+6, pdf.GetY()+1)
		pdf.SetFont("Helvetica", "B", 10.5)
		pdf.MultiCell(110, 5.5, tr(t.VenueName), "", "L", false)
		if t.VenueAddress != "" {
			pdf.SetFont("Helvetica", "", 9)
			pdf.SetTextColor(113, 113, 122)
			pdf.SetXY(left+6, pdf.GetY())
			pdf.MultiCell(110, 4.8, tr(t.VenueAddress), "", "L", false)
		}

		// Details
		rows := [][2]string{
			{"Ticket", t.TicketTypeName},
			{"Attendee", t.HolderName},
			{"Order", t.OrderReference},
			{"Price", t.PriceText},
		}
		y = max(pdf.GetY()+5, top+70)
		for _, row := range rows {
			pdf.SetXY(left+6, y)
			pdf.SetFont("Helvetica", "", 9)
			pdf.SetTextColor(113, 113, 122)
			pdf.CellFormat(26, 6, row[0], "", 0, "L", false, 0, "")
			pdf.SetFont("Helvetica", "B", 10.5)
			pdf.SetTextColor(24, 24, 27)
			pdf.CellFormat(84, 6, tr(row[1]), "", 0, "L", false, 0, "")
			y += 7.5
		}

		// QR code with code underneath
		const qrSize = 62.0
		qrX, qrY := left+width-qrSize-8, top+26.0
		if err := drawQR(pdf, t.QRPayload, qrX, qrY, qrSize); err != nil {
			return nil, err
		}
		pdf.SetFont("Courier", "B", 13)
		pdf.SetTextColor(24, 24, 27)
		pdf.SetXY(qrX, qrY+qrSize+2)
		pdf.CellFormat(qrSize, 6, FormatCode(t.Code), "", 0, "C", false, 0, "")
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(113, 113, 122)
		pdf.SetXY(qrX, qrY+qrSize+8)
		pdf.CellFormat(qrSize, 5, "Scan at the entrance", "", 0, "C", false, 0, "")

		// Perforation and notes
		pdf.SetDashPattern([]float64{1.5, 1.5}, 0)
		pdf.Line(left, top+122, left+width, top+122)
		pdf.SetDashPattern([]float64{}, 0)
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(24, 24, 27)
		pdf.SetXY(left, top+128)
		pdf.CellFormat(width, 6, "Before you go", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 9.5)
		pdf.SetTextColor(63, 63, 70)
		pdf.SetX(left)
		pdf.MultiCell(width, 5.2, tr("• Show this QR code on your phone or printed. Each ticket admits one person and can only be scanned once.\n"+
			"• Keep your ticket private: anyone holding a copy of the QR code can use it before you do.\n"+
			"• If the QR code cannot be scanned, staff can enter the ticket code printed under it."), "", "L", false)
	}
	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("render ticket PDF: %w", err)
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("write ticket PDF: %w", err)
	}
	return buf.Bytes(), nil
}

// drawQR draws payload as a QR code, merging horizontal runs of dark modules into single
// rectangles to keep the PDF small.
func drawQR(pdf *fpdf.Fpdf, payload string, x, y, size float64) error {
	qr, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return fmt.Errorf("encode QR: %w", err)
	}
	bitmap := qr.Bitmap()
	module := size / float64(len(bitmap))
	pdf.SetFillColor(255, 255, 255)
	pdf.Rect(x, y, size, size, "F")
	pdf.SetFillColor(0, 0, 0)
	for row, cells := range bitmap {
		for col := 0; col < len(cells); {
			if !cells[col] {
				col++
				continue
			}
			start := col
			for col < len(cells) && cells[col] {
				col++
			}
			// A hair of overlap avoids visible seams between modules in some viewers.
			pdf.Rect(x+float64(start)*module, y+float64(row)*module, float64(col-start)*module+0.02, module+0.02, "F")
		}
	}
	return nil
}

// QRPNG renders payload as a PNG image, used for inline email images.
func QRPNG(payload string, size int) ([]byte, error) {
	return qrcode.Encode(payload, qrcode.Medium, size)
}
