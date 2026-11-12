package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// lcsPairs returns the index pairs (i, j) of a longest common subsequence of
// a and b, in increasing order. It uses Hirschberg's method, so memory stays
// linear in the input length.
func lcsPairs(a, b []string) [][2]int {
	ids := map[string]int32{}
	intern := func(s []string) []int32 {
		out := make([]int32, len(s))
		for i, x := range s {
			id, ok := ids[x]
			if !ok {
				id = int32(len(ids))
				ids[x] = id
			}
			out[i] = id
		}
		return out
	}
	ai, bi := intern(a), intern(b)
	var out [][2]int
	hirschberg(ai, bi, 0, len(ai), 0, len(bi), &out)
	return out
}

func hirschberg(a, b []int32, a0, a1, b0, b1 int, out *[][2]int) {
	for a0 < a1 && b0 < b1 && a[a0] == b[b0] {
		*out = append(*out, [2]int{a0, b0})
		a0++
		b0++
	}
	var suffix [][2]int
	for a0 < a1 && b0 < b1 && a[a1-1] == b[b1-1] {
		a1--
		b1--
		suffix = append(suffix, [2]int{a1, b1})
	}
	defer func() {
		for i := len(suffix) - 1; i >= 0; i-- {
			*out = append(*out, suffix[i])
		}
	}()
	if a0 == a1 || b0 == b1 {
		return
	}
	if a1-a0 == 1 {
		for j := b0; j < b1; j++ {
			if b[j] == a[a0] {
				*out = append(*out, [2]int{a0, j})
				return
			}
		}
		return
	}
	mid := (a0 + a1) / 2
	fwd := lcsRow(a[a0:mid], b[b0:b1], false)
	bwd := lcsRow(a[mid:a1], b[b0:b1], true)
	n := b1 - b0
	best, bestK := -1, 0
	for k := 0; k <= n; k++ {
		if v := fwd[k] + bwd[n-k]; v > best {
			best, bestK = v, k
		}
	}
	hirschberg(a, b, a0, mid, b0, b0+bestK, out)
	hirschberg(a, b, mid, a1, b0+bestK, b1, out)
}

// lcsRow returns L where L[k] is the LCS length of a and the first k items
// of b. With rev, both are read backwards, so L[k] is the LCS length of a
// and the last k items of b.
func lcsRow(a, b []int32, rev bool) []int {
	n := len(b)
	prev := make([]int, n+1)
	cur := make([]int, n+1)
	for ii := range a {
		x := a[ii]
		if rev {
			x = a[len(a)-1-ii]
		}
		cur[0] = 0
		for jj := 0; jj < n; jj++ {
			y := b[jj]
			if rev {
				y = b[n-1-jj]
			}
			switch {
			case x == y:
				cur[jj+1] = prev[jj] + 1
			case prev[jj+1] >= cur[jj]:
				cur[jj+1] = prev[jj+1]
			default:
				cur[jj+1] = cur[jj]
			}
		}
		prev, cur = cur, prev
	}
	return prev
}

// Fit modes: how a reference page may be moved onto the candidate page
// before word positions are compared.
const (
	fitNone  = "none"  // positions are compared as they are
	fitShift = "shift" // translation only
	fitScale = "scale" // uniform scale and translation
)

// affine maps a reference point onto the candidate page: (S*x+TX, S*y+TY).
type affine struct{ S, TX, TY float64 }

func (f affine) apply(x, y float64) (float64, float64) {
	return f.S*x + f.TX, f.S*y + f.TY
}

type point struct{ X, Y float64 }

