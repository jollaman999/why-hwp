package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// pageImage is a page drawn in 8-bit RGB, three bytes for each pixel.
type pageImage struct {
	W, H int
	Pix  []uint8
}

const (
	// inkPxPerPt is the resolution pages are drawn at for the picture
	// comparison, in pixels per point (288 dpi). The tolerance is counted
	// in whole pixels, so it is good to a quarter of a point.
	inkPxPerPt = 4
	// defaultInkLimit is the largest picture difference (see inkDiff)
	// that passes, in percent. README.md tells how it was chosen.
	defaultInkLimit = 1.0
)

const (
	// inkStep is the smallest step of a color channel that counts as an
	// outline: a quarter of the range from 0 to 255. It has to be larger
	// than what pdftoppm draws differently for two PDFs that show the
	// same thing: the seams between the tiles Hancom Office cuts a pie
	// slice into (a step of 1), the edge of a picture with a transparent
	// border (36), and the grain of a picture that one PDF holds as JPEG
	// and the other without loss.
	inkStep = 64
	// inkPartnerStep is the smallest step that may be the partner of an
	// outline on the other page. It is half of inkStep, so that an edge
	// that is a little above inkStep on one page and a little below on
	// the other still finds its partner.
	inkPartnerStep = inkStep / 2
)

// inkReach is how many pixels apart the two sides of a step may be. A
// sharp edge is smoothed over about two pixels when it is drawn.
const inkReach = 2

// renderPage draws one page (0-based) of a PDF with pdftoppm.
func renderPage(path string, page int) (*pageImage, error) {
	n := strconv.Itoa(page + 1)
	c := exec.Command("pdftoppm", "-r", strconv.Itoa(72*inkPxPerPt), "-f", n, "-l", n, path)
	var out, errb bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("pdftoppm %s: %w: %s", path, err, strings.TrimSpace(errb.String()))
	}
	return parsePPM(&out)
}

// parsePPM reads a binary PPM (P6) image with 8-bit samples.
func parsePPM(r io.Reader) (*pageImage, error) {
	br := bufio.NewReader(r)
	field := func() (string, error) {
		var b []byte
		for {
			c, err := br.ReadByte()
			if err != nil {
				return "", err
			}
			switch {
			case c == '#' && len(b) == 0:
				if _, err := br.ReadString('\n'); err != nil {
					return "", err
				}
			case c == ' ' || c == '\t' || c == '\n' || c == '\r':
				if len(b) > 0 {
					return string(b), nil
				}
			default:
				b = append(b, c)
			}
		}
	}
	magic, err := field()
	if err != nil || magic != "P6" {
		return nil, fmt.Errorf("pdftoppm output: not a binary PPM image")
	}
	var dim [3]int
	for i := range dim {
		s, err := field()
		if err != nil {
			return nil, fmt.Errorf("pdftoppm output: short PPM header")
		}
		if dim[i], err = strconv.Atoi(s); err != nil || dim[i] <= 0 {
			return nil, fmt.Errorf("pdftoppm output: bad PPM header field %q", s)
		}
	}
	if dim[2] != 255 {
		return nil, fmt.Errorf("pdftoppm output: PPM maximum %d, want 255", dim[2])
	}
	if dim[0] > 1<<15 || dim[1] > 1<<15 {
		return nil, fmt.Errorf("pdftoppm output: image %dx%d is too large", dim[0], dim[1])
	}
	g := &pageImage{W: dim[0], H: dim[1], Pix: make([]uint8, 3*dim[0]*dim[1])}
	if _, err := io.ReadFull(br, g.Pix); err != nil {
		return nil, fmt.Errorf("pdftoppm output: short PPM data: %w", err)
	}
	return g, nil
}

// outline marks the darker side of every edge of g. A pixel is marked
// when a pixel within inkReach pixels to the left, right, top or bottom
// differs from it by step or more in one color channel and is not darker
// (by the sum of its channels). Color is used and not gray because two
// pie slices can differ in hue and hardly in brightness.
func outline(g *pageImage, step int) []bool {
	m := make([]bool, g.W*g.H)
	sum := make([]uint16, g.W*g.H)
	for p := range sum {
		sum[p] = uint16(g.Pix[3*p]) + uint16(g.Pix[3*p+1]) + uint16(g.Pix[3*p+2])
	}
	edge := func(p, q int) bool {
		if sum[p] > sum[q] {
			return false
		}
		for c := 0; c < 3; c++ {
			if d := int(g.Pix[3*p+c]) - int(g.Pix[3*q+c]); d >= step || -d >= step {
				return true
			}
		}
		return false
	}
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			p := y*g.W + x
			if sum[p] == 3*255 {
				continue // white is never the darker side
			}
			for d := 1; d <= inkReach && !m[p]; d++ {
				m[p] = (x-d >= 0 && edge(p, p-d)) || (x+d < g.W && edge(p, p+d)) ||
					(y-d >= 0 && edge(p, p-d*g.W)) || (y+d < g.H && edge(p, p+d*g.W))
			}
		}
	}
	return m
}

