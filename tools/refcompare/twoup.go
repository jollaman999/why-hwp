package main

import (
	"math"
	"strconv"
)

// Hancom Office follows the print settings of a document, and one of them
// prints two pages side by side on a landscape sheet. When the two PDFs do
// not shrink the pages by the same amount (Hancom Office shrinks every page
// once more to make room for memo balloons), each half of a sheet is scaled
// about its own corner and no single fit of the sheet holds both halves.
// Such a document is compared half by half: a half is a page of the
// document, so it gets what every page gets, one fit of its own.

const (
	// twoUpGap is how far (in points) the fits of the two halves must be
	// from what one fit of the whole sheet would give before the halves
	// are compared apart. It is 1 % of half an A4 sheet, the same share
	// as memoScale. Below it one fit serves both halves to within a few
	// points and the sheet is compared as one page, which is the
	// stricter test.
	twoUpGap = 4.0
	// twoUpSlack is how far (in points) the fits of the two halves may be
	// from what printing two pages to a sheet gives. It is half of
	// twoUpGap, so a sheet whose halves are not scaled can never be
	// taken for two pages, however its halves are moved.
	twoUpSlack = 2.0
	// halfScaleDiff is the largest relative difference between the fit
	// scales of the two halves of a sheet that is not reported. The two
	// halves are pages of one document and should have one scale. The
	// pages of a document that is not printed two to a sheet differ by
	// up to 0.33 % in the 17 such documents this was checked with.
	halfScaleDiff = 0.005
)

// sideBySide reports whether d may be printed two pages to a sheet: every
// page is landscape, no word lies across the vertical centre line of its
// page, and there are words on both sides of it.
func sideBySide(d *pdfWords) bool {
	if len(d.Pages) == 0 {
		return false
	}
	for _, p := range d.Pages {
		if p.W <= p.H {
			return false
		}
	}
	left, right := false, false
	for _, w := range d.Words {
		mid := d.Pages[w.Page].W / 2
		switch {
		case w.X1 <= mid:
			left = true
		case w.X0 >= mid:
			right = true
		default:
			return false
		}
	}
	return left && right
}

// halves returns d with every page cut in two at its vertical centre line:
// pages 2n and 2n+1 of the result are the left and the right half of page
// n. Words keep their order and their positions on the sheet, so the fit of
// a right half holds the distance between the two halves.
func halves(d *pdfWords) *pdfWords {
	out := &pdfWords{TwoUp: true, Base: d.Base, Words: make([]word, len(d.Words))}
	for _, p := range d.Pages {
		out.Pages = append(out.Pages, pageSize{p.W / 2, p.H}, pageSize{p.W / 2, p.H})
	}
	for i, w := range d.Words {
		if w.X1 <= d.Pages[w.Page].W/2 {
			w.Page = 2 * w.Page
		} else {
			w.Page = 2*w.Page + 1
		}
		out.Words[i] = w
	}
	return out
}

// fittedByHalves reports whether r, a comparison of the halves of ref and
// cand with the scale fit, shows two pages to a sheet. Printing two pages
// to a sheet puts the top left corner of the right page half a sheet to the
// right of that of the left page, whatever the pages are scaled by. A sheet
// says so when
//
//   - both of its halves have a fit with a scale,
//   - the two fits put the top left corners of the two halves half a
//     candidate sheet apart, to within twoUpSlack, and
//   - that is more than twoUpGap away from where one fit of the sheet with
//     the mean of the two scales would put them.
//
// The document is taken as two pages to a sheet when more than half of the
// sheets with two fitted halves say so. Both distances are the same when
// the scale is 1, so the two conditions hold together only when the halves
// are scaled and their corners stay in place. A sheet with two columns is
// not taken for two pages, however far the candidate moves its columns
// apart.
func fittedByHalves(r pdfResult, ref, cand *pdfWords) bool {
	sheets, votes := 0, 0
	for i := 0; i+1 < len(r.Page); i += 2 {
		l, rt := r.Page[i], r.Page[i+1]
		if l.FitEdges < minScalePoints || rt.FitEdges < minScalePoints {
			continue
		}
		sheets++
		rs, cs := ref.Pages[i/2], cand.Pages[i/2]
		lx, ly := l.Fit.apply(0, 0)
		rx, ry := rt.Fit.apply(rs.W/2, 0)
		dx, dy := rx-lx, math.Abs(ry-ly)
		oneFit := (l.Fit.S + rt.Fit.S) / 2 * rs.W / 2
		if math.Max(math.Abs(dx-oneFit), dy) > twoUpGap && math.Max(math.Abs(dx-cs.W/2), dy) <= twoUpSlack {
			votes++
		}
	}
	return votes > 0 && 2*votes > sheets
}

