package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// docPages makes n portrait pages (595 x 842) of twelve words each. Every
// word has its own text.
func docPages(n int) [][]word {
	pages := make([][]word, n)
	for k := range pages {
		for i := 0; i < 12; i++ {
			x := 60 + 97*float64(i%5)
			y := 70 + 40*float64(i)
			pages[k] = append(pages[k], word{Text: fmt.Sprintf("p%d-w%d", k, i), X0: x, Y0: y, X1: x + 30, Y1: y + 10})
		}
	}
	return pages
}

// twoUp prints pages two to a landscape sheet (841 x 595): page k goes to
// the left (even k) or right half of sheet k/2, scaled by scale(k) about
// the top left corner of its half.
func twoUp(pages [][]word, scale func(k int) float64) *pdfWords {
	d := &pdfWords{}
	for k, ws := range pages {
		if k%2 == 0 {
			d.Pages = append(d.Pages, pageSize{841, 595})
		}
		s, ox := scale(k), 420.5*float64(k%2)
		for _, w := range ws {
			d.Words = append(d.Words, word{w.Text, k / 2, s*w.X0 + ox, s * w.Y0, s*w.X1 + ox, s * w.Y1})
		}
	}
	return d
}

func every(s float64) func(int) float64 { return func(int) float64 { return s } }

// columnsDoc makes landscape sheets (841 x 595) with two columns of words,
// one on each side of the centre line. It is not printed two to a sheet.
func columnsDoc(sheets int) *pdfWords {
	d := &pdfWords{}
	for s := 0; s < sheets; s++ {
		d.Pages = append(d.Pages, pageSize{841, 595})
		for i := 0; i < 24; i++ {
			x := 60 + 440*float64(i%2) + 90*float64(i/2%3)
			y := 60 + 40*float64(i/2)
			d.Words = append(d.Words, word{fmt.Sprintf("s%d-w%d", s, i), s, x, y, x + 30, y + 10})
		}
	}
	return d
}

// scaleRight returns d with the words right of the centre line of the given
// sheets moved by (dx, dy) and scaled by s about the top of the centre line.
func scaleRight(d *pdfWords, sheets map[int]bool, s, dx, dy float64) *pdfWords {
	out := &pdfWords{Pages: d.Pages}
	for _, w := range d.Words {
		if mid := d.Pages[w.Page].W / 2; sheets[w.Page] && w.X0 >= mid {
			w.X0, w.X1 = mid+s*(w.X0-mid)+dx, mid+s*(w.X1-mid)+dx
			w.Y0, w.Y1 = s*w.Y0+dy, s*w.Y1+dy
		}
		out.Words = append(out.Words, w)
	}
	return out
}

func wordIndex(t *testing.T, d *pdfWords, text string) int {
	t.Helper()
	for i, w := range d.Words {
		if w.Text == text {
			return i
		}
	}
	t.Fatalf("no word %q", text)
	return -1
}

func TestSideBySide(t *testing.T) {
	two := twoUp(docPages(4), every(0.71))
	if !sideBySide(two) {
		t.Errorf("two pages to a sheet are not side by side")
	}
	// A word across the centre line: a wide table, a centred title.
	wide := twoUp(docPages(4), every(0.71))
	wide.Words = append(wide.Words, word{"가운데", 1, 400, 300, 440, 310})
	if sideBySide(wide) {
		t.Errorf("a sheet with a word across the centre line is side by side")
	}
	// A word that ends or starts at the centre line is on one side.
	edge := twoUp(docPages(4), every(0.71))
	edge.Words = append(edge.Words, word{"왼", 0, 400, 300, 420.5, 310}, word{"오", 0, 420.5, 300, 440, 310})
	if !sideBySide(edge) {
		t.Errorf("words that touch the centre line are taken as across it")
	}
	portrait := twoUp(docPages(4), every(0.71))
	portrait.Pages[1] = pageSize{841, 900}
	if sideBySide(portrait) {
		t.Errorf("a document with a portrait page is side by side")
	}
	oneSide := twoUp(docPages(1), every(0.71))
	if sideBySide(oneSide) {
		t.Errorf("a document with words in the left halves only is side by side")
	}
	if sideBySide(&pdfWords{}) {
		t.Errorf("a document without pages is side by side")
	}
}