// fitPoints finds the transform of the given mode that maps ref[i] closest
// to cand[i] in the least-squares sense. Points far from the first fit
// (more than max(2pt, 3 x median residual)) are dropped and the fit is made
// once more, so a few moved words do not drag the rest.
func fitPoints(ref, cand []point, mode string) affine {
	f := solveFit(ref, cand, mode)
	if mode == fitNone || len(ref) < 3 {
		return f
	}
	res := make([]float64, len(ref))
	for i := range ref {
		x, y := f.apply(ref[i].X, ref[i].Y)
		res[i] = math.Hypot(x-cand[i].X, y-cand[i].Y)
	}
	sorted := append([]float64(nil), res...)
	sort.Float64s(sorted)
	limit := math.Max(2, 3*sorted[len(sorted)/2])
	var r2, c2 []point
	for i := range ref {
		if res[i] <= limit {
			r2 = append(r2, ref[i])
			c2 = append(c2, cand[i])
		}
	}
	if len(r2) >= 2 && len(r2) < len(ref) {
		return solveFit(r2, c2, mode)
	}
	return f
}

func solveFit(ref, cand []point, mode string) affine {
	if mode == fitNone || len(ref) == 0 {
		return affine{S: 1}
	}
	n := float64(len(ref))
	var rx, ry, cx, cy float64
	for i := range ref {
		rx += ref[i].X
		ry += ref[i].Y
		cx += cand[i].X
		cy += cand[i].Y
	}
	rx, ry, cx, cy = rx/n, ry/n, cx/n, cy/n
	s := 1.0
	if mode == fitScale {
		var num, den float64
		for i := range ref {
			dx, dy := ref[i].X-rx, ref[i].Y-ry
			num += dx*(cand[i].X-cx) + dy*(cand[i].Y-cy)
			den += dx*dx + dy*dy
		}
		if den > 1e-9 {
			s = num / den
		}
	}
	return affine{S: s, TX: cx - s*rx, TY: cy - s*ry}
}

// cmpOptions are the pass criteria and fit settings of a PDF comparison.
type cmpOptions struct {
	Tol      float64 // maximum allowed word deviation in points
	Fit      string
	SkipMemo bool    // ignore unmatched reference words in the memo area (see inMemoArea)
	InkLimit float64 // largest picture difference of a picture page that passes, as a fraction (see inkDiff)
	InkAll   bool    // measure the picture difference of every page, not only of picture pages
}

type located struct {
	Text string
	Page int // 1-based
}

// pdfResult is the comparison of one candidate PDF with its reference.
type pdfResult struct {
	// TwoUp tells that the document is printed two pages to a sheet and
	// was compared half by half (see compareSheets). RefPages and
	// CandPages count sheets, and Page has one entry for each half.
	TwoUp               bool
	RefPages, CandPages int
	RefWords, CandWords int
	Matched             int // reference words whose every character has a partner
	Substituted         int // characters paired by position only (different code points)
	Reordered           int // words paired by text and position, outside reading order
	PageMoves           int // reference words paired with text on another page number
	RefOnly, CandOnly   int // words with a character that has no partner
	MemoSkipped         int
	Edges               int // word edges measured
	BaseEdges           int // of those, edges measured at the baseline (see pairY)
	BoxEdges            int // edges of Type 3 words measured at the box middle for want of a baseline
	MaxDev              float64
	Worst               located
	ScaleMin, ScaleMax  float64
	RefOnlySample       []located
	CandOnlySample      []located
	SubstSample         []string
	// Page has one entry for each page number that both PDFs have. A
	// page number in located and in Page is that of a half with TwoUp
	// (see pageLabel).
	Page []pageStat
	// PictureWords counts the candidate words without a partner on
	// picture pages. They are not in CandOnly.
	PictureWords int
}

// pageStat is the comparison of one reference page with the candidate page
// of the same number.
type pageStat struct {
	RefWords, CandWords int
	RefOnly, CandOnly   int     // words without a partner; CandOnly counts them on a picture page too
	MaxDev              float64 // largest edge deviation between the two pages
	Fit                 affine  // fit of the two pages; the identity when there is no paired word
	FitEdges            int     // edges the fit was made from; with -fit scale it has a scale from minScalePoints up
	// Picture is set when the reference page has no word. Hancom Office
	// draws chart text as outlines, so there is nothing to pair; such a
	// page is judged by drawing both pages (see inkDiff). A candidate
	// page without words is not one: its reference words are unmatched.
	Picture bool
	// InkDone tells that Ink was measured; InkErr tells why it was not.
	InkDone bool
	Ink     float64
	InkErr  string
}