// compareSheets compares ref and cand like comparePDF. When both are laid
// out side by side (see sideBySide) and their halves are fitted apart (see
// fittedByHalves), each half of a sheet is compared as a page: the result
// has TwoUp set and one pageStat for each half. Every other document is
// compared exactly as comparePDF does.
func compareSheets(ref, cand *pdfWords, o cmpOptions) pdfResult {
	if sideBySide(ref) && sideBySide(cand) {
		hr, hc := halves(ref), halves(cand)
		probe := o
		probe.Fit = fitScale
		r := comparePDF(hr, hc, probe)
		if fittedByHalves(r, ref, cand) {
			if o.Fit != fitScale {
				r = comparePDF(hr, hc, o)
			}
			return r
		}
	}
	return comparePDF(ref, cand, o)
}

// pageLabel names page (0-based) of a comparison: its number, or with twoUp
// the number of its sheet and L or R for the half.
func pageLabel(twoUp bool, page int) string {
	if !twoUp {
		return strconv.Itoa(page + 1)
	}
	return strconv.Itoa(page/2+1) + "LR"[page%2:page%2+1]
}

// halfScales tells the fit scales of the halves of a comparison with
// TwoUp. Halves without a scale fit are left out.
type halfScales struct {
	LeftMin, LeftMax   float64
	RightMin, RightMax float64
	Left, Right        int // halves with a scale fit
	// Differ lists the sheets (1-based) whose two scales differ by more
	// than halfScaleDiff.
	Differ []sheetScales
}

// sheetScales is the pair of fit scales of one sheet.
type sheetScales struct {
	Sheet                 int // 1-based
	Left, Right           float64
	LeftEdges, RightEdges int
}

func (r pdfResult) halfScales() halfScales {
	var h halfScales
	fitted := func(p pageStat) bool { return p.FitEdges >= minScalePoints }
	add := func(lo, hi *float64, n *int, s float64) {
		if *n == 0 || s < *lo {
			*lo = s
		}
		if *n == 0 || s > *hi {
			*hi = s
		}
		*n++
	}
	for i, p := range r.Page {
		if !fitted(p) {
			continue
		}
		if i%2 == 0 {
			add(&h.LeftMin, &h.LeftMax, &h.Left, p.Fit.S)
		} else {
			add(&h.RightMin, &h.RightMax, &h.Right, p.Fit.S)
		}
	}
	for i := 0; i+1 < len(r.Page); i += 2 {
		l, rt := r.Page[i], r.Page[i+1]
		if !fitted(l) || !fitted(rt) {
			continue
		}
		if math.Abs(l.Fit.S-rt.Fit.S) > halfScaleDiff*(l.Fit.S+rt.Fit.S)/2 {
			h.Differ = append(h.Differ, sheetScales{i/2 + 1, l.Fit.S, rt.Fit.S, l.FitEdges, rt.FitEdges})
		}
	}
	return h
}

// halfImage returns g with the other half painted white: side 1 keeps the
// left half, side 2 the right half, and 0 gives g itself. Positions stay
// those of the sheet.
func halfImage(g *pageImage, side int) *pageImage {
	if side == 0 {
		return g
	}
	out := &pageImage{W: g.W, H: g.H, Pix: append([]uint8(nil), g.Pix...)}
	x0, x1 := g.W/2, g.W
	if side == 2 {
		x0, x1 = 0, g.W/2
	}
	for y := 0; y < g.H; y++ {
		row := out.Pix[3*(y*g.W+x0) : 3*(y*g.W+x1)]
		for k := range row {
			row[k] = 255
		}
	}
	return out
}