func TestHalves(t *testing.T) {
	d := twoUp(docPages(3), every(0.71))
	d.Base = make([]wordBase, len(d.Words))
	h := halves(d)
	if !h.TwoUp || len(h.Pages) != 4 || h.Pages[3] != (pageSize{420.5, 595}) {
		t.Fatalf("two-up %v, pages %v, want 4 halves of 420.5 x 595", h.TwoUp, h.Pages)
	}
	if len(h.Words) != len(d.Words) || len(h.Base) != len(d.Base) {
		t.Fatalf("%d words and %d baselines, want %d and %d", len(h.Words), len(h.Base), len(d.Words), len(d.Base))
	}
	for i, w := range h.Words {
		var k int
		fmt.Sscanf(w.Text, "p%d-", &k)
		src := d.Words[i]
		if w.Page != k || w.Text != src.Text || w.X0 != src.X0 || w.Y1 != src.Y1 {
			t.Fatalf("word %d %q is on half %d at %v, want half %d where it was on the sheet", i, w.Text, w.Page, w.X0, k)
		}
	}
	if d.Words[13].Page != 0 || d.TwoUp {
		t.Errorf("halves changed its input")
	}
}

func TestCompareSheetsFitsHalvesApart(t *testing.T) {
	pages := docPages(4)
	ref := twoUp(pages, every(0.55)) // Hancom shrank every page once more for memos
	cand := twoUp(pages, every(0.71))
	o := cmpOptions{Tol: 1, Fit: fitScale}
	r := compareSheets(ref, cand, o)
	if !r.TwoUp || !r.pass(o) || r.MaxDev > 1e-6 {
		t.Fatalf("got %s, two-up %v, want a two-up document that passes with no deviation", r, r.TwoUp)
	}
	if r.RefPages != 2 || r.CandPages != 2 || len(r.Page) != 4 || r.Matched != 48 || r.PageMoves != 0 {
		t.Errorf("sheets %d/%d, %d halves, %d matched, %d moved, want 2/2, 4, 48, 0", r.RefPages, r.CandPages, len(r.Page), r.Matched, r.PageMoves)
	}
	s := 0.71 / 0.55
	for i, p := range r.Page {
		tx := 420.5 * (1 - s) * float64(i%2)
		if !near(p.Fit.S, s) || !near(p.Fit.TX, tx) || !near(p.Fit.TY, 0) || p.RefWords != 12 || p.FitEdges != 24 {
			t.Errorf("half %s: fit %+v from %d edges, %d words, want scale %.4f, shift %.2f 0, 24 edges, 12 words",
				pageLabel(true, i), p.Fit, p.FitEdges, p.RefWords, s, tx)
		}
	}
	// One fit of a sheet cannot hold both halves.
	if sheet := comparePDF(ref, cand, o); sheet.pass(o) || sheet.MaxDev < 20 {
		t.Errorf("sheet by sheet: %s, want a failure with a large deviation", sheet)
	}
	// A half is fitted as -fit says, like every page.
	shift := cmpOptions{Tol: 1, Fit: fitShift}
	rs := compareSheets(ref, cand, shift)
	if !rs.TwoUp || rs.pass(shift) || rs.MaxDev < 20 {
		t.Errorf("-fit shift: %s, two-up %v, want a two-up document that fails", rs, rs.TwoUp)
	}
	for i, p := range rs.Page {
		if p.Fit.S != 1 {
			t.Errorf("-fit shift: half %s has scale %v", pageLabel(true, i), p.Fit.S)
		}
	}
}

func TestCompareSheetsMeasuresAWordInsideAHalf(t *testing.T) {
	pages := docPages(4)
	ref := twoUp(pages, every(0.55))
	cand := twoUp(pages, every(0.71))
	i := wordIndex(t, cand, "p3-w5")
	cand.Words[i].X0 += 2.5
	cand.Words[i].X1 += 2.5
	o := cmpOptions{Tol: 1, Fit: fitScale}
	r := compareSheets(ref, cand, o)
	if !r.TwoUp || r.pass(o) || math.Abs(r.MaxDev-2.5) > 0.2 || r.Worst.Text != "p3-w5" {
		t.Fatalf("got %s worst %q, two-up %v, want a two-up failure at p3-w5 near 2.5pt", r, r.Worst.Text, r.TwoUp)
	}
	if got := pageLabel(r.TwoUp, r.Worst.Page-1); got != "2R" {
		t.Errorf("worst word is on %s, want 2R", got)
	}
	for k, p := range r.Page {
		if (k == 3) != (p.MaxDev > o.Tol) {
			t.Errorf("half %s: largest deviation %.2f", pageLabel(true, k), p.MaxDev)
		}
	}
	o.Tol = 3
	if !compareSheets(ref, cand, o).pass(o) {
		t.Errorf("2.5pt deviation should pass with tolerance 3")
	}
}