func (r pdfResult) pass(o cmpOptions) bool {
	for _, p := range r.Page {
		if p.Picture && (!p.InkDone || p.Ink > o.InkLimit) {
			return false
		}
	}
	return r.RefPages == r.CandPages && r.PageMoves == 0 &&
		r.RefOnly == 0 && r.CandOnly == 0 && r.MaxDev <= o.Tol
}

// picturePages returns the 1-based numbers of the picture pages.
func (r pdfResult) picturePages() []int {
	var out []int
	for i, p := range r.Page {
		if p.Picture {
			out = append(out, i+1)
		}
	}
	return out
}

const (
	sampleWords = 5
	// maxSubst is the longest run of unpaired characters, equal on both
	// sides, that is paired by position as a substitution. Symbol fonts make
	// the two PDFs report different code points for the same glyph (Hancom
	// reports Wingdings codes such as U+009F where rhwp reports U+2022).
	maxSubst = 2
	// minScalePoints is the fewest edge points a page needs before its scale
	// is fitted; with fewer only the translation is fitted.
	minScalePoints = 6
)

// readingOrder returns the word indexes of d page by page, lines from the
// top and words from the left. Words are taken from the top; a word joins
// the current line when its vertical middle is less than half a height
// (the line's mean or its own, whichever is larger) below the line's mean
// vertical middle, so a superscript or a word in a font with other metrics
// does not split the line. The two PDFs write their text in different
// orders (for example table cells before or after the body), so the content
// stream order cannot be used to pair words.
func readingOrder(d *pdfWords) []int {
	idx := make([]int, len(d.Words))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		wa, wb := d.Words[idx[a]], d.Words[idx[b]]
		if wa.Page != wb.Page {
			return wa.Page < wb.Page
		}
		return wa.ymid() < wb.ymid()
	})
	for i := 0; i < len(idx); {
		w := d.Words[idx[i]]
		sumMid, sumH := w.ymid(), w.Y1-w.Y0
		j := i + 1
		for ; j < len(idx); j++ {
			v := d.Words[idx[j]]
			n := float64(j - i)
			h := math.Max(math.Max(sumH/n, v.Y1-v.Y0), 1)
			if v.Page != w.Page || v.ymid()-sumMid/n >= h/2 {
				break
			}
			sumMid += v.ymid()
			sumH += v.Y1 - v.Y0
		}
		line := idx[i:j]
		sort.SliceStable(line, func(a, b int) bool { return d.Words[line[a]].X0 < d.Words[line[b]].X0 })
		i = j
	}
	return idx
}

// normText maps characters that the two producers report differently for
// the same glyph. Hancom Office reports its hyphen as a soft hyphen.
func normText(s string) string {
	return strings.ReplaceAll(s, "\u00ad", "-")
}

// charSeq is a PDF's text as single characters in reading order.
type charSeq struct {
	text        []string
	word        []int // index into pdfWords.Words
	first, last []bool
	start       []int // order position -> index of its first character; one extra at the end
	order       []int
	words       []string // normalized word text by order position
}

func explode(d *pdfWords) *charSeq {
	c := &charSeq{order: readingOrder(d)}
	for _, wi := range c.order {
		t := normText(d.Words[wi].Text)
		c.words = append(c.words, t)
		c.start = append(c.start, len(c.text))
		rs := []rune(t)
		for k, ch := range rs {
			c.text = append(c.text, string(ch))
			c.word = append(c.word, wi)
			c.first = append(c.first, k == 0)
			c.last = append(c.last, k == len(rs)-1)
		}
	}
	c.start = append(c.start, len(c.text))
	return c
}

