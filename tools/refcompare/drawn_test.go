package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// sheet returns a white page of w x h points drawn at drawnPxPerPt.
func sheet(w, h int) *region {
	g := &region{}
	g.W, g.H = w*drawnPxPerPt, h*drawnPxPerPt
	g.Pix = bytes.Repeat([]byte{255}, 3*g.W*g.H)
	return g
}

// rect paints the rectangle from (x0, y0) to (x1, y1), in points, black.
func (g *region) rect(x0, y0, x1, y1 float64) {
	for y := max(int(y0*drawnPxPerPt), 0); y < min(int(y1*drawnPxPerPt), g.H); y++ {
		for x := max(int(x0*drawnPxPerPt), 0); x < min(int(x1*drawnPxPerPt), g.W); x++ {
			copy(g.Pix[3*(y*g.W+x):], black[:])
		}
	}
}

// segments tells which of the seven strokes a digit is made of: top, upper
// right, lower right, bottom, lower left, upper left, middle.
var segments = map[rune]string{
	'0': "abcdef", '1': "bc", '2': "abged", '3': "abgcd", '4': "fgbc",
	'5': "afgcd", '6': "afgedc", '7': "abc", '8': "abcdefg", '9': "abcdfg",
}

// label draws digits h points high with their top left corner at (x, y),
// and returns the box pdftotext would give the word: the font's ascent
// and descent, which reach above and below the digits.
func (g *region) label(text string, x, y, h float64) word {
	w, t := h/2, 0.12*h
	x0 := x
	for _, ch := range text {
		for _, s := range segments[ch] {
			switch s {
			case 'a':
				g.rect(x, y, x+w, y+t)
			case 'b':
				g.rect(x+w-t, y, x+w, y+h/2)
			case 'c':
				g.rect(x+w-t, y+h/2, x+w, y+h)
			case 'd':
				g.rect(x, y+h-t, x+w, y+h)
			case 'e':
				g.rect(x, y+h/2, x+t, y+h)
			case 'f':
				g.rect(x, y, x+t, y+h/2)
			case 'g':
				g.rect(x, y+h/2-t/2, x+w, y+h/2+t/2)
			}
		}
		x += 0.7 * h
	}
	return word{Text: text, X0: x0, Y0: y - 0.3*h, X1: x - 0.2*h, Y1: y + 1.3*h}
}

// labelFit is the fit of the drawn pages: the reference is the candidate
// shrunk to four fifths, as Hancom Office shrinks a page with memos, and
// moved.
var labelFit = affine{S: 1.25, TX: 3, TY: -2}

// labelHeight is the height of a digit in the candidate. The reference
// draws it 3 pt high, which is what a chart label of 4 pt is.
const labelHeight = 3.75

// refLabel draws on the reference page the label that the fit puts at
// (x, y) of the candidate page.
func refLabel(g *region, text string, x, y float64) {
	g.label(text, (x-labelFit.TX)/labelFit.S, (y-labelFit.TY)/labelFit.S, labelHeight/labelFit.S)
}

func TestDrawnShiftSamePlace(t *testing.T) {
	// A chart label that the candidate has as a word and the reference
	// as a shape, where the fit puts it and up to the tolerance from it.
	for _, tc := range []struct {
		dx, dy, shift float64
	}{
		{0, 0, 0}, {0.5, 0, 0.5}, {0, -0.75, 0.75}, {-1, 1, 1},
	} {
		ref, cand := sheet(100, 60), sheet(100, 60)
		w := cand.label("2022", 40, 30, labelHeight)
		refLabel(ref, "2022", 40+tc.dx, 30+tc.dy)
		got, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01)
		if !ok {
			t.Errorf("label %.2f, %.2f pt from its place is not found", tc.dx, tc.dy)
		} else if got < tc.shift-0.25 || got > tc.shift+0.25 {
			t.Errorf("label %.2f, %.2f pt from its place: shift %.3f, want %.2f to a quarter of a point", tc.dx, tc.dy, got, tc.shift)
		}
	}
}