func TestCompareSheetsWordInTheOtherHalfIsMoved(t *testing.T) {
	pages := docPages(4)
	ref := twoUp(pages, every(0.55))
	// The last word of page 1 starts page 2 in the candidate: it is on
	// the same sheet and in the other half.
	moved := pages[0][11]
	moved.Y0, moved.Y1 = 30, 40
	candPages := [][]word{pages[0][:11], append([]word{moved}, pages[1]...), pages[2], pages[3]}
	cand := twoUp(candPages, every(0.71))
	o := cmpOptions{Tol: 1000, Fit: fitScale}
	r := compareSheets(ref, cand, o)
	if !r.TwoUp || r.PageMoves != 1 || r.RefOnly != 0 || r.CandOnly != 0 || r.pass(o) {
		t.Errorf("got %s, two-up %v, want one word moved to another page and a failure", r, r.TwoUp)
	}
	// Sheet by sheet the word did not leave its page.
	if sheet := comparePDF(ref, cand, o); sheet.PageMoves != 0 {
		t.Errorf("sheet by sheet: %d words moved, want 0", sheet.PageMoves)
	}
	// A word that stays in its half is not moved.
	if same := compareSheets(ref, twoUp(pages, every(0.71)), o); same.PageMoves != 0 || !same.pass(o) {
		t.Errorf("same halves: %s", same)
	}
}

func TestCompareSheetsLeavesLandscapePagesAlone(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale}
	only := func(sheets ...int) map[int]bool {
		m := map[int]bool{}
		for _, s := range sheets {
			m[s] = true
		}
		return m
	}
	cols := columnsDoc(3)
	if !sideBySide(cols) {
		t.Fatalf("the two columns are not side by side: the cases below test nothing")
	}
	shrunk := transform(cols, 0.8, 30, 10) // memo balloons: the whole sheet is shrunk
	if !sideBySide(shrunk) {
		t.Fatalf("the shrunken columns are not side by side")
	}
	cases := []struct {
		name      string
		ref, cand *pdfWords
		pass      bool
		minDev    float64
	}{
		{"same columns", cols, cols, true, 0},
		{"whole sheet shrunk", shrunk, cols, true, 0},
		{"right column 5pt to the right", cols, scaleRight(cols, only(0, 1, 2), 1, 5, 0), false, 1.5},
		{"right column 80pt to the right", cols, scaleRight(cols, only(0, 1, 2), 1, 80, 0), false, 20},
		{"right column 5pt down", cols, scaleRight(cols, only(0, 1, 2), 1, 0, 5), false, 2},
		// The right column is scaled about a point that is not the
		// corner of its half.
		{"right column scaled and moved", cols, scaleRight(cols, only(0, 1, 2), 1.03, 9, 6), false, 2},
		// The right halves are scaled about their corners by so little
		// that one fit of the sheet is within twoUpGap of both.
		{"right halves scaled by 0.8 %", cols, scaleRight(cols, only(0, 1, 2), 1.008, 0, 0), false, 1},
		// One sheet of three looks like two pages to a sheet.
		{"one sheet of three scaled by halves", cols, scaleRight(cols, only(1), 1.03, 0, 0), false, 2},
	}
	for _, c := range cases {
		r := compareSheets(c.ref, c.cand, o)
		if r.TwoUp {
			t.Errorf("%s: taken as two pages to a sheet", c.name)
		}
		if want := comparePDF(c.ref, c.cand, o); !reflect.DeepEqual(r, want) {
			t.Errorf("%s: got %s, want what comparePDF gives: %s", c.name, r, want)
		}
		if r.pass(o) != c.pass || r.MaxDev < c.minDev {
			t.Errorf("%s: %s, pass %v, want pass %v and a deviation of %.1fpt or more", c.name, r, r.pass(o), c.pass, c.minDev)
		}
	}
	// A word across the centre line keeps the sheets whole even where the
	// halves are scaled.
	ref := twoUp(docPages(4), every(0.55))
	cand := twoUp(docPages(4), every(0.71))
	ref.Words = append(ref.Words, word{"가운데", 0, 400, 560, 440, 570})
	cand.Words = append(cand.Words, word{"가운데", 0, 400, 560, 440, 570})
	if r := compareSheets(ref, cand, o); r.TwoUp || r.pass(o) || !reflect.DeepEqual(r, comparePDF(ref, cand, o)) {
		t.Errorf("word across the centre line: %s, two-up %v, want what comparePDF gives", r, r.TwoUp)
	}
	// Two of three sheets scaled by halves: the document is two-up, and
	// the sheet that is not scaled is compared by halves too.
	r := compareSheets(cols, scaleRight(cols, only(0, 2), 1.03, 0, 0), o)
	if !r.TwoUp || len(r.Page) != 6 || !near(r.Page[1].Fit.S, 1.03) || !near(r.Page[3].Fit.S, 1) {
		t.Errorf("two sheets of three scaled by halves: %s, two-up %v", r, r.TwoUp)
	}
}