// pairChars pairs the characters of a and b. Whole words are paired first;
// the words between two paired words are then paired character by
// character, which absorbs words that one PDF splits and the other does not.
// It returns the paired characters and, separately, the substitutions.
func pairChars(a, b *charSeq) (pairs, subst [][2]int) {
	anchors := lcsPairs(a.words, b.words)
	pa, pb := 0, 0 // next unpaired order position
	gap := func(ea, eb int) {
		ca0, ca1 := a.start[pa], a.start[ea]
		cb0, cb1 := b.start[pb], b.start[eb]
		inner := lcsPairs(a.text[ca0:ca1], b.text[cb0:cb1])
		prevA, prevB := ca0, cb0
		flush := func(na, nb int) {
			n := na - prevA
			if n <= 0 || n > maxSubst || nb-prevB != n {
				return
			}
			for k := 0; k < n; k++ {
				if !symbolSubst(a.text[prevA+k], b.text[prevB+k]) {
					return
				}
			}
			for k := 0; k < n; k++ {
				subst = append(subst, [2]int{prevA + k, prevB + k})
			}
		}
		for _, p := range inner {
			ia, ib := p[0]+ca0, p[1]+cb0
			flush(ia, ib)
			pairs = append(pairs, [2]int{ia, ib})
			prevA, prevB = ia+1, ib+1
		}
		flush(ca1, cb1)
	}
	for _, an := range anchors {
		gap(an[0], an[1])
		for k := 0; k < a.start[an[0]+1]-a.start[an[0]]; k++ {
			pairs = append(pairs, [2]int{a.start[an[0]] + k, b.start[an[1]] + k})
		}
		pa, pb = an[0]+1, an[1]+1
	}
	gap(len(a.words), len(b.words))
	return pairs, subst
}

// cp1252High holds the characters that Windows code page 1252 puts at
// 0x80-0x9F. Hancom Office reports a symbol font's byte code through this
// code page, so a Wingdings bullet (0x9F) comes out as U+0178.
const cp1252High = "\u20ac\u201a\u0192\u201e\u2026\u2020\u2021\u02c6\u2030\u0160\u2039\u0152\u017d\u2018\u2019\u201c\u201d\u2022\u2013\u2014\u02dc\u2122\u0161\u203a\u0153\u017e\u0178"

// symbolSubst reports whether ref and cand, two single characters at the
// same place, may be the same glyph reported differently: the reference
// character is a symbol-font code (private use area or a code page 1252
// byte above 0x7F) and the candidate character is a symbol or private use
// character, not a letter or digit. A changed letter or digit is never a
// substitution.
func symbolSubst(ref, cand string) bool {
	r, c := []rune(ref), []rune(cand)
	if len(r) != 1 || len(c) != 1 {
		return false
	}
	rr, cc := r[0], c[0]
	refSym := isPrivateUse(rr) || (rr >= 0x80 && rr <= 0xff) || strings.ContainsRune(cp1252High, rr)
	candSym := isPrivateUse(cc) || !(unicode.IsLetter(cc) || unicode.IsDigit(cc))
	return refSym && candSym
}

func isPrivateUse(r rune) bool { return r >= 0xe000 && r <= 0xf8ff }

// pageKey is a pair of 0-based page numbers: reference page, candidate page.
type pageKey struct{ rp, cp int }

// edge is one measured word edge: the left edge where both words start
// with the paired character, the right edge where both end with it.
type edge struct {
	key       pageKey
	ref, cand point
	refWord   int
	how       int // yMiddle, yBaseline or yNoBaseline
}

// How the vertical position of a pair of words is taken.
const (
	yMiddle     = iota // middle of the word boxes
	yBaseline          // baselines
	yNoBaseline        // middle of the boxes, because the baseline of a Type 3 word is not known
)