func TestDrawnShiftMovedShape(t *testing.T) {
	// Further away than the tolerance and its slack of a quarter of a
	// point, the reference shape is not the word's.
	for _, d := range [][2]float64{{1.5, 0}, {0, 1.5}, {-2, 0}, {0, 2}, {3, 0}, {0, -3}, {3, 3}} {
		ref, cand := sheet(100, 60), sheet(100, 60)
		w := cand.label("2022", 40, 30, labelHeight)
		refLabel(ref, "2022", 40+d[0], 30+d[1])
		if s, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01); ok {
			t.Errorf("label %.1f, %.1f pt from its place is found at shift %.3f", d[0], d[1], s)
		}
	}
	// A wider tolerance takes it, as it does for a word edge.
	ref, cand := sheet(100, 60), sheet(100, 60)
	w := cand.label("2022", 40, 30, labelHeight)
	refLabel(ref, "2022", 43, 30)
	if _, ok := drawnShift(ref, cand, w, labelFit, 3, 0.01); !ok {
		t.Errorf("label 3 pt from its place is not found at a tolerance of 3 pt")
	}

	// A label of one digit repeated, moved by one digit: three of its
	// four digits lie on a digit, and it is still not at its place.
	ref, cand = sheet(100, 60), sheet(100, 60)
	w = cand.label("1111", 40, 30, labelHeight)
	refLabel(ref, "1111", 40+0.7*labelHeight, 30)
	if s, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01); ok {
		t.Errorf("repeated digits moved by one digit are found at shift %.3f", s)
	}
}

func TestDrawnShiftNothingThere(t *testing.T) {
	// The candidate made up a word: the reference has nothing there.
	ref, cand := sheet(100, 60), sheet(100, 60)
	w := cand.label("2022", 40, 30, labelHeight)
	if _, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01); ok {
		t.Errorf("a word on an empty place of the reference is taken for drawn text")
	}
	// The same label elsewhere on the reference page does not help.
	refLabel(ref, "2022", 40, 45)
	refLabel(ref, "2022", 70, 30)
	if _, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01); ok {
		t.Errorf("a label elsewhere on the reference page is taken for the word")
	}
	// A rule of a table runs through the place.
	ref = sheet(100, 60)
	ref.rect(20, 25.4, 70, 25.8)
	if _, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01); ok {
		t.Errorf("a rule through the place is taken for the word")
	}
	// A word without ink (white, or a space) on an empty place.
	blank := sheet(100, 60)
	if _, ok := drawnShift(sheet(100, 60), blank, w, labelFit, 1, 0.01); ok {
		t.Errorf("a word without ink is taken for drawn text")
	}
	// The reference has the label, too faint to be measured (see
	// inkStep): there is no outline to lay the word on.
	ref = sheet(100, 60)
	refLabel(ref, "2022", 40, 30)
	for i, v := range ref.Pix {
		if v == 0 {
			ref.Pix[i] = 255 - inkStep + 8
		}
	}
	if _, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01); ok {
		t.Errorf("a label too faint to be measured in the reference is taken for the word")
	}
	// The same with a rule just below the word box.
	ref.rect(20, (w.Y1+0.4-labelFit.TY)/labelFit.S, 70, (w.Y1+0.8-labelFit.TY)/labelFit.S)
	if _, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01); ok {
		t.Errorf("a faint label next to a rule in the reference is taken for the word")
	}
	if _, ok := drawnShift(ref, cand, w, affine{}, 1, 0.01); ok {
		t.Errorf("a word on a page without a fit is taken for drawn text")
	}
}

func TestDrawnShiftOtherShape(t *testing.T) {
	// Another shape at the very place: one digit of four is another, the
	// reference has a stroke more, a stroke less, or a box.
	for _, tc := range []struct{ cand, ref string }{
		{"2022", "2021"}, {"2021", "2022"}, {"6", "8"}, {"8", "6"}, {"1", "7"}, {"3", "9"}, {"100", "150"},
	} {
		ref, cand := sheet(100, 60), sheet(100, 60)
		w := cand.label(tc.cand, 40, 30, labelHeight)
		refLabel(ref, tc.ref, 40, 30)
		if s, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01); ok {
			t.Errorf("%q in the candidate on %q in the reference is found at shift %.3f", tc.cand, tc.ref, s)
		}
	}
	ref, cand := sheet(100, 60), sheet(100, 60)
	w := cand.label("2022", 40, 30, labelHeight)
	ref.rect(29.6, 25.6, 37.6, 28.6)
	if _, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01); ok {
		t.Errorf("a box at the place is taken for the word")
	}
	// The label with a line of the chart through it in the reference
	// only.
	ref = sheet(100, 60)
	refLabel(ref, "2022", 40, 30)
	ref.rect(20, 26.9, 70, 27.2)
	if _, ok := drawnShift(ref, cand, w, labelFit, 1, 0.01); ok {
		t.Errorf("a label with a line through it in the reference only is taken for the word")
	}
}

