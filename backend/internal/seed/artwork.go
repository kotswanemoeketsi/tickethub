package seed

import (
	"bytes"
	"hash/fnv"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand/v2"
)

// The demo events get generated artwork so the marketplace looks like it would with real
// event photography: stage lights for concerts, a spotlight for comedy, string lights for
// food markets, a night sky for the stargazing retreat. Each image is deterministic for
// its title, so re-seeding produces the same pictures.

const (
	artWidth  = 1440
	artHeight = 810
)

type rgb struct{ r, g, b float64 }

func hex(v uint32) rgb {
	return rgb{float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255}
}

type canvas struct {
	w, h int
	px   []rgb
}

func newCanvas(w, h int) *canvas { return &canvas{w: w, h: h, px: make([]rgb, w*h)} }

// gradient fills the canvas from top to bottom.
func (c *canvas) gradient(top, bottom rgb) {
	for y := 0; y < c.h; y++ {
		t := float64(y) / float64(c.h-1)
		col := rgb{top.r + (bottom.r-top.r)*t, top.g + (bottom.g-top.g)*t, top.b + (bottom.b-top.b)*t}
		for x := 0; x < c.w; x++ {
			c.px[y*c.w+x] = col
		}
	}
}

// glow adds light that fades smoothly from (cx, cy) out to radius.
func (c *canvas) glow(cx, cy, radius float64, col rgb, strength float64) {
	c.each(cx-radius, cy-radius, cx+radius, cy+radius, func(x, y int, p *rgb) {
		d := math.Hypot(float64(x)-cx, float64(y)-cy) / radius
		if d >= 1 {
			return
		}
		a := strength * math.Pow(1-d, 2.2)
		p.r += col.r * a
		p.g += col.g * a
		p.b += col.b * a
	})
}

// disc blends a soft-edged circle (a bokeh highlight) over the canvas.
func (c *canvas) disc(cx, cy, radius float64, col rgb, alpha float64) {
	c.each(cx-radius, cy-radius, cx+radius, cy+radius, func(x, y int, p *rgb) {
		d := math.Hypot(float64(x)-cx, float64(y)-cy)
		edge := math.Min(1, math.Max(0, (radius-d)/2.5))
		if edge <= 0 {
			return
		}
		// Bokeh is brighter at the rim, like out-of-focus lights through a lens.
		rim := 0.75 + 0.25*math.Pow(d/radius, 4)
		a := alpha * edge * rim
		p.r += col.r * a
		p.g += col.g * a
		p.b += col.b * a
	})
}

// beam adds a cone of light from (x0, y0) towards angle (radians), spreading by width.
func (c *canvas) beam(x0, y0, angle, length, spread float64, col rgb, strength float64) {
	dx, dy := math.Cos(angle), math.Sin(angle)
	ex, ey := x0+dx*length, y0+dy*length
	minX, maxX := math.Min(x0, ex)-length*spread, math.Max(x0, ex)+length*spread
	minY, maxY := math.Min(y0, ey)-length*spread, math.Max(y0, ey)+length*spread
	c.each(minX, minY, maxX, maxY, func(x, y int, p *rgb) {
		vx, vy := float64(x)-x0, float64(y)-y0
		along := vx*dx + vy*dy
		if along <= 0 || along > length {
			return
		}
		across := math.Abs(-vx*dy + vy*dx)
		width := along*spread + 4
		if across > width {
			return
		}
		a := strength * (1 - along/length) * math.Pow(1-across/width, 1.6)
		p.r += col.r * a
		p.g += col.g * a
		p.b += col.b * a
	})
}

// silhouette darkens everything below a ridge line given by height(x).
func (c *canvas) silhouette(height func(x float64) float64, col rgb) {
	for x := 0; x < c.w; x++ {
		top := int(height(float64(x)))
		for y := max(top, 0); y < c.h; y++ {
			c.px[y*c.w+x] = col
		}
	}
}

func (c *canvas) each(x0, y0, x1, y1 float64, f func(x, y int, p *rgb)) {
	minX, maxX := max(int(x0), 0), min(int(x1)+1, c.w)
	minY, maxY := max(int(y0), 0), min(int(y1)+1, c.h)
	for y := minY; y < maxY; y++ {
		for x := minX; x < maxX; x++ {
			f(x, y, &c.px[y*c.w+x])
		}
	}
}