// pairY returns the vertical positions at which reference word i and
// candidate word j are compared.
//
// pdftotext makes a word box from the ascent and descent of the word's
// font, so the middle of the box is a fixed distance above the baseline
// for one font. Two cases break that, and those pairs are compared at
// the baselines:
//
//   - One of the words holds a glyph of a Type 3 font. Such a font states
//     no ascent and descent and pdftotext makes up a box that is not where
//     the glyphs are.
//   - The words are not the same run of characters. pdftotext starts a new
//     word where a Type 3 font begins, so one PDF has "(" and "abc" where
//     the other has "(abc", and the two boxes come from different fonts.
//
// Baselines are known only in a document with Type 3 fonts (see
// loadBaselines). Every other pair is compared at the box middles.
func pairY(ref, cand *pdfWords, i, j int) (ry, cy float64, how int) {
	rb, cb := ref.base(i), cand.base(j)
	type3 := rb.Type3 || cb.Type3
	split := utf8.RuneCountInString(ref.Words[i].Text) != utf8.RuneCountInString(cand.Words[j].Text)
	switch {
	case (type3 || split) && rb.OK && cb.OK:
		return rb.Y, cb.Y, yBaseline
	case type3:
		return ref.Words[i].ymid(), cand.Words[j].ymid(), yNoBaseline
	}
	return ref.Words[i].ymid(), cand.Words[j].ymid(), yMiddle
}

