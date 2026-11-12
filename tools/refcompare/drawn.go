package main

import (
	"bytes"
	"fmt"
	"maps"
	"math"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Hancom Office draws the text of a chart as outlines, and a chart that it
// holds as a picture has no text at all, so the reference has no word where
// the candidate has one. Such a candidate word has no partner. It is not
// counted as unmatched when the reference shows the same shape at the same
// place: the two PDFs are drawn around the word and the outlines compared
// (see drawnShift). A word the candidate made up, or printed somewhere
// else, has no such shape behind it and stays unmatched.

const (
	// drawnPxPerPt is the resolution the surroundings of a word are
	// drawn at, in pixels per point (576 dpi). Chart labels are small:
	// at inkPxPerPt a digit of a 3.5 pt label is 8 by 11 pixels, and
	// labels that differ in one digit could hardly be told apart.
	// README.md tells how this and drawnSlack were chosen.
	drawnPxPerPt = 8
	// drawnSlack is how far, in pixels, an outline pixel may be from the
	// outline of the other PDF once the two shapes are laid on each
	// other: a quarter of a point. The same glyph is not drawn alike by
	// the two producers (Hancom Office writes it as a path, in another
	// size, and the candidate as a glyph of a font).
	drawnSlack = 2
	// drawnBand is the tallest strip of a page, in points, that is drawn
	// at once.
	drawnBand = 100.0
)

// loneWord is a candidate word without a partner.
type loneWord struct {
	word
	// Shared tells that another word is at the same place: a candidate
	// word (see hasTwin) or a reference word without a partner (see
	// samePlace). The ink there is that word's, so the word is never
	// taken for drawn text.
	Shared bool
}

// hasTwin reports whether word i of d has another word of the page (those
// are the indexes in page) at the same place: the boxes overlap sideways
// and the vertical middles are less than half the height of the shorter
// box apart, as for samePlace.
func hasTwin(d *pdfWords, i int, page []int) bool {
	w := d.Words[i]
	for _, j := range page {
		v := d.Words[j]
		if j == i || math.Min(w.X1, v.X1)-math.Max(w.X0, v.X0) <= 0 {
			continue
		}
		if math.Abs(w.ymid()-v.ymid()) < math.Min(w.Y1-w.Y0, v.Y1-v.Y0)/2 {
			return true
		}
	}
	return false
}

// region is a part of a page drawn at drawnPxPerPt pixels per point. Its
// first pixel is pixel (X, Y) of the page; what lies outside it is white.
type region struct {
	pageImage
	X, Y int
}

// renderRegion draws the part of a page (0-based) inside the box, which is
// in points, with pdftoppm. size is the size of the page.
func renderRegion(path string, page int, size pageSize, x0, y0, x1, y1 float64) (*region, error) {
	const k = drawnPxPerPt
	// pdftoppm draws the whole page when the corner of the box is not on
	// the page, and cuts a box that sticks out of it.
	x0, y0 = math.Max(x0, 0), math.Max(y0, 0)
	x1, y1 = math.Min(x1, size.W), math.Min(y1, size.H)
	px, py := int(math.Floor(x0*k)), int(math.Floor(y0*k))
	w, h := int(math.Ceil(x1*k))-px, int(math.Ceil(y1*k))-py
	if x1-x0 < 1 || y1-y0 < 1 {
		return &region{X: px, Y: py}, nil
	}
	n := strconv.Itoa(page + 1)
	c := exec.Command("pdftoppm", "-r", strconv.Itoa(72*k), "-f", n, "-l", n,
		"-x", strconv.Itoa(px), "-y", strconv.Itoa(py), "-W", strconv.Itoa(w), "-H", strconv.Itoa(h), path)
	var out, errb bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("pdftoppm %s: %w: %s", path, err, strings.TrimSpace(errb.String()))
	}
	g, err := parsePPM(&out)
	if err != nil {
		return nil, err
	}
	if g.W > w || g.H > h {
		return nil, fmt.Errorf("pdftoppm %s: asked for %dx%d pixels of page %d, got %dx%d", path, w, h, page+1, g.W, g.H)
	}
	return &region{pageImage: *g, X: px, Y: py}, nil
}

// outlines holds the outlines of a part of a region: the outline that is
// measured (inkStep) and, as a summed-area table, the outline that may be
// its partner (inkPartnerStep). Its first pixel is pixel (X, Y) of the page.
type outlines struct {
	X, Y, W, H int
	Ink        []bool
	Partner    []int32
}

