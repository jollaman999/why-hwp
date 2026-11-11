package svg2png

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// availableBrowsers returns every default candidate found on this machine,
// one per browser name, or only $WHY_HWP_BROWSER when it is set.
func availableBrowsers(t *testing.T) []Browser {
	t.Helper()
	f := DefaultFinder(runtime.GOOS)
	if os.Getenv(EnvBrowser) != "" {
		b, err := f.Find("")
		if err != nil {
			t.Fatal(err)
		}
		return []Browser{b}
	}
	seen := map[string]bool{}
	var out []Browser
	for _, c := range Candidates(runtime.GOOS, os.Getenv) {
		if seen[c.Name] {
			continue
		}
		p, ok := f.locate(c.Path)
		if !ok {
			continue
		}
		seen[c.Name] = true
		out = append(out, Browser{Name: c.Name, Path: p, Kind: KindOf(p)})
	}
	return out
}

func TestRenderBoxes(t *testing.T) {
	if testing.Short() {
		t.Skip("browser render skipped in -short mode")
	}
	browsers := availableBrowsers(t)
	if len(browsers) == 0 {
		t.Skip("no browser installed")
	}
	svg, err := os.ReadFile(filepath.Join("testdata", "boxes.svg"))
	if err != nil {
		t.Fatal(err)
	}
	sw, sh, err := Size(svg)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range browsers {
		for _, mm := range []float64{40, 160} {
			w, h, err := TargetSize(sw, sh, mm, 0, 600)
			if err != nil {
				t.Fatal(err)
			}
			t.Run(fmt.Sprintf("%s/%gmm", b.Name, mm), func(t *testing.T) {
				res, err := Render(context.Background(), Options{Browser: b, SVG: svg, Width: w, Height: h, DPI: 600, TempDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("%s: screenshot %dx%d, output %dx%d", b.Path, res.RawWidth, res.RawHeight, res.Width, res.Height)
				if res.Width != w || res.Height != h {
					t.Fatalf("output %dx%d, want %dx%d", res.Width, res.Height, w, h)
				}
				img, err := png.Decode(bytes.NewReader(res.PNG))
				if err != nil {
					t.Fatal(err)
				}
				checkBoxes(t, img, w, h)
			})
		}
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

	// The Korean caption sits at x 60..110 with its baseline at y 30.
	dark, total := 0, 0
	for y := int(19 * sy); y < int(32*sy); y++ {
		for x := int(60 * sx); x < int(110*sx); x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			total++
			if r < 0x4000 && g < 0x4000 && b < 0x4000 {
				dark++
			}
		}
	}
	if frac := float64(dark) / float64(total); frac < 0.03 || frac > 0.6 {
		t.Errorf("caption area is %.1f%% dark, want glyph strokes", frac*100)
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