// comparePDF pairs the text of ref and cand, fits each pair of pages and
// measures how far the paired word edges are apart.
//
// Text is paired in reading order first (see pairChars). Words left over on
// both sides with the same text are then paired by nearest position, which
// catches words whose reading order differs (table cells whose lines
// interleave differently) and words that moved to another page.
//
// A word edge is compared where both PDFs have a word boundary at the same
// character: the left edges of words that start with paired characters and
// the right edges of words that end with paired characters. Each edge is the
// point (x, vertical position of the word); pairY tells which position. The deviation is the larger of the
// horizontal and vertical distance after the fit, in candidate points. Fits
// are made from the reading-order pairs only. Words on another page number
// are counted in PageMoves and left out of the deviation.
func comparePDF(ref, cand *pdfWords, o cmpOptions) pdfResult {
	r := pdfResult{
		RefPages: len(ref.Pages), CandPages: len(cand.Pages),
		RefWords: len(ref.Words), CandWords: len(cand.Words),
	}
	if ref.TwoUp && cand.TwoUp {
		r.TwoUp, r.RefPages, r.CandPages = true, r.RefPages/2, r.CandPages/2
	}
	r.Page = make([]pageStat, min(len(ref.Pages), len(cand.Pages)))
	for i := range r.Page {
		r.Page[i].Fit = affine{S: 1}
	}
	for _, w := range ref.Words {
		if w.Page < len(r.Page) {
			r.Page[w.Page].RefWords++
		}
	}
	for _, w := range cand.Words {
		if w.Page < len(r.Page) {
			r.Page[w.Page].CandWords++
		}
	}
	for i := range r.Page {
		p := &r.Page[i]
		p.Picture = p.RefWords == 0
	}
	a, b := explode(ref), explode(cand)
	pairs, subst := pairChars(a, b)
	r.Substituted = len(subst)
	for _, s := range subst {
		if len(r.SubstSample) < sampleWords {
			r.SubstSample = append(r.SubstSample, fmt.Sprintf("%q>%q p%s", a.text[s[0]], b.text[s[1]], pageLabel(r.TwoUp, ref.Words[a.word[s[0]]].Page)))
		}
	}

	pairedA := make([]bool, len(a.text))
	pairedB := make([]bool, len(b.text))
	moved := map[int]bool{}
	var edges []edge
	addPair := func(i, j int) {
		pairedA[i], pairedB[j] = true, true
		wa, wb := ref.Words[a.word[i]], cand.Words[b.word[j]]
		k := pageKey{wa.Page, wb.Page}
		if k.rp != k.cp {
			moved[a.word[i]] = true
		}
		ry, cy, how := pairY(ref, cand, a.word[i], b.word[j])
		if a.first[i] && b.first[j] {
			edges = append(edges, edge{k, point{wa.X0, ry}, point{wb.X0, cy}, a.word[i], how})
		}
		if a.last[i] && b.last[j] {
			edges = append(edges, edge{k, point{wa.X1, ry}, point{wb.X1, cy}, a.word[i], how})
		}
	}
	for _, p := range pairs {
		addPair(p[0], p[1])
	}
	for _, p := range subst {
		addPair(p[0], p[1])
	}

	groups := map[pageKey][]int{}
	for i, e := range edges {
		groups[e.key] = append(groups[e.key], i)
	}
	fits := map[pageKey]affine{}
	dominant := map[int]pageKey{} // ref page -> group with most edges
	for k, idx := range groups {
		var rp, cp []point
		for _, i := range idx {
			rp = append(rp, edges[i].ref)
			cp = append(cp, edges[i].cand)
		}
		mode := o.Fit
		if mode == fitScale && len(rp) < minScalePoints {
			mode = fitShift
		}
		f := fitPoints(rp, cp, mode)
		fits[k] = f
		if k.rp == k.cp && k.rp < len(r.Page) {
			r.Page[k.rp].Fit, r.Page[k.rp].FitEdges = f, len(idx)
		}
		if d, ok := dominant[k.rp]; !ok || len(idx) > len(groups[d]) ||
			(len(idx) == len(groups[d]) && k.cp < d.cp) {
			dominant[k.rp] = k
		}
		if r.ScaleMin == 0 || f.S < r.ScaleMin {
			r.ScaleMin = f.S
		}
		if f.S > r.ScaleMax {
			r.ScaleMax = f.S
		}
	}
	fitFor := func(k pageKey) affine {
		if f, ok := fits[k]; ok {
			return f
		}
		if d, ok := dominant[k.rp]; ok {
			return fits[d]
		}
		return affine{S: 1}
	}

	r.Reordered = pairLeftovers(ref, cand, a, b, pairedA, pairedB, fitFor, addPair)
	r.PageMoves = len(moved)
	r.Edges = len(edges)

	for _, e := range edges {
		switch e.how {
		case yBaseline:
			r.BaseEdges++
		case yNoBaseline:
			r.BoxEdges++
		}
		if e.key.rp != e.key.cp {
			continue
		}
		x, y := fitFor(e.key).apply(e.ref.X, e.ref.Y)
		dev := math.Max(math.Abs(x-e.cand.X), math.Abs(y-e.cand.Y))
		if e.key.rp < len(r.Page) && dev > r.Page[e.key.rp].MaxDev {
			r.Page[e.key.rp].MaxDev = dev
		}
		if dev > r.MaxDev {
			r.MaxDev = dev
			w := ref.Words[e.refWord]
			r.Worst = located{w.Text, w.Page + 1}
		}
	}

	unpairedA := map[int]bool{}
	for i, ok := range pairedA {
		if !ok {
			unpairedA[a.word[i]] = true
		}
	}
	unpairedB := map[int]bool{}
	for j, ok := range pairedB {
		if !ok {
			unpairedB[b.word[j]] = true
		}
	}
	for _, wi := range a.order {
		w := ref.Words[wi]
		if !unpairedA[wi] {
			if w.Text != "" {
				r.Matched++
			}
			continue
		}
		if o.SkipMemo && inMemoArea(w, dominant, fits, cand) {
			r.MemoSkipped++
			continue
		}
		if w.Page < len(r.Page) {
			r.Page[w.Page].RefOnly++
		}
		r.RefOnly++
		if len(r.RefOnlySample) < sampleWords {
			r.RefOnlySample = append(r.RefOnlySample, located{w.Text, w.Page + 1})
		}
	}
	for _, wi := range b.order {
		if !unpairedB[wi] {
			continue
		}
		w := cand.Words[wi]
		if w.Page < len(r.Page) {
			r.Page[w.Page].CandOnly++
		}
		if w.Page < len(r.Page) && r.Page[w.Page].Picture {
			r.PictureWords++
			continue
		}
		r.CandOnly++
		if len(r.CandOnlySample) < sampleWords {
			r.CandOnlySample = append(r.CandOnlySample, located{w.Text, w.Page + 1})
		}
	}
	return r
}

