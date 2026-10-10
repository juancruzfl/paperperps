// Package sharecard renders a trader's profile card as a PNG for link previews: when a
// /u/<handle> link is pasted into Discord, X, iMessage or Slack, their crawlers show this
// image. Crawlers don't run JavaScript or accept SVG, so it's drawn here, mirroring the
// card the browser draws (web/src/lib/shareCard.ts). Every card says PAPER TRADING.
package sharecard

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

const (
	W = 1200
	H = 630
)

var (
	bg      = rgb(0x0b0f14)
	surface = rgb(0x121821)
	border  = rgb(0x1f2833)
	text    = rgb(0xe6edf3)
	muted   = rgb(0x8b98a5)
	long    = rgb(0x3ddc97)
	short   = rgb(0xff5c7a)
	warn    = rgb(0xf5b942)
)

func rgb(v uint32) color.RGBA {
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}

var (
	boldFont   = mustParse(gobold.TTF)
	mediumFont = mustParse(gomedium.TTF)
)

func mustParse(ttf []byte) *opentype.Font {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	return f
}

type faceKey struct {
	f    *opentype.Font
	size float64
}

// faces caches font faces. Faces aren't safe for concurrent use, so they're only used
// under renderMu.
var faces = map[faceKey]font.Face{}

func face(f *opentype.Font, size float64) font.Face {
	k := faceKey{f, size}
	if fc, ok := faces[k]; ok {
		return fc
	}
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		panic(err)
	}
	faces[k] = fc
	return fc
}

// renderMu serializes rendering, which shares the cached faces and one rasterizer.
var (
	renderMu sync.Mutex
	raster   = vector.NewRasterizer(W, H)
)

// Profile is what a profile card shows.
type Profile struct {
	Handle  string
	Pnl     float64  // lifetime PnL in USD
	WinRate *float64 // nil before any closed trade
	Trades  int
	Rank    int    // all-time; 0 when unranked
	Link    string // e.g. paperperps.lol/u/brave-otter-412
}

// RenderProfile draws the card and encodes it as PNG.
func RenderProfile(p Profile) ([]byte, error) {
	renderMu.Lock()
	defer renderMu.Unlock()
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	up := p.Pnl >= 0
	tone := long
	if !up {
		tone = short
	}
	glow(img, tone)

	// Header: brand, and the paper-trading label.
	logo(img, 64, 56, 52)
	drawText(img, "PaperPerps", boldFont, 32, 132, 94, text)
	label := "PAPER TRADING · NOT REAL FUNDS"
	lf := face(boldFont, 20)
	lw := measure(lf, label) + 24
	pill(img, label, CARDRight-lw, 62, 20, bg, warn)

	trendLine(img, up, tone)

	handle := "@" + p.Handle
	drawText(img, handle, boldFont, fitSize(boldFont, handle, 52, textWidth), 64, 205, text)
	pnl := FormatUSD(p.Pnl, true)
	drawText(img, pnl, boldFont, fitSize(boldFont, pnl, 130, textWidth), 56, 340, tone)

	winRate := "—"
	if p.WinRate != nil {
		winRate = fmt.Sprintf("%.0f%%", *p.WinRate*100)
	}
	rank := "Unranked"
	if p.Rank > 0 {
		rank = "#" + strconv.Itoa(p.Rank)
	}
	for i, kv := range [][2]string{{"Win rate", winRate}, {"Trades", groupThousands(strconv.Itoa(p.Trades))}, {"All-time rank", rank}} {
		x := 64 + float64(i)*240
		drawText(img, kv[0], mediumFont, 24, x, 444, muted)
		drawText(img, kv[1], boldFont, 38, x, 490, text)
	}

	// Footer.
	fillRect(img, 64, H-100, W-128, 2, border)
	drawText(img, "Lifetime PnL on paper", mediumFont, 26, 64, H-46, muted)
	lf = face(mediumFont, 26)
	drawText(img, p.Link, mediumFont, 26, CARDRight-measure(lf, p.Link), H-46, text)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const (
	CARDRight = W - 64
	textWidth = 660 // room before the trend line
)

// --- drawing helpers ---

func measure(f font.Face, s string) float64 {
	return float64(font.MeasureString(f, s)) / 64
}

func fitSize(f *opentype.Font, s string, size, maxWidth float64) float64 {
	for size > 20 && measure(face(f, size), s) > maxWidth {
		size -= 4
	}
	return size
}

func drawText(img draw.Image, s string, f *opentype.Font, size, x, y float64, c color.Color) {
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face(f, size),
		Dot: fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}}
	d.DrawString(s)
}

func fillRect(img draw.Image, x, y, w, h int, c color.Color) {
	draw.Draw(img, image.Rect(x, y, x+w, y+h), image.NewUniform(c), image.Point{}, draw.Over)
}

// fill rasterizes a path and paints it with src. Overlapping subpaths must wind the
// same way, or they cancel out.
func fill(img draw.Image, src image.Image, path func(z *vector.Rasterizer)) {
	raster.Reset(W, H)
	path(raster)
	raster.Draw(img, img.Bounds(), src, image.Point{})
}

func roundRect(z *vector.Rasterizer, x, y, w, h, r float32) {
	z.MoveTo(x+r, y)
	z.LineTo(x+w-r, y)
	z.QuadTo(x+w, y, x+w, y+r)
	z.LineTo(x+w, y+h-r)
	z.QuadTo(x+w, y+h, x+w-r, y+h)
	z.LineTo(x+r, y+h)
	z.QuadTo(x, y+h, x, y+h-r)
	z.LineTo(x, y+r)
	z.QuadTo(x, y, x+r, y)
	z.ClosePath()
}