// outlines returns the outlines of g inside the box, which is in points.
func (g *region) outlines(x0, y0, x1, y1 float64) outlines {
	const k = drawnPxPerPt
	px, py := int(math.Floor(x0*k)), int(math.Floor(y0*k))
	w, h := max(int(math.Ceil(x1*k))-px, 0), max(int(math.Ceil(y1*k))-py, 0)
	sub := pageImage{W: w, H: h, Pix: bytes.Repeat([]byte{255}, 3*w*h)}
	for y := max(py, g.Y); y < min(py+h, g.Y+g.H); y++ {
		a, b := max(px, g.X), min(px+w, g.X+g.W)
		if a < b {
			copy(sub.Pix[3*((y-py)*w+a-px):], g.Pix[3*((y-g.Y)*g.W+a-g.X):3*((y-g.Y)*g.W+b-g.X)])
		}
	}
	return outlines{X: px, Y: py, W: w, H: h, Ink: outline(&sub, inkStep), Partner: integral(outline(&sub, inkPartnerStep), w, h)}
}

// ink returns the middles of the measured outline pixels, in points.
func (o outlines) ink() []point {
	var out []point
	for y := 0; y < o.H; y++ {
		for x := 0; x < o.W; x++ {
			if o.Ink[y*o.W+x] {
				out = append(out, point{(float64(o.X+x) + 0.5) / drawnPxPerPt, (float64(o.Y+y) + 0.5) / drawnPxPerPt})
			}
		}
	}
	return out
}

// near reports whether a partner outline pixel is within drawnSlack pixels
// of the point (x, y), which is in points.
func (o outlines) near(x, y float64) bool {
	return anyNear(o.Partner, o.W, o.H, int(math.Floor(x*drawnPxPerPt))-o.X, int(math.Floor(y*drawnPxPerPt))-o.Y, drawnSlack)
}

// drawnShift looks for the shape of the candidate word w in the reference.
// f is the fit of the page and maps a reference point onto the candidate
// page; ref and cand are the two pages drawn around the word.
//
// The reference shape may be up to tol points from where the fit puts it,
// horizontally and vertically, as a word edge may. For every such shift,
// in steps of a pixel and the smallest first, the outline pixels inside
// the word box are taken on both pages, and those of them counted that
// have no outline pixel of the other page within drawnSlack pixels. The
// two shapes lie on each other when there is outline on both pages and
// at most the share limit of either is alone.
//
// The word is drawn in the reference when the shapes lie on each other
// at one shift and at none of the four shifts that are tol and twice
// drawnSlack further to the left, right, top or bottom. Where they do,
// the box is so full of ink (the middle of a radar chart, a hatched
// area) that any shape lies on it, and nothing is known about the word.
// drawnShift returns the shift, the larger of its horizontal and vertical
// part, in points.
//
// A box that is empty in the reference, a shape that is further away than
// tol, and another shape at the place all leave the word as it was.
func drawnShift(ref, cand *region, w word, f affine, tol, limit float64) (float64, bool) {
	if f.S <= 0 {
		return 0, false
	}
	const k = drawnPxPerPt
	inv := func(x, y float64) (float64, float64) { return (x - f.TX) / f.S, (y - f.TY) / f.S }
	// Outlines are found inkReach pixels deep and partners are looked
	// for drawnSlack pixels away.
	m := float64(drawnSlack+inkReach+1) / k
	co := cand.outlines(w.X0-m, w.Y0-m, w.X1+m, w.Y1+m)
	var cp []point
	for _, p := range co.ink() {
		if p.X >= w.X0 && p.X < w.X1 && p.Y >= w.Y0 && p.Y < w.Y1 {
			cp = append(cp, p)
		}
	}
	if len(cp) == 0 {
		return 0, false
	}
	reach := int(math.Round(tol * k))
	away := reach + 2*drawnSlack
	far := float64(reach+away) / k
	rx0, ry0 := inv(w.X0-far, w.Y0-far)
	rx1, ry1 := inv(w.X1+far, w.Y1+far)
	ro := ref.outlines(rx0-m, ry0-m, rx1+m, ry1+m)
	rp := ro.ink()
	if len(rp) == 0 {
		return 0, false
	}
	// on reports whether the shapes lie on each other when the reference
	// is moved by sx, sy pixels.
	on := func(sx, sy int) bool {
		fx, fy := float64(sx)/k, float64(sy)/k
		lone, most := 0, limit*float64(len(cp))
		for _, p := range cp {
			if x, y := inv(p.X-fx, p.Y-fy); !ro.near(x, y) {
				if lone++; float64(lone) > most {
					return false
				}
			}
		}
		all, lone := 0, 0
		for _, p := range rp {
			x, y := f.apply(p.X, p.Y)
			x, y = x+fx, y+fy
			if x < w.X0 || x >= w.X1 || y < w.Y0 || y >= w.Y1 {
				continue
			}
			all++
			if !co.near(x, y) {
				lone++
			}
		}
		return all > 0 && float64(lone) <= limit*float64(all)
	}
	for _, s := range shifts(reach) {
		if !on(s[0], s[1]) {
			continue
		}
		if on(s[0]-away, s[1]) || on(s[0]+away, s[1]) || on(s[0], s[1]-away) || on(s[0], s[1]+away) {
			return 0, false
		}
		return float64(max(s[0], -s[0], s[1], -s[1])) / k, true
	}
	return 0, false
}

