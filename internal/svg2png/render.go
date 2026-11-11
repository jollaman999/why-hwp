package svg2png

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/draw"
	"image/png"
	"math"

	"github.com/jollaman999/why-hwp/internal/engine"
)

// Options describes one render.
type Options struct {
	SVG    []byte   // SVG document
	Fonts  [][]byte // the only fonts the renderer may use (see ScanFonts)
	Width  int      // output width in device pixels
	Height int      // output height in device pixels
	DPI    float64  // written to the PNG pHYs chunk
	Trim   bool     // cut white or transparent margins after rendering
}

// Result is a rendered image.
type Result struct {
	PNG    []byte
	Width  int
	Height int
}

// Render draws the SVG with the embedded engine (resvg) on a white page of
// exactly Width x Height pixels and returns the PNG.
func Render(ctx context.Context, o Options) (Result, error) {
	if o.Width < 1 || o.Height < 1 {
		return Result{}, errors.New("output size must be positive")
	}
	raw, err := engine.SVG2PNG(ctx, o.SVG, o.Fonts, o.Width, o.Height)
	if err != nil {
		return Result{}, err
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return Result{}, fmt.Errorf("decode engine output: %w", err)
	}
	b := img.Bounds()
	if b.Dx() != o.Width || b.Dy() != o.Height {
		return Result{}, fmt.Errorf("engine drew %dx%d, want %dx%d", b.Dx(), b.Dy(), o.Width, o.Height)
	}
	crop := b
	if o.Trim {
		crop = trimRect(img, crop)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	draw.Draw(dst, dst.Bounds(), img, crop.Min, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return Result{}, err
	}
	data := buf.Bytes()
	if o.DPI > 0 {
		data = withPHYs(data, o.DPI)
	}
	return Result{PNG: data, Width: crop.Dx(), Height: crop.Dy()}, nil
}

// trimRect shrinks r to exclude rows and columns at the edges that are all
// white or fully transparent.
func trimRect(img image.Image, r image.Rectangle) image.Rectangle {
	blank := func(x, y int) bool {
		cr, cg, cb, ca := img.At(x, y).RGBA()
		if ca == 0 {
			return true
		}
		const t = 0xfafa
		return cr >= t && cg >= t && cb >= t
	}
	rowBlank := func(y int) bool {
		for x := r.Min.X; x < r.Max.X; x++ {
			if !blank(x, y) {
				return false
			}
		}
		return true
	}
	colBlank := func(x int) bool {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			if !blank(x, y) {
				return false
			}
		}
		return true
	}
	for r.Min.Y < r.Max.Y && rowBlank(r.Min.Y) {
		r.Min.Y++
	}
	for r.Max.Y > r.Min.Y && rowBlank(r.Max.Y-1) {
		r.Max.Y--
	}
	for r.Min.X < r.Max.X && colBlank(r.Min.X) {
		r.Min.X++
	}
	for r.Max.X > r.Min.X && colBlank(r.Max.X-1) {
		r.Max.X--
	}
	if r.Empty() {
		return image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Min.Y+1)
	}
	return r
}

// withPHYs inserts a pHYs chunk after IHDR so the PNG carries its DPI.
func withPHYs(p []byte, dpi float64) []byte {
	const ihdrEnd = 8 + 4 + 4 + 13 + 4
	if len(p) < ihdrEnd {
		return p
	}
	ppm := uint32(math.Round(dpi / 0.0254))
	chunk := make([]byte, 4+4+9+4)
	binary.BigEndian.PutUint32(chunk[0:], 9)
	copy(chunk[4:], "pHYs")
	binary.BigEndian.PutUint32(chunk[8:], ppm)
	binary.BigEndian.PutUint32(chunk[12:], ppm)
	chunk[16] = 1 // unit: metre
	binary.BigEndian.PutUint32(chunk[17:], crc32.ChecksumIEEE(chunk[4:17]))
	out := make([]byte, 0, len(p)+len(chunk))
	out = append(out, p[:ihdrEnd]...)
	out = append(out, chunk...)
	return append(out, p[ihdrEnd:]...)
}