func TestHalfScales(t *testing.T) {
	pages := docPages(4)
	ref := twoUp(pages, every(0.55))
	o := cmpOptions{Tol: 1, Fit: fitScale}
	// Right pages a little larger than left pages.
	cand := twoUp(pages, func(k int) float64 { return 0.71 + 0.01*float64(k%2) })
	r := compareSheets(ref, cand, o)
	if !r.TwoUp || !r.pass(o) {
		t.Fatalf("got %s, two-up %v, want a two-up document that passes", r, r.TwoUp)
	}
	h := r.halfScales()
	l, rt := 0.71/0.55, 0.72/0.55
	if h.Left != 2 || h.Right != 2 || !near(h.LeftMin, l) || !near(h.LeftMax, l) || !near(h.RightMin, rt) || !near(h.RightMax, rt) {
		t.Errorf("half scales %+v, want left %.4f and right %.4f, two halves each", h, l, rt)
	}
	if len(h.Differ) != 2 || h.Differ[0].Sheet != 1 || h.Differ[1].Sheet != 2 ||
		!near(h.Differ[1].Left, l) || !near(h.Differ[1].Right, rt) || h.Differ[1].LeftEdges != 24 {
		t.Errorf("sheets that differ: %+v, want sheets 1 and 2", h.Differ)
	}
	if got, want := twoUpCell(r), "two-up: left 1.2909, right 1.3091; halves differ on sheets 1, 2"; got != want {
		t.Errorf("cell %q, want %q", got, want)
	}
	// Only sheet 2 differs, and by less than halfScaleDiff on sheet 1.
	cand = twoUp(pages, func(k int) float64 { return []float64{0.71, 0.713, 0.71, 0.72}[k] })
	r = compareSheets(ref, cand, o)
	if got, want := twoUpCell(r), "two-up: left 1.2909, right 1.2964-1.3091; halves differ on sheet 2"; got != want {
		t.Errorf("cell %q, want %q", got, want)
	}
	// A half with too few words for a scale is left out.
	few := docPages(4)
	few[2] = few[2][:2]
	r = compareSheets(twoUp(few, every(0.55)), twoUp(few, every(0.71)), o)
	if h := r.halfScales(); !r.TwoUp || h.Left != 1 || h.Right != 2 || len(h.Differ) != 0 {
		t.Errorf("half with two words: two-up %v, scales %+v, want one left and two right halves", r.TwoUp, h)
	}
}

func TestCompareSheetsPictureHalf(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	// Three pages: the right half of sheet 2 has no word in the
	// reference, and a chart's words in the candidate.
	ref := twoUp(docPages(3), every(0.55))
	candPages := append(docPages(3), []word{{Text: "판매", X0: 60, Y0: 70, X1: 90, Y1: 80}, {Text: "분기", X0: 60, Y0: 110, X1: 90, Y1: 120}})
	cand := twoUp(candPages, every(0.71))
	r := compareSheets(ref, cand, o)
	if !r.TwoUp || len(r.Page) != 4 {
		t.Fatalf("got %s, two-up %v, %d halves", r, r.TwoUp, len(r.Page))
	}
	for i, p := range r.Page {
		if p.Picture != (i == 3) {
			t.Errorf("half %s: picture %v", pageLabel(true, i), p.Picture)
		}
	}
	if r.CandOnly != 0 || r.PictureWords != 2 || r.pass(o) {
		t.Errorf("cand-only %d, picture words %d, pass %v, want 0, 2 and no pass before the picture is compared", r.CandOnly, r.PictureWords, r.pass(o))
	}
	r.Page[3].InkDone, r.Page[3].Ink = true, 0.004
	if !r.pass(o) {
		t.Errorf("picture half at 0.4%% fails")
	}
	if got, want := pictureCell(r), "p2R 0.40 %, 2 words not paired"; got != want {
		t.Errorf("picture cell %q, want %q", got, want)
	}
}