// encode tone-maps the light buffer (so overlapping lights roll off instead of clipping),
// adds a vignette and fine grain, and returns a JPEG.
func (c *canvas) encode(rng *rand.Rand) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, c.w, c.h))
	cx, cy := float64(c.w)/2, float64(c.h)/2
	maxD := math.Hypot(cx, cy)
	tone := func(v float64) float64 { return 1 - math.Exp(-1.35*v) }
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			p := c.px[y*c.w+x]
			vig := 1 - 0.45*math.Pow(math.Hypot(float64(x)-cx, float64(y)-cy)/maxD, 2.2)
			grain := (rng.Float64() - 0.5) * 0.025
			conv := func(v float64) uint8 {
				return uint8(math.Max(0, math.Min(255, (tone(v)*vig+grain)*255)))
			}
			img.SetRGBA(x, y, color.RGBA{conv(p.r), conv(p.g), conv(p.b), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 84}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// eventArtwork paints a cover image in the style of the event's category.
func eventArtwork(category, title string) ([]byte, error) {
	h := fnv.New64a()
	h.Write([]byte(title))
	rng := rand.New(rand.NewPCG(h.Sum64(), 0x7469636b6574))
	c := newCanvas(artWidth, artHeight)
	w, ht := float64(artWidth), float64(artHeight)

	switch category {
	case "Music", "Festivals":
		palettes := [][3]uint32{{0x1a0b3d, 0xff3d8b, 0x5b8cff}, {0x081a3a, 0x00c2ff, 0xb05cff}, {0x2a0a14, 0xff8a00, 0xff2e63}}
		pal := palettes[rng.IntN(len(palettes))]
		c.gradient(hex(pal[0]), rgb{0.01, 0.01, 0.03})
		c.glow(w*0.5, ht*1.05, w*0.7, hex(pal[1]), 0.55)
		for i := 0; i < 7; i++ { // stage lights fanning up from the rig
			x := w * (0.12 + 0.76*float64(i)/6)
			ang := -math.Pi/2 + (rng.Float64()-0.5)*0.9
			col := hex(pal[1+i%2])
			c.beam(x, ht*1.02, ang, ht*1.15, 0.13+rng.Float64()*0.08, col, 0.65)
		}
		for i := 0; i < 26; i++ {
			r := 12 + rng.Float64()*46
			c.disc(rng.Float64()*w, rng.Float64()*ht*0.7, r, hex(pal[1+rng.IntN(2)]), 0.10+rng.Float64()*0.18)
		}
		// The crowd: heads and shoulders against the light, a few hands in the air.
		dark := rgb{0.012, 0.006, 0.02}
		fill := func(cx, cy, rx, ry float64) {
			c.each(cx-rx, cy-ry, cx+rx, cy+ry, func(x, y int, p *rgb) {
				if math.Pow((float64(x)-cx)/rx, 2)+math.Pow((float64(y)-cy)/ry, 2) <= 1 {
					*p = dark
				}
			})
		}
		for x := -20.0; x < w+40; x += 26 + rng.Float64()*20 {
			y := ht*0.86 + rng.Float64()*ht*0.06
			fill(x, y+56, 46, 40) // shoulders
			fill(x, y, 17, 21)    // head
			if rng.Float64() < 0.12 {
				arm := 70 + rng.Float64()*50
				lean := (rng.Float64() - 0.5) * 30
				for i := 0.0; i <= 1; i += 0.02 {
					fill(x+18+lean*i, y+10-arm*i, 6, 6)
				}
				fill(x+18+lean, y+10-arm, 10, 12) // hand
			}
		}
		c.each(0, ht*0.93, w, ht, func(_, _ int, p *rgb) { *p = dark })

	case "Comedy":
		c.gradient(hex(0x3b0716), hex(0x0d0206))
		for x := 0; x < artWidth; x++ { // folds of a stage curtain
			shade := 0.55 + 0.45*math.Pow(math.Abs(math.Sin(float64(x)/38)), 0.7)
			for y := 0; y < artHeight; y++ {
				p := &c.px[y*artWidth+x]
				p.r, p.g, p.b = p.r*shade, p.g*shade, p.b*shade
			}
		}
		c.beam(w*0.5, -ht*0.1, math.Pi/2, ht*1.1, 0.16, hex(0xfff1c9), 0.9)
		c.glow(w*0.5, ht*0.82, w*0.22, hex(0xffe3a3), 0.8)
		// Microphone stand.
		c.each(w*0.5-3, ht*0.48, w*0.5+3, ht*0.84, func(_, _ int, p *rgb) { *p = rgb{0.02, 0.02, 0.02} })
		c.disc(w*0.5, ht*0.47, 15, rgb{-1, -1, -1}, 1)

	case "Theatre":
		c.gradient(hex(0x0b1640), hex(0x05060f))
		// A proscenium: velvet drapes framing a lit stage.
		for x := 0; x < artWidth; x++ {
			fx := float64(x)
			edge := math.Min(fx, w-fx) / (w * 0.22)
			if edge >= 1 {
				continue
			}
			shade := (0.45 + 0.55*math.Pow(math.Abs(math.Sin(fx/22)), 0.6)) * (1 - edge*0.35)
			for y := 0; y < artHeight; y++ {
				c.px[y*artWidth+x] = rgb{0.42 * shade, 0.05 * shade, 0.09 * shade}
			}
		}
		c.each(0, 0, w, ht*0.12, func(_, _ int, p *rgb) { *p = rgb{0.36, 0.04, 0.08} }) // valance
		c.glow(w*0.5, ht*1.0, w*0.45, hex(0xffc46b), 0.75)                              // footlights
		c.beam(w*0.32, -10, math.Pi/2-0.18, ht*1.05, 0.10, hex(0xcfe3ff), 0.45)
		c.beam(w*0.68, -10, math.Pi/2+0.18, ht*1.05, 0.10, hex(0xcfe3ff), 0.45)
		c.glow(w*0.5, ht*0.72, w*0.16, hex(0xfff0d6), 0.45)

	case "Food & Drink":
		c.gradient(hex(0x0f1b2d), hex(0x1d0f08))
		c.glow(w*0.3, ht*0.9, w*0.5, hex(0xff9d3c), 0.45)
		for row := 0; row < 3; row++ { // strings of festoon lights
			sag, y0 := 60+rng.Float64()*50, ht*(0.12+0.16*float64(row))
			for i := 0; i <= 22; i++ {
				t := float64(i) / 22
				x := w * t
				y := y0 + sag*4*t*(1-t)
				c.glow(x, y, 70, hex(0xffb347), 0.35)
				c.disc(x, y, 7, hex(0xfff2d6), 0.9)
			}
		}
		for i := 0; i < 40; i++ {
			c.disc(rng.Float64()*w, ht*0.55+rng.Float64()*ht*0.45, 10+rng.Float64()*40, hex(0xffa64d), 0.06+rng.Float64()*0.12)
		}

	case "Business & Tech":
		c.gradient(hex(0x061a2e), hex(0x02070f))
		c.glow(w*0.78, ht*0.25, w*0.55, hex(0x2f7bff), 0.6)
		c.glow(w*0.2, ht*0.95, w*0.45, hex(0x00d1b2), 0.35)
		for y := 40.0; y < ht; y += 40 { // a field of points, like a network map
			for x := 40.0; x < w; x += 40 {
				d := math.Hypot(x-w*0.78, y-ht*0.25) / w
				c.disc(x, y, 2.2, hex(0x9cc4ff), math.Max(0.08, 0.7-d))
			}
		}
		for i := 0; i < 9; i++ {
			c.beam(w*0.78, ht*0.25, rng.Float64()*2*math.Pi, w*0.6, 0.004, hex(0x7fb2ff), 0.35)
		}

	case "Sport":
		c.gradient(hex(0x05301c), hex(0x020c07))
		c.glow(w*0.5, ht*0.2, w*0.8, hex(0xd6ffe6), 0.35)
		for i := 0; i < 4; i++ {
			c.beam(w*(0.1+0.27*float64(i)), -20, math.Pi/2+(0.5-float64(i)/3)*0.5, ht*1.2, 0.12, hex(0xf4fff8), 0.35)
		}
		for y := 0; y < artHeight; y++ { // mowing stripes on the pitch, wider towards the camera
			fy := float64(y)
			if fy < ht*0.55 {
				continue
			}
			band := int(math.Log(fy-ht*0.5) * 7)
			if band%2 == 0 {
				c.each(0, fy, w, fy, func(_, _ int, p *rgb) { p.g *= 1.18; p.r *= 1.05 })
			}
		}

	default: // Arts & Culture and anything else: a desert night sky
		c.gradient(hex(0x050a24), hex(0x2a1640))
		c.glow(w*0.55, ht*0.95, w*0.6, hex(0xff7a59), 0.35)
		for i := 0; i < 1400; i++ {
			c.disc(rng.Float64()*w, rng.Float64()*ht*0.8, 0.6+rng.Float64()*1.6, rgb{1, 1, 1}, 0.25+rng.Float64()*0.75)
		}
		for i := 0; i < 260; i++ { // the Milky Way, a dense diagonal band
			t := rng.Float64()
			x, y := w*t, ht*(0.75-0.6*t)+rng.NormFloat64()*45
			c.glow(x, y, 26+rng.Float64()*40, hex(0xb9a6ff), 0.05)
		}
		c.silhouette(func(x float64) float64 {
			return ht*0.82 + 26*math.Sin(x/210) + 12*math.Sin(x/57) - 40*math.Exp(-math.Pow((x-w*0.7)/90, 2))
		}, rgb{0.01, 0.01, 0.02})
	}
	return c.encode(rng)
}