// circle winds the same way as stroke's segments, so they merge in one path.
func circle(z *vector.Rasterizer, cx, cy, r float32) {
	const n = 32
	for i := 0; i <= n; i++ {
		a := -float64(i) / n * 2 * math.Pi
		x, y := cx+r*float32(math.Cos(a)), cy+r*float32(math.Sin(a))
		if i == 0 {
			z.MoveTo(x, y)
		} else {
			z.LineTo(x, y)
		}
	}
	z.ClosePath()
}

// stroke draws a polyline with round joins and caps, as one path: a quad per segment and
// a disc per point.
func stroke(img draw.Image, pts [][2]float32, width float32, c color.Color) {
	half := width / 2
	fill(img, image.NewUniform(c), func(z *vector.Rasterizer) {
		for i := 0; i+1 < len(pts); i++ {
			a, b := pts[i], pts[i+1]
			dx, dy := b[0]-a[0], b[1]-a[1]
			l := float32(math.Hypot(float64(dx), float64(dy)))
			if l == 0 {
				continue
			}
			nx, ny := -dy/l*half, dx/l*half
			z.MoveTo(a[0]+nx, a[1]+ny)
			z.LineTo(b[0]+nx, b[1]+ny)
			z.LineTo(b[0]-nx, b[1]-ny)
			z.LineTo(a[0]-nx, a[1]-ny)
			z.ClosePath()
		}
		for _, p := range pts {
			circle(z, p[0], p[1], half)
		}
	})
}

func pill(img draw.Image, label string, x, y, size float64, fg, bgc color.Color) {
	f := face(boldFont, size)
	w := measure(f, label) + size*1.2
	h := size * 1.75
	fill(img, image.NewUniform(bgc), func(z *vector.Rasterizer) {
		roundRect(z, float32(x), float32(y), float32(w), float32(h), float32(h/2))
	})
	// Baseline that centers the cap height in the pill.
	drawText(img, label, boldFont, size, x+size*0.6, y+h/2+size*0.36, fg)
}

func logo(img draw.Image, x, y, size float32) {
	s := size / 32
	fill(img, image.NewUniform(surface), func(z *vector.Rasterizer) { roundRect(z, x, y, size, size, 7*s) })
	stroke(img, [][2]float32{{x + 7*s, y + 21*s}, {x + 13*s, y + 15*s}, {x + 17*s, y + 19*s}, {x + 25*s, y + 10*s}}, 3*s, long)
}

// glow lights the bottom-right corner in the PnL's color.
func glow(img *image.RGBA, tone color.RGBA) {
	cx, cy, radius := float64(W)*0.85, float64(H)*1.1, 700.0
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			a := 0.21 * (1 - d/radius)
			if a <= 0 {
				continue
			}
			blend(img, x, y, tone, a)
		}
	}
}

// fadeFill paints a path with c, fading from alpha at top to nothing at bottom.
func fadeFill(img *image.RGBA, c color.RGBA, top, bottom, alpha float64, path func(z *vector.Rasterizer)) {
	mask := image.NewAlpha(img.Bounds())
	raster.Reset(W, H)
	path(raster)
	raster.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})
	for y := max(0, int(top)); y < int(bottom) && y < H; y++ {
		a := alpha * (1 - (float64(y)-top)/(bottom-top))
		for x := 0; x < W; x++ {
			m := mask.Pix[mask.PixOffset(x, y)]
			if m == 0 {
				continue
			}
			blend(img, x, y, c, a*float64(m)/255)
		}
	}
}

// blend mixes c into the pixel at x, y with weight k.
func blend(img *image.RGBA, x, y int, c color.RGBA, k float64) {
	i := img.PixOffset(x, y)
	img.Pix[i] = uint8(float64(img.Pix[i])*(1-k) + float64(c.R)*k)
	img.Pix[i+1] = uint8(float64(img.Pix[i+1])*(1-k) + float64(c.G)*k)
	img.Pix[i+2] = uint8(float64(img.Pix[i+2])*(1-k) + float64(c.B)*k)
}

func trendLine(img *image.RGBA, up bool, tone color.RGBA) {
	steps := []float32{0, 0.18, 0.1, 0.34, 0.27, 0.5, 0.44, 0.68, 0.6, 0.86, 1}
	const x0, x1, top, bottom = 760, CARDRight, 170, 420
	pts := make([][2]float32, len(steps))
	for i, v := range steps {
		t := float32(i) / float32(len(steps)-1)
		y := bottom - v*(bottom-top)
		if !up {
			y = top + v*(bottom-top)
		}
		pts[i] = [2]float32{x0 + t*(x1-x0), y}
	}
	fadeFill(img, tone, top, bottom+40, 0.22, func(z *vector.Rasterizer) {
		z.MoveTo(pts[0][0], bottom+40)
		for _, p := range pts {
			z.LineTo(p[0], p[1])
		}
		z.LineTo(pts[len(pts)-1][0], bottom+40)
		z.ClosePath()
	})
	stroke(img, pts, 5, tone)
	last := pts[len(pts)-1]
	fill(img, image.NewUniform(tone), func(z *vector.Rasterizer) { circle(z, last[0], last[1], 9) })
}

// --- formatting ---

// FormatUSD formats like the web app: $1,234.56, -$1,234.56, and +$1,234.56 when signed.
func FormatUSD(v float64, signed bool) string {
	abs := strconv.FormatFloat(math.Abs(v), 'f', 2, 64)
	whole, frac, _ := strings.Cut(abs, ".")
	sign := ""
	switch {
	case abs == "0.00":
	case v < 0:
		sign = "-"
	case signed:
		sign = "+"
	}
	return sign + "$" + groupThousands(whole) + "." + frac
}

func groupThousands(digits string) string {
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