func TestDrawnShiftFullOfInk(t *testing.T) {
	// A hatched area in both PDFs, and a word on it that only the
	// candidate has. Every outline pixel of the word is next to a line
	// of the hatching, so the shapes lie on each other wherever the
	// reference is moved to; that says nothing about the word.
	hatch := func(g *region, s float64) {
		for y := 10.0; y < 50; y += 0.6 {
			g.rect(10/s, y/s, 90/s, (y+0.2)/s)
		}
	}
	ref, cand := sheet(100, 60), sheet(100, 60)
	hatch(cand, 1)
	w := cand.label("2022", 40, 30, labelHeight)
	id := affine{S: 1}
	hatch(ref, 1)
	if _, ok := drawnShift(ref, cand, w, id, 1, 0.01); ok {
		t.Errorf("a word on a hatched area that the reference does not draw is taken for drawn text")
	}
	// With the label in the reference too, it is still not known.
	ref.label("2022", 40, 30, labelHeight)
	if _, ok := drawnShift(ref, cand, w, id, 1, 0.01); ok {
		t.Errorf("a word on a hatched area is taken for drawn text")
	}
	// A word of upright strokes on the grid lines of a chart, which both
	// PDFs have: the lines hide the word, and they lie on each other
	// wherever the reference is moved up or down.
	ref, cand = sheet(100, 60), sheet(100, 60)
	w = cand.label("111", 40, 30, labelHeight)
	for _, g := range []*region{ref, cand} {
		for i := 0.0; i < 3; i++ {
			x := 40 + i*0.7*labelHeight + 0.38*labelHeight
			g.rect(x, 10, x+0.12*labelHeight, 50)
		}
	}
	if _, ok := drawnShift(ref, cand, w, id, 1, 0.01); ok {
		t.Errorf("a word hidden in grid lines is taken for drawn text")
	}
	// A label between two rules, as in a table, is found: moved along
	// the rules it does not lie on itself.
	ref, cand = sheet(100, 60), sheet(100, 60)
	for _, g := range []*region{ref, cand} {
		g.rect(10, 28.9, 90, 29.2)
		g.rect(10, 35.5, 90, 35.8)
		w = g.label("2022", 40, 30, labelHeight)
	}
	if _, ok := drawnShift(ref, cand, w, id, 1, 0.01); !ok {
		t.Errorf("a label between two rules is not found")
	}
}

func TestShifts(t *testing.T) {
	s := shifts(2)
	if len(s) != 25 || s[0] != [2]int{0, 0} {
		t.Fatalf("shifts(2): %d shifts, first %v, want 25 and 0,0", len(s), s[0])
	}
	seen := map[[2]int]bool{}
	for i, v := range s {
		if seen[v] {
			t.Errorf("shift %v twice", v)
		}
		seen[v] = true
		reach := func(v [2]int) int { return max(v[0], -v[0], v[1], -v[1]) }
		if i > 0 && reach(v) < reach(s[i-1]) {
			t.Errorf("shift %v comes after the larger %v", v, s[i-1])
		}
	}
}

func TestHasTwin(t *testing.T) {
	d := &pdfWords{Words: []word{
		{"가", 0, 100, 100, 130, 110},
		{"가", 0, 100.2, 100.1, 130.2, 110.1}, // printed twice
		{"나", 0, 160, 100, 190, 110},         // next on the line
		{"다", 0, 100, 112, 130, 122},         // next line
		{"라", 0, 120, 104, 150, 114},         // across the first, a little lower
		{"가", 1, 100, 100, 130, 110},         // same place on another page
	}}
	page := []int{0, 1, 2, 3, 4}
	for i, want := range []bool{true, true, false, false, true} {
		if got := hasTwin(d, i, page); got != want {
			t.Errorf("word %d (%s): twin %v, want %v", i, d.Words[i].Text, got, want)
		}
	}
	if hasTwin(d, 5, []int{5}) {
		t.Errorf("a word alone on its page has a twin")
	}
}

