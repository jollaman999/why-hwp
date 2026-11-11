package svgtool

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"regexp"
	"strings"
	"testing"

	"github.com/jollaman999/why-hwp/internal/svg2png"
)

func renderGray(t *testing.T, svg string) *image.Gray {
	t.Helper()
	scan, err := svg2png.ScanFonts([]byte(svg))
	if err != nil {
		t.Fatal(err)
	}
	res, err := svg2png.Render(context.Background(), svg2png.Options{
		SVG: []byte(svg), Fonts: scan.Fonts, Width: 800, Height: 160, DPI: 96,
	})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(res.PNG))
	if err != nil {
		t.Fatal(err)
	}
	g := image.NewGray(img.Bounds())
	for y := g.Rect.Min.Y; y < g.Rect.Max.Y; y++ {
		for x := g.Rect.Min.X; x < g.Rect.Max.X; x++ {
			g.Set(x, y, img.At(x, y))
		}
	}
	return g
}

// differing counts pixels whose gray levels differ by more than 16.
func differing(a, b *image.Gray) int {
	n := 0
	for i := range a.Pix {
		d := int(a.Pix[i]) - int(b.Pix[i])
		if d > 16 || d < -16 {
			n++
		}
	}
	return n
}

var base64Run = regexp.MustCompile(`base64,([^"]*)"`)

// TestEmbeddedFontRenders draws the same text without and with the
// embedded font with the engine: the embedded font must change the drawing,
// line continuations inside the base64 must not, and a damaged font must
// fall back to the drawing without it.
func TestEmbeddedFontRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("engine render skipped in -short mode")
	}
	plain := `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="80" viewBox="0 0 400 80"><rect width="400" height="80" fill="#ffffff"/>` +
		`<text x="10" y="55" font-size="40" font-family="NoSuchFont" fill="#000000">Hamburgefonstiv</text></svg>`
	embedded := string(embedWith(t, readFont(t), []byte(plain)))
	broken := base64Run.ReplaceAllStringFunc(embedded, func(m string) string {
		var out strings.Builder
		for i, r := range m {
			if i > len("base64,") && i%97 == 0 {
				out.WriteString("\\\n")
			}
			out.WriteRune(r)
		}
		return out.String()
	})
	if strings.Count(broken, "\\\n") < 10 {
		t.Fatalf("only %d line continuations inserted", strings.Count(broken, "\\\n"))
	}
	damaged := strings.Replace(embedded, "base64,AAEAAA", "base64,!!EAAA", 1)

	a := renderGray(t, plain)
	e := renderGray(t, embedded)
	c := renderGray(t, broken)
	d := renderGray(t, damaged)
	t.Logf("plain vs embedded %d px, embedded vs continued %d px, plain vs damaged %d px",
		differing(a, e), differing(e, c), differing(a, d))
	if differing(a, e) < 500 {
		t.Errorf("embedding the font changed only %d pixels; the engine did not use it", differing(a, e))
	}
	if n := differing(e, c); n != 0 {
		t.Errorf("line continuations in the base64 changed %d pixels", n)
	}
	if n := differing(a, d); n != 0 {
		t.Errorf("a damaged font changed %d pixels; want the fallback drawing", n)
	}
}
