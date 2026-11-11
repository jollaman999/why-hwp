package svg2png

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/jollaman999/why-hwp/internal/sfnt"
	"github.com/jollaman999/why-hwp/internal/svgtool"
)

const (
	fixtureFont = "../../testdata/fonts/sans-subset.ttf"
	fixtureSVG  = "../../testdata/svg/good.svg"
)

func render(t *testing.T, o Options) image.Image {
	t.Helper()
	res, err := Render(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != o.Width || res.Height != o.Height {
		t.Fatalf("output %dx%d, want %dx%d", res.Width, res.Height, o.Width, o.Height)
	}
	img, err := png.Decode(bytes.NewReader(res.PNG))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestRenderBoxes(t *testing.T) {
	if testing.Short() {
		t.Skip("engine render skipped in -short mode")
	}
	svg, err := os.ReadFile(filepath.Join("testdata", "boxes.svg"))
	if err != nil {
		t.Fatal(err)
	}
	sw, sh, err := Size(svg)
	if err != nil {
		t.Fatal(err)
	}
	for _, mm := range []float64{40, 160} {
		w, h, err := TargetSize(sw, sh, mm, 0, 600)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(fmt.Sprintf("%gmm", mm), func(t *testing.T) {
			checkBoxes(t, render(t, Options{SVG: svg, Width: w, Height: h, DPI: 600}), w, h)
		})
	}
}

// checkBoxes probes testdata/boxes.svg (viewBox 0 0 160 90) at known spots.
func checkBoxes(t *testing.T, img image.Image, w, h int) {
	t.Helper()
	sx, sy := float64(w)/160, float64(h)/90
	probes := []struct {
		name    string
		x, y    float64
		r, g, b uint8
	}{
		{"red box", 30, 25, 0xff, 0, 0},
		{"blue box", 130, 65, 0, 0, 0xff},
		{"green bottom band", 80, 88, 0, 0xff, 0},
		{"magenta right band", 157.5, 40, 0xff, 0, 0xff},
		{"white background", 80, 5, 0xff, 0xff, 0xff},
	}
	for _, p := range probes {
		assertColor(t, img, p.name, int(p.x*sx), int(p.y*sy), p.r, p.g, p.b)
	}
	// The last row and column must still be the bands: nothing was cut off
	// and no blank rows or columns were added. Rounding the height to whole
	// pixels leaves up to half a pixel of letterbox, so edge pixels may be
	// partly blended with the white page.
	assertColorTol(t, img, "last row", w/2, h-1, 0, 0xff, 0, 0x80)
	assertColorTol(t, img, "last column", w-1, h/4, 0xff, 0, 0xff, 0x80)

	// The SVG embeds no font, so its Korean caption (x 60..110, baseline
	// y 30) must not be drawn with a system font.
	if n := darkPixels(img, int(60*sx), int(19*sy), int(110*sx), int(32*sy)); n != 0 {
		t.Errorf("caption area has %d dark pixels; a system font was used", n)
	}
}

// darkPixels counts pixels in [x0,x1)x[y0,y1) with every channel below 0x40.
func darkPixels(img image.Image, x0, y0, x1, y1 int) int {
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r < 0x4000 && g < 0x4000 && b < 0x4000 {
				n++
			}
		}
	}
	return n
}

// embedFixtureFont does what why-hwp svgfont does, with the fixture font
// for both weights.
func embedFixtureFont(t *testing.T, svg []byte) []byte {
	t.Helper()
	data, err := os.ReadFile(fixtureFont)
	if err != nil {
		t.Fatal(err)
	}
	f, err := sfnt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	sets, err := svgtool.Collect(svg)
	if err != nil {
		t.Fatal(err)
	}
	var faces []svgtool.Face
	for _, w := range []svgtool.Weight{svgtool.Regular, svgtool.Bold} {
		if len(sets[w]) == 0 {
			continue
		}
		face, _, err := svgtool.SubsetFace(f, w, svgtool.DefaultFamily, sets[w])
		if err != nil {
			t.Fatal(err)
		}
		faces = append(faces, face)
	}
	out, err := svgtool.Embed(svg, svgtool.DefaultFamily, faces)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRenderEmbeddedFont draws testdata/svg/good.svg (viewBox 0 0 520 220)
// with and without its embedded font: the "Input file" label inside the
// first box must be drawn only when the font is passed.
func TestRenderEmbeddedFont(t *testing.T) {
	if testing.Short() {
		t.Skip("engine render skipped in -short mode")
	}
	plain, err := os.ReadFile(fixtureSVG)
	if err != nil {
		t.Fatal(err)
	}
	svg := embedFixtureFont(t, plain)
	scan, err := ScanFonts(svg)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Fonts) != 2 || len(scan.Skipped) != 0 {
		t.Fatalf("ScanFonts found %d fonts, skipped %q; want 2 and none", len(scan.Fonts), scan.Skipped)
	}
	const w, h = 1040, 440
	label := func(img image.Image) int { return darkPixels(img, 2*25, 2*45, 2*155, 2*85) }
	with := label(render(t, Options{SVG: svg, Fonts: scan.Fonts, Width: w, Height: h}))
	without := label(render(t, Options{SVG: svg, Width: w, Height: h}))
	t.Logf("label dark pixels: %d with the font, %d without", with, without)
	if with < 200 {
		t.Errorf("label has %d dark pixels with the embedded font; want glyph strokes", with)
	}
	if without != 0 {
		t.Errorf("label has %d dark pixels without fonts; want none", without)
	}
}

func assertColor(t *testing.T, img image.Image, name string, x, y int, r, g, b uint8) {
	t.Helper()
	assertColorTol(t, img, name, x, y, r, g, b, 8)
}

func assertColorTol(t *testing.T, img image.Image, name string, x, y int, r, g, b uint8, tol int) {
	t.Helper()
	cr, cg, cb, _ := img.At(x, y).RGBA()
	off := func(c uint32, want uint8) bool {
		d := int(c>>8) - int(want)
		return d > tol || d < -tol
	}
	if off(cr, r) || off(cg, g) || off(cb, b) {
		t.Errorf("%s at (%d,%d) = #%02x%02x%02x, want #%02x%02x%02x", name, x, y, cr>>8, cg>>8, cb>>8, r, g, b)
	}
}

func TestTrimRect(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 8))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.Pix[img.PixOffset(3, 2)] = 0
	img.Pix[img.PixOffset(6, 5)+2] = 0
	if got, want := trimRect(img, img.Bounds()), image.Rect(3, 2, 7, 6); got != want {
		t.Errorf("trimRect = %v, want %v", got, want)
	}
	blank := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if got := trimRect(blank, blank.Bounds()); got.Dx() != 1 || got.Dy() != 1 {
		t.Errorf("all-transparent trim = %v, want 1x1", got)
	}
}

func TestWithPHYs(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	p := withPHYs(buf.Bytes(), 600)
	// 600 dpi = 23622 pixels per metre = 0x5c46.
	if !bytes.Contains(p, []byte{'p', 'H', 'Y', 's', 0, 0, 0x5c, 0x46, 0, 0, 0x5c, 0x46, 1}) {
		t.Error("pHYs chunk missing or wrong")
	}
	img, err := png.Decode(bytes.NewReader(p))
	if err != nil || img.Bounds().Dx() != 3 {
		t.Errorf("decode after insert: %v", err)
	}
}