func TestComparePDFKeepsWordsWithoutPartner(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	ref := testDoc(595, [][]string{{"가", "나", "다"}, {}, {"마"}})
	cand := testDoc(595, [][]string{{"가", "나", "다", "라"}, {"판매"}, {"마"}, {"바"}})
	// "가" is printed twice at one place, and where the reference has
	// "7)" the candidate has "5)".
	cand.Words = append(cand.Words, cand.Words[0], word{"5)", 0, 300, 400, 315, 410})
	ref.Words = append(ref.Words, word{"7)", 0, 300, 400, 315, 410})
	r := comparePDF(ref, cand, o)
	// The word of the picture page (page 2) is not among them; the word
	// of the page that only the candidate has is.
	var got []string
	for _, w := range r.CandOnlyWords {
		got = append(got, fmt.Sprintf("%s p%d shared %v", w.Text, w.Page+1, w.Shared))
	}
	want := []string{"가 p1 shared true", "라 p1 shared false", "5) p1 shared true", "바 p4 shared false"}
	if !reflect.DeepEqual(got, want) || r.CandOnly != 4 || r.RefOnly != 1 || r.DrawnWords != 0 {
		t.Errorf("words without a partner %v, cand-only %d, ref-only %d, drawn %d, want %v, 4, 1, 0", got, r.CandOnly, r.RefOnly, r.DrawnWords, want)
	}
}