// pagePenalty is added to the distance of a word on another page, so a word
// is paired across pages only when its page has no word with the same text.
const pagePenalty = 1e5

// pairLeftovers pairs reference words that have no paired character with
// candidate words of the same text that have none either, nearest first
// after the fit. It returns the number of words paired.
func pairLeftovers(ref, cand *pdfWords, a, b *charSeq, pairedA, pairedB []bool,
	fitFor func(pageKey) affine, addPair func(i, j int)) int {
	free := func(c *charSeq, paired []bool, pos int) bool {
		for k := c.start[pos]; k < c.start[pos+1]; k++ {
			if paired[k] {
				return false
			}
		}
		return c.start[pos+1] > c.start[pos]
	}
	pool := map[string][]int{} // text -> candidate order positions
	for pos := range b.order {
		if free(b, pairedB, pos) {
			pool[b.words[pos]] = append(pool[b.words[pos]], pos)
		}
	}
	n := 0
	for pos, wi := range a.order {
		if !free(a, pairedA, pos) {
			continue
		}
		cands := pool[a.words[pos]]
		if len(cands) == 0 {
			continue
		}
		w := ref.Words[wi]
		best, bestD := -1, math.Inf(1)
		for k, cpos := range cands {
			cw := cand.Words[b.order[cpos]]
			ry, cy, _ := pairY(ref, cand, wi, b.order[cpos])
			x, y := fitFor(pageKey{w.Page, cw.Page}).apply(w.X0, ry)
			d := math.Hypot(x-cw.X0, y-cy) + pagePenalty*math.Abs(float64(w.Page-cw.Page))
			if d < bestD {
				best, bestD = k, d
			}
		}
		cpos := cands[best]
		pool[a.words[pos]] = append(cands[:best:best], cands[best+1:]...)
		for k := 0; k < a.start[pos+1]-a.start[pos]; k++ {
			addPair(a.start[pos]+k, b.start[cpos]+k)
		}
		n++
	}
	return n
}

// memoScale is the smallest page fit scale taken as a sign that Hancom
// Office shrank the body to make room for memo balloons.
const memoScale = 1.01

// inMemoArea reports whether a reference word sits where Hancom Office puts
// memo balloons: the page was shrunk (fit scale above memoScale) and the
// word, moved by the page's fit, lands right of every candidate word on the
// page.
func inMemoArea(w word, dominant map[int]pageKey, fits map[pageKey]affine, cand *pdfWords) bool {
	k, ok := dominant[w.Page]
	if !ok || k.cp >= len(cand.Pages) {
		return false
	}
	f := fits[k]
	if f.S < memoScale {
		return false
	}
	right := 0.0
	for _, cw := range cand.Words {
		if cw.Page == k.cp && cw.X1 > right {
			right = cw.X1
		}
	}
	x0, _ := f.apply(w.X0, w.ymid())
	return x0 >= right
}

func (r pdfResult) String() string {
	return fmt.Sprintf("pages %d/%d, matched %d, subst %d, ref-only %d, cand-only %d, moved %d, max dev %.2f",
		r.RefPages, r.CandPages, r.Matched, r.Substituted, r.RefOnly, r.CandOnly, r.PageMoves, r.MaxDev)
}