// integral returns the summed-area table of a mask: t[(y+1)*(w+1)+x+1] is
// the number of set pixels in the rectangle from (0,0) to (x,y).
func integral(m []bool, w, h int) []int32 {
	t := make([]int32, (w+1)*(h+1))
	for y := 0; y < h; y++ {
		var row int32
		for x := 0; x < w; x++ {
			if m[y*w+x] {
				row++
			}
			t[(y+1)*(w+1)+x+1] = t[y*(w+1)+x+1] + row
		}
	}
	return t
}

// anyNear reports whether the mask behind t has a set pixel within r
// pixels of (x, y), horizontally and vertically.
func anyNear(t []int32, w, h, x, y, r int) bool {
	x0, y0, x1, y1 := max(x-r, 0), max(y-r, 0), min(x+r, w-1), min(y+r, h-1)
	if x0 > x1 || y0 > y1 {
		return false
	}
	s := w + 1
	return t[(y1+1)*s+x1+1]-t[y0*s+x1+1]-t[(y1+1)*s+x0]+t[y0*s+x0] > 0
}

// inkDiff measures how far the ink of two drawn pages is apart. It takes
// the outline pixels of both pages (see outline, with inkStep) and counts
// those with no outline pixel of the other page (with inkPartnerStep)
// within tol points, horizontally and vertically as for word edges, after
// the fit f, which maps a reference point onto the candidate page. The
// result is the larger of the two shares: reference outline with no
// candidate outline near it, and the other way round. 0 means every
// outline has a partner; 1 means none has. Two pages without ink give 0,
// and ink on one page only gives 1.
func inkDiff(ref, cand *pageImage, f affine, tol float64) float64 {
	r := int(math.Round(tol * inkPxPerPt))
	refLone, refAll := loneOutline(ref, cand, r, f.apply)
	candLone, candAll := loneOutline(cand, ref, r, func(x, y float64) (float64, float64) {
		return (x - f.TX) / f.S, (y - f.TY) / f.S
	})
	share := func(lone, all int) float64 {
		if all == 0 {
			return 0
		}
		return float64(lone) / float64(all)
	}
	return math.Max(share(refLone, refAll), share(candLone, candAll))
}

// loneOutline counts the outline pixels of a, and those of them that have
// no outline pixel of b within r pixels of where move puts them. move maps
// a point of a onto b, in points.
func loneOutline(a, b *pageImage, r int, move func(x, y float64) (float64, float64)) (lone, all int) {
	ao := outline(a, inkStep)
	bt := integral(outline(b, inkPartnerStep), b.W, b.H)
	for y := 0; y < a.H; y++ {
		for x := 0; x < a.W; x++ {
			if !ao[y*a.W+x] {
				continue
			}
			all++
			bx, by := move((float64(x)+0.5)/inkPxPerPt, (float64(y)+0.5)/inkPxPerPt)
			if !anyNear(bt, b.W, b.H, int(math.Floor(bx*inkPxPerPt)), int(math.Floor(by*inkPxPerPt)), r) {
				lone++
			}
		}
	}
	return lone, all
}

// measureInk draws the picture pages of a comparison, or every page with
// o.InkAll, and stores their picture difference. A picture page has no
// paired word and so no fit; its two pages are compared as they are. Of a
// sheet compared half by half, the other half is painted white.
func measureInk(refPath, candPath string, r *pdfResult, o cmpOptions) {
	var ref, cand *pageImage
	var err error
	drawn := -1
	for i := range r.Page {
		p := &r.Page[i]
		if !p.Picture && !o.InkAll {
			continue
		}
		sheet, side := i, 0
		if r.TwoUp {
			sheet, side = i/2, 1+i%2
		}
		if sheet != drawn {
			drawn = sheet
			if ref, err = renderPage(refPath, sheet); err == nil {
				cand, err = renderPage(candPath, sheet)
			}
		}
		if err != nil {
			p.InkErr = err.Error()
			continue
		}
		p.Ink, p.InkDone = inkDiff(halfImage(ref, side), halfImage(cand, side), p.Fit, o.Tol), true
	}
}