// TestFakePdftoppm is not a test: it stands in for pdftoppm when
// REFCOMPARE_FAKE_PDFTOPPM is set (see fakePdftoppm). It cuts the box out
// of <pdf>.<page>.ppm, a page drawn at drawnPxPerPt, the way pdftoppm
// does: a box that sticks out of the page is cut, and a box whose corner
// is not on the page gives the whole page.
func TestFakePdftoppm(t *testing.T) {
	if os.Getenv("REFCOMPARE_FAKE_PDFTOPPM") == "" {
		t.Skip("stands in for pdftoppm in other tests")
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	opt := map[string]int{}
	for i := 0; i+1 < len(args); i += 2 {
		n, err := strconv.Atoi(args[i+1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "fake pdftoppm: bad arguments %v\n", args)
			os.Exit(2)
		}
		opt[args[i]] = n
	}
	if opt["-r"] != 72*drawnPxPerPt || opt["-f"] != opt["-l"] {
		fmt.Fprintf(os.Stderr, "fake pdftoppm: unexpected arguments %v\n", args)
		os.Exit(2)
	}
	f, err := os.Open(fmt.Sprintf("%s.%d.ppm", args[len(args)-1], opt["-f"]))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	g, err := parsePPM(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	x, y := opt["-x"], opt["-y"]
	if x >= g.W || y >= g.H {
		os.Stdout.Write(ppm(g))
		os.Exit(0)
	}
	out := &pageImage{W: min(opt["-W"], g.W-x), H: min(opt["-H"], g.H-y)}
	for row := 0; row < out.H; row++ {
		out.Pix = append(out.Pix, g.Pix[3*((y+row)*g.W+x):3*((y+row)*g.W+x+out.W)]...)
	}
	os.Stdout.Write(ppm(out))
	os.Exit(0)
}

// fakePdftoppm puts a pdftoppm on PATH that draws the pages written with
// writeSheet, and returns the folder for them.
func fakePdftoppm(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script in place of pdftoppm")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nREFCOMPARE_FAKE_PDFTOPPM=1 exec '" + exe + "' -test.run='^TestFakePdftoppm$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pdftoppm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// writeSheet stores g as page (1-based) of the PDF at path.
func writeSheet(t *testing.T, path string, page int, g *region) {
	t.Helper()
	if err := os.WriteFile(fmt.Sprintf("%s.%d.ppm", path, page), ppm(&g.pageImage), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRenderRegion(t *testing.T) {
	dir := fakePdftoppm(t)
	path := filepath.Join(dir, "a.pdf")
	g := sheet(100, 60)
	g.rect(90, 50, 100, 60)
	writeSheet(t, path, 1, g)
	size := pageSize{100, 60}
	// A box that sticks out of the page is cut at the page.
	r, err := renderRegion(path, 0, size, 88.5, 49, 130, 90)
	if err != nil {
		t.Fatal(err)
	}
	if r.X != 708 || r.Y != 392 || r.W != 92 || r.H != 88 {
		t.Fatalf("region at %d,%d of %dx%d pixels, want 708,392 of 92x88", r.X, r.Y, r.W, r.H)
	}
	at := func(x, y int) uint8 { return r.Pix[3*((y-r.Y)*r.W+x-r.X)] }
	if at(719, 399) != 255 || at(720, 400) != 0 || at(799, 479) != 0 {
		t.Errorf("the box is not where it was asked for: %d %d %d, want 255 0 0", at(719, 399), at(720, 400), at(799, 479))
	}
	// A box off the page is empty, and pdftoppm, which would draw the
	// whole page, is not asked.
	for _, b := range [][4]float64{{120, 10, 140, 20}, {10, 70, 20, 90}, {-30, -30, -10, -10}, {99.5, 10, 140, 20}} {
		r, err := renderRegion(path, 0, size, b[0], b[1], b[2], b[3])
		if err != nil || r.W != 0 || r.H != 0 {
			t.Errorf("box %v off the page: %dx%d pixels, error %v, want none", b, r.W, r.H, err)
		}
	}
	// A page size that is not the PDF's: pdftoppm gives the whole page,
	// which is not taken for the box.
	if _, err := renderRegion(path, 0, pageSize{300, 300}, 150, 10, 160, 20); err == nil {
		t.Errorf("the whole page was taken for a box of 10x10 pt")
	}
	if _, err := renderRegion(path, 1, size, 10, 10, 20, 20); err == nil {
		t.Errorf("a page that cannot be drawn gives no error")
	}
}

func TestMeasureDrawn(t *testing.T) {
	dir := fakePdftoppm(t)
	refPath, candPath := filepath.Join(dir, "ref.pdf"), filepath.Join(dir, "cand.pdf")
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	ref, cand := sheet(200, 300), sheet(200, 300)
	// Six candidate words without a partner on page 1: a chart label
	// that the reference draws, one it draws 3 pt lower, one it does not
	// draw, one it draws with another digit, one that shares its place
	// with another word, and far below, in another strip of the page, a
	// second label it draws.
	at := cand.label("2022", 40, 30, labelHeight)
	refLabel(ref, "2022", 40.5, 30)
	low := cand.label("2023", 80, 30, labelHeight)
	refLabel(ref, "2023", 80, 33)
	none := cand.label("2024", 120, 30, labelHeight)
	other := cand.label("2025", 40, 60, labelHeight)
	refLabel(ref, "2026", 40, 60)
	twin := cand.label("2027", 80, 60, labelHeight)
	refLabel(ref, "2027", 80, 60)
	far := cand.label("2028", 150, 280, labelHeight)
	refLabel(ref, "2028", 150, 280.75)
	writeSheet(t, refPath, 1, ref)
	writeSheet(t, candPath, 1, cand)
	// A word on page 3, which only the candidate has.
	extra := at
	extra.Page = 2
	sizes := &pdfWords{Pages: []pageSize{{200, 300}, {200, 300}}}
	candSizes := &pdfWords{Pages: []pageSize{{200, 300}, {200, 300}, {200, 300}}}
	r := pdfResult{RefPages: 2, CandPages: 3, CandOnly: 7,
		Page: []pageStat{{Fit: labelFit, RefWords: 5, CandWords: 11, CandOnly: 6}, {Fit: labelFit}},
		CandOnlyWords: []loneWord{
			{word: at}, {word: low}, {word: none}, {word: other}, {word: twin, Shared: true}, {word: far}, {word: extra},
		},
		CandOnlySample: []located{{"2022", 1}, {"2023", 1}, {"2024", 1}, {"2025", 1}, {"2027", 1}}}
	measureDrawn(refPath, candPath, sizes, candSizes, &r, o)
	var left []string
	for _, w := range r.CandOnlyWords {
		left = append(left, w.Text)
	}
	if want := []string{"2023", "2024", "2025", "2027", "2022"}; !reflect.DeepEqual(left, want) {
		t.Errorf("words left without a partner %v, want %v", left, want)
	}
	if r.DrawnWords != 2 || r.CandOnly != 5 || r.Page[0].Drawn != 2 || r.Page[0].CandOnly != 4 || r.Page[0].DrawnErr != "" {
		t.Errorf("drawn %d, cand-only %d, page 1: %+v, want 2 drawn and 5 (4 on page 1) left", r.DrawnWords, r.CandOnly, r.Page[0])
	}
	// The labels are 0.5 and 0.75 pt off; a shape is found a quarter of
	// a point before it lies on the other.
	if s := r.Page[0].DrawnShift; s < 0.5 || s > 0.75 {
		t.Errorf("largest shift %.3f, want that of the label 0.75 pt off", s)
	}
	if want := []located{{"2023", 1}, {"2024", 1}, {"2025", 1}, {"2027", 1}, {"2022", 3}}; !reflect.DeepEqual(r.CandOnlySample, want) {
		t.Errorf("sample %v, want %v", r.CandOnlySample, want)
	}
	rep := renderReport([]row{{Name: "chart.hwp", PDF: &r}}, o, "cand", false)
	for _, want := range []string{
		"| chart.hwp | 2/3 | 0.00 | 0/5 | 0 | 0 | - | 0 |  | - | - | - | 2 words: p1 2 (shift up to 0.62 pt) | FAIL |",
		"- PDF: 2 words without a partner in cand drawn in the reference and not counted: p1 2 (shift up to 0.62 pt)",
		`- PDF: only in cand: "2023" p1, "2024" p1, "2025" p1, "2027" p1, "2022" p3`,
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q\n%s", want, rep)
		}
	}

	// Only drawn labels are left over: the document passes, and not
	// before the pictures were looked at.
	r = pdfResult{RefPages: 1, CandPages: 1, CandOnly: 2, Page: []pageStat{{Fit: labelFit, RefWords: 5, CandWords: 7, CandOnly: 2}},
		CandOnlyWords: []loneWord{{word: at}, {word: far}}}
	if r.pass(o) {
		t.Errorf("words without a partner pass before the reference picture was looked at")
	}
	measureDrawn(refPath, candPath, sizes, candSizes, &r, o)
	if !r.pass(o) || r.CandOnly != 0 || r.DrawnWords != 2 || len(r.CandOnlyWords) != 0 || len(r.CandOnlySample) != 0 {
		t.Errorf("two drawn labels: pass %v, %+v", r.pass(o), r)
	}

	// Two pages to a sheet: the right half of sheet 2 is page 4, and its
	// words are where they are on the sheet.
	writeSheet(t, refPath, 2, ref)
	writeSheet(t, candPath, 2, cand)
	at.Page, none.Page = 3, 3
	r = pdfResult{TwoUp: true, RefPages: 2, CandPages: 2, CandOnly: 2,
		Page:          []pageStat{{Fit: labelFit}, {Fit: labelFit}, {Fit: labelFit}, {Fit: labelFit, CandOnly: 2}},
		CandOnlyWords: []loneWord{{word: at}, {word: none}}}
	measureDrawn(refPath, candPath, sizes, sizes, &r, o)
	if r.DrawnWords != 1 || r.CandOnly != 1 || r.Page[3].Drawn != 1 || r.Page[3].CandOnly != 1 {
		t.Errorf("half 2R: drawn %d, cand-only %d, %+v, want 1 and 1", r.DrawnWords, r.CandOnly, r.Page[3])
	}

	// A page that cannot be drawn: the words stay, and the page says why.
	at.Page = 1
	r = pdfResult{RefPages: 2, CandPages: 2, CandOnly: 1, Page: []pageStat{{Fit: labelFit}, {Fit: labelFit, CandOnly: 1}},
		CandOnlyWords: []loneWord{{word: at}}}
	measureDrawn(refPath, filepath.Join(dir, "missing.pdf"), sizes, sizes, &r, o)
	if r.DrawnWords != 0 || r.CandOnly != 1 || r.Page[1].DrawnErr == "" {
		t.Errorf("page that cannot be drawn: drawn %d, cand-only %d, error %q", r.DrawnWords, r.CandOnly, r.Page[1].DrawnErr)
	}
	rep = renderReport([]row{{Name: "chart.hwp", PDF: &r}}, o, "cand", false)
	if want := "- PDF: page 2: the words without a partner could not be looked for in the reference picture: "; !strings.Contains(rep, want) {
		t.Errorf("report lacks %q\n%s", want, rep)
	}
}