func TestHalfImage(t *testing.T) {
	ref, cand := blank(200, 120), blank(200, 120)
	ref.fill(20, 20, 80, 100, black)
	cand.fill(20, 20, 80, 100, black)
	ref.fill(120, 20, 180, 100, blue)
	cand.fill(130, 60, 190, 110, blue)
	before := count(outline(ref, inkStep))
	id := affine{S: 1}
	if d := inkDiff(halfImage(ref, 1), halfImage(cand, 1), id, 1); d != 0 {
		t.Errorf("left halves, the same ink: difference %.3f, want 0", d)
	}
	if d := inkDiff(halfImage(ref, 2), halfImage(cand, 2), id, 1); d < 0.3 {
		t.Errorf("right halves, other ink: difference %.3f, want a large one", d)
	}
	if halfImage(ref, 0) != ref {
		t.Errorf("side 0 does not give the image itself")
	}
	at := func(g *pageImage, xpt, ypt int) [3]uint8 {
		p := 3 * (ypt*inkPxPerPt*g.W + xpt*inkPxPerPt)
		return [3]uint8{g.Pix[p], g.Pix[p+1], g.Pix[p+2]}
	}
	white := [3]uint8{255, 255, 255}
	l, r := halfImage(ref, 1), halfImage(ref, 2)
	if at(l, 50, 50) != black || at(l, 150, 50) != white || at(r, 50, 50) != white || at(r, 150, 50) != blue {
		t.Errorf("left half %v %v, right half %v %v, want black white, white blue", at(l, 50, 50), at(l, 150, 50), at(r, 50, 50), at(r, 150, 50))
	}
	// The cut is at the centre line.
	g := blank(200, 120)
	g.fill(0, 10, 200, 20, black)
	if a, b := count(outline(halfImage(g, 1), inkStep)), count(outline(halfImage(g, 2), inkStep)); a != b || a == 0 {
		t.Errorf("a bar across the sheet: %d outline pixels in the left half, %d in the right", a, b)
	}
	if count(outline(ref, inkStep)) != before {
		t.Errorf("halfImage painted over its input")
	}
}

// ppm writes g as the binary PPM image that pdftoppm makes.
func ppm(g *pageImage) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "P6\n%d %d\n255\n", g.W, g.H)
	b.Write(g.Pix)
	return b.Bytes()
}