// shifts returns every shift of up to reach pixels to either side,
// horizontally and vertically, the smallest first.
func shifts(reach int) [][2]int {
	var out [][2]int
	for y := -reach; y <= reach; y++ {
		for x := -reach; x <= reach; x++ {
			out = append(out, [2]int{x, y})
		}
	}
	abs := func(v int) int { return max(v, -v) }
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ma, mb := max(abs(a[0]), abs(a[1])), max(abs(b[0]), abs(b[1])); ma != mb {
			return ma < mb
		}
		return a[0]*a[0]+a[1]*a[1] < b[0]*b[0]+b[1]*b[1]
	})
	return out
}

// measureDrawn tests the candidate words without a partner (see
// drawnShift, with the fit of the word's page, o.Tol and o.InkLimit) and
// takes those that the reference draws out of the unmatched words of r.
// ref and cand give the sizes of the sheets. A word on a page that only
// the candidate has, and a word that shares its place, are not tested.
func measureDrawn(refPath, candPath string, ref, cand *pdfWords, r *pdfResult, o cmpOptions) {
	byPage := map[int][]int{}
	for i, w := range r.CandOnlyWords {
		if !w.Shared && w.Page < len(r.Page) {
			byPage[w.Page] = append(byPage[w.Page], i)
		}
	}
	if len(byPage) == 0 {
		return
	}
	drawn := make([]bool, len(r.CandOnlyWords))
	for _, page := range slices.Sorted(maps.Keys(byPage)) {
		p := &r.Page[page]
		refSheet, _, candSheet, _ := r.sheetOf(page)
		if p.Fit.S <= 0 || refSheet >= len(ref.Pages) || candSheet >= len(cand.Pages) {
			continue
		}
		idx := byPage[page]
		sort.SliceStable(idx, func(a, b int) bool { return r.CandOnlyWords[idx[a]].Y0 < r.CandOnlyWords[idx[b]].Y0 })
		for len(idx) > 0 {
			// A strip of the page: the words that end within
			// drawnBand of the top of the first one.
			first := r.CandOnlyWords[idx[0]]
			x0, y0, x1, y1 := first.X0, first.Y0, first.X1, first.Y1
			n := 1
			for ; n < len(idx); n++ {
				w := r.CandOnlyWords[idx[n]]
				if w.Y1 > y0+drawnBand {
					break
				}
				x0, x1, y1 = math.Min(x0, w.X0), math.Max(x1, w.X1), math.Max(y1, w.Y1)
			}
			// drawnShift looks at the reference up to twice the
			// tolerance and a point around a word.
			pad := 2*o.Tol + 2
			cg, err := renderRegion(candPath, candSheet, cand.Pages[candSheet], x0-pad, y0-pad, x1+pad, y1+pad)
			var rg *region
			if err == nil {
				rx0, ry0 := (x0-pad-p.Fit.TX)/p.Fit.S, (y0-pad-p.Fit.TY)/p.Fit.S
				rx1, ry1 := (x1+pad-p.Fit.TX)/p.Fit.S, (y1+pad-p.Fit.TY)/p.Fit.S
				rg, err = renderRegion(refPath, refSheet, ref.Pages[refSheet], rx0-1, ry0-1, rx1+1, ry1+1)
			}
			if err != nil {
				p.DrawnErr = err.Error()
				break
			}
			for _, i := range idx[:n] {
				if s, ok := drawnShift(rg, cg, r.CandOnlyWords[i].word, p.Fit, o.Tol, o.InkLimit); ok {
					drawn[i] = true
					p.Drawn++
					p.CandOnly--
					p.DrawnShift = math.Max(p.DrawnShift, s)
				}
			}
			idx = idx[n:]
		}
	}
	var left []loneWord
	r.CandOnlySample = nil
	for i, w := range r.CandOnlyWords {
		if drawn[i] {
			r.DrawnWords++
			r.CandOnly--
			continue
		}
		left = append(left, w)
		if len(r.CandOnlySample) < sampleWords {
			r.CandOnlySample = append(r.CandOnlySample, located{w.Text, w.Page + 1})
		}
	}
	r.CandOnlyWords = left
}