func TestMeasureInkOfAHalf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script in place of pdftoppm")
	}
	// Sheet 2 of two PDFs: the left halves differ, the right halves are
	// the same. The script gives the picture of the page it is asked for.
	dir := t.TempDir()
	script := "#!/bin/sh\nexec cat \"$7.$4.ppm\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pdftoppm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ref, cand := blank(200, 120), blank(200, 120)
	ref.fill(20, 20, 80, 100, black)
	cand.fill(30, 40, 90, 110, black)
	ref.fill(120, 20, 180, 100, blue)
	cand.fill(120, 20, 180, 100, blue)
	refPath, candPath := filepath.Join(dir, "ref.pdf"), filepath.Join(dir, "cand.pdf")
	for path, g := range map[string]*pageImage{refPath + ".2.ppm": ref, candPath + ".2.ppm": cand} {
		if err := os.WriteFile(path, ppm(g), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id := affine{S: 1}
	o := cmpOptions{Tol: 1, InkLimit: 0.01}
	r := pdfResult{TwoUp: true, RefPages: 2, CandPages: 2, Page: []pageStat{
		{Fit: id, RefWords: 3, CandWords: 3}, {Fit: id, RefWords: 3, CandWords: 3},
		{Fit: id, RefWords: 3, CandWords: 3}, {Fit: id, Picture: true},
	}}
	measureInk(refPath, candPath, &r, o)
	for i, p := range r.Page[:3] {
		if p.InkDone || p.InkErr != "" {
			t.Errorf("half %s is not a picture half and was drawn: %+v", pageLabel(true, i), p)
		}
	}
	if p := r.Page[3]; !p.InkDone || p.Ink != 0 {
		t.Errorf("right half of sheet 2, the same ink: done %v, difference %.3f, error %q, want 0", p.InkDone, p.Ink, p.InkErr)
	}
	if !r.pass(o) {
		t.Errorf("the picture half is the same and the document fails")
	}
	// With -ink-all the left half of sheet 2 is measured too; sheet 1
	// cannot be drawn.
	o.InkAll = true
	measureInk(refPath, candPath, &r, o)
	if p := r.Page[2]; !p.InkDone || p.Ink < 0.3 {
		t.Errorf("left half of sheet 2, other ink: done %v, difference %.3f, error %q, want a large one", p.InkDone, p.Ink, p.InkErr)
	}
	if p := r.Page[3]; !p.InkDone || p.Ink != 0 {
		t.Errorf("right half of sheet 2 with -ink-all: done %v, difference %.3f", p.InkDone, p.Ink)
	}
	if r.Page[0].InkDone || r.Page[0].InkErr == "" || r.Page[1].InkDone || r.Page[1].InkErr == "" {
		t.Errorf("sheet 1 has no picture and was measured: %+v %+v", r.Page[0], r.Page[1])
	}
	// One page to a sheet: page 2 is the whole sheet.
	one := pdfResult{RefPages: 2, CandPages: 2, Page: []pageStat{{Fit: id, RefWords: 3}, {Fit: id, Picture: true}}}
	measureInk(refPath, candPath, &one, cmpOptions{Tol: 1, InkLimit: 0.01})
	if p := one.Page[1]; !p.InkDone || p.Ink < 0.1 {
		t.Errorf("whole sheet 2: done %v, difference %.3f, want the difference of the left halves", p.InkDone, p.Ink)
	}
}

func TestReportTwoUp(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	pages := docPages(4)
	ref := twoUp(pages, every(0.55))
	good := compareSheets(ref, twoUp(pages, every(0.71)), o)

	cand := twoUp(pages, func(k int) float64 { return 0.71 + 0.01*float64(k%2) })
	i := wordIndex(t, cand, "p3-w5")
	cand.Words[i].X0 += 2.5
	cand.Words[i].X1 += 2.5
	cand.Words = append(cand.Words, word{"덤", 0, 700, 500, 730, 510})
	bad := compareSheets(ref, cand, o)

	plainDoc := testDoc(595, [][]string{{"가", "나", "다"}})
	plain := comparePDF(plainDoc, plainDoc, o)
	rows := []row{
		{Name: "twoup.hwp", PDF: &good, Pass: good.pass(o)},
		{Name: "twoup-bad.hwp", PDF: &bad, Pass: bad.pass(o)},
		{Name: "plain.hwp", PDF: &plain, Pass: plain.pass(o)},
	}
	rep := renderReport(rows, o, "cand", false)
	for _, want := range []string{
		"2 of 3 documents pass.",
		"| twoup.hwp | 2/2 | 0.00 | 0/0 | 0 | 0 | two-up: left 1.2909, right 1.2909 | 0 |  | - | - | - | - | pass |",
		"| twoup-bad.hwp | 2/2 | 2.50 | 0/1 | 0 | 0 | two-up: left 1.2909, right 1.3091; halves differ on sheets 1, 2 | 0 |  | - | - | - | - | FAIL |",
		"| plain.hwp | 1/1 | 0.00 | 0/0 | 0 | 0 | 1.0000 | 0 |  | - | - | - | - | pass |",
		"- PDF: 2/2 sheets, 48/49 words, 48 matched (0 out of reading order), 0 on another page, 96 edges measured",
		"- PDF: two pages to a sheet, compared half by half (L left, R right): scale of the left halves 1.2909, of the right halves 1.3091",
		"- PDF: the halves of sheet 1 differ in scale: left 1.2909 from 24 edges, right 1.3091 from 24 edges",
		"- PDF: the halves of sheet 2 differ in scale: left 1.2909 from 24 edges, right 1.3091 from 24 edges",
		`- PDF: largest deviation 2.50 pt at "p3-w5" (ref page 2R)`,
		`- PDF: only in cand: "덤" p1R`,
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q\n%s", want, rep)
		}
	}
	if strings.Contains(rep, "### twoup.hwp") || strings.Contains(rep, "### plain.hwp") {
		t.Errorf("a passing document is listed under Differences\n%s", rep)
	}

	o.InkAll = true
	good.Page[1].InkDone, good.Page[1].Ink = true, 0.0004
	rep = renderReport(rows[:1], o, "cand", false)
	if want := "| twoup.hwp | 1R | 12/12 | 0/0 | 0 | 0.00 | 0.04 | words |"; !strings.Contains(rep, want) {
		t.Errorf("report with -ink-all lacks %q\n%s", want, rep)
	}
}
