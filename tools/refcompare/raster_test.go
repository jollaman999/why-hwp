package main

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

// blank returns a white page of w x h points.
func blank(w, h int) *pageImage {
	g := &pageImage{W: w * inkPxPerPt, H: h * inkPxPerPt}
	g.Pix = bytes.Repeat([]byte{255}, 3*g.W*g.H)
	return g
}

// fill paints the rectangle from (x0, y0) to (x1, y1), in points.
func (g *pageImage) fill(x0, y0, x1, y1 float64, c [3]uint8) {
	for y := int(y0 * inkPxPerPt); y < int(y1*inkPxPerPt); y++ {
		for x := int(x0 * inkPxPerPt); x < int(x1*inkPxPerPt); x++ {
			copy(g.Pix[3*(y*g.W+x):], c[:])
		}
	}
}

var (
	black  = [3]uint8{0, 0, 0}
	blue   = [3]uint8{91, 127, 222}
	orange = [3]uint8{237, 125, 49}
)

// chart draws a frame, two bars of different hue that touch, and a small
// legend box, moved by (dx, dy) points.
func chart(dx, dy float64) *pageImage {
	g := blank(200, 120)
	g.fill(20+dx, 20+dy, 180+dx, 21+dy, black)
	g.fill(20+dx, 99+dy, 180+dx, 100+dy, black)
	g.fill(20+dx, 20+dy, 21+dx, 100+dy, black)
	g.fill(179+dx, 20+dy, 180+dx, 100+dy, black)
	g.fill(40+dx, 40+dy, 80+dx, 90+dy, blue)
	g.fill(80+dx, 55+dy, 120+dx, 90+dy, orange)
	g.fill(150+dx, 50+dy, 156+dx, 56+dy, blue)
	return g
}

func TestParsePPM(t *testing.T) {
	data := append([]byte("P6\n# made by a test\n2 1\n255\n"), 1, 2, 3, 250, 251, 252)
	g, err := parsePPM(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if g.W != 2 || g.H != 1 || !bytes.Equal(g.Pix, []byte{1, 2, 3, 250, 251, 252}) {
		t.Errorf("got %dx%d %v", g.W, g.H, g.Pix)
	}
	for name, bad := range map[string]string{
		"gray image":  "P5\n2 1\n255\n\x01\x02",
		"short data":  "P6\n2 1\n255\n\x01\x02\x03",
		"16-bit":      "P6\n1 1\n65535\n\x01\x02\x03\x04\x05\x06",
		"zero width":  "P6\n0 1\n255\n",
		"empty input": "",
	} {
		if _, err := parsePPM(strings.NewReader(bad)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestOutline(t *testing.T) {
	g := blank(10, 10)
	g.fill(2, 2, 6, 6, blue)
	g.fill(6, 2, 8, 6, orange)
	m := outline(g, inkStep)
	at := func(xpt, ypt float64) bool { return m[int(ypt*inkPxPerPt)*g.W+int(xpt*inkPxPerPt)] }
	// The dark side of an edge is marked up to inkReach pixels deep.
	if !at(2, 4) || !at(2.25, 4) || at(2.5, 4) {
		t.Errorf("left edge of the blue box: %v %v %v, want true true false", at(2, 4), at(2.25, 4), at(2.5, 4))
	}
	if at(1.75, 4) {
		t.Errorf("the white side of an edge is marked")
	}
	if at(4, 4) {
		t.Errorf("the inside of a fill is marked")
	}
	// Blue and orange differ by 3 in brightness and by 146 in red.
	if !at(5.75, 4) && !at(6, 4) {
		t.Errorf("no outline between two fills of the same brightness and another hue")
	}
	// A seam: two fills one value apart.
	h := blank(10, 10)
	h.fill(2, 2, 5, 6, [3]uint8{160, 160, 160})
	h.fill(5, 2, 8, 6, [3]uint8{159, 159, 159})
	hm := outline(h, inkStep)
	if hm[4*inkPxPerPt*h.W+5*inkPxPerPt] || hm[4*inkPxPerPt*h.W+5*inkPxPerPt-1] {
		t.Errorf("a step of 1 makes an outline")
	}
	// A faint line: a step of 36, below inkStep and above inkPartnerStep.
	f := blank(10, 10)
	f.fill(2, 2, 8, 2.25, [3]uint8{219, 219, 219})
	if n := count(outline(f, inkStep)); n != 0 {
		t.Errorf("a step of 36 makes %d outline pixels", n)
	}
	if n := count(outline(f, inkPartnerStep)); n == 0 {
		t.Errorf("a step of 36 cannot be a partner")
	}
}

func count(m []bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

func TestInkDiffShift(t *testing.T) {
	id := affine{S: 1}
	ref := chart(0, 0)
	cases := []struct {
		dx, dy   float64
		min, max float64
	}{
		{0, 0, 0, 0},
		{0.5, 0, 0, 0},
		{1, 1, 0, 0},       // at the tolerance
		{1.25, 0, 0.05, 1}, // just over it: the edges across the shift are alone
		{0, 2, 0.3, 1},     // most of the outline here runs across this shift
		{3, 3, 0.9, 1},
	}
	for _, c := range cases {
		got := inkDiff(ref, chart(c.dx, c.dy), id, 1)
		if got < c.min || got > c.max {
			t.Errorf("shift (%.2f, %.2f): diff %.3f, want %.2f to %.2f", c.dx, c.dy, got, c.min, c.max)
		}
	}
	// The same shift is nothing when the fit says the page moved.
	if got := inkDiff(ref, chart(3, 3), affine{S: 1, TX: 3, TY: 3}, 1); got != 0 {
		t.Errorf("shift taken out by the fit: diff %.3f, want 0", got)
	}
	// A larger tolerance accepts a larger shift.
	if got := inkDiff(ref, chart(0, 2), id, 2); got != 0 {
		t.Errorf("shift 2 at tolerance 2: diff %.3f, want 0", got)
	}
}

func TestInkDiffOneSided(t *testing.T) {
	id := affine{S: 1}
	if got := inkDiff(blank(50, 50), blank(50, 50), id, 1); got != 0 {
		t.Errorf("two blank pages: %.3f, want 0", got)
	}
	if got := inkDiff(chart(0, 0), blank(200, 120), id, 1); got != 1 {
		t.Errorf("ink in the reference only: %.3f, want 1", got)
	}
	if got := inkDiff(blank(200, 120), chart(0, 0), id, 1); got != 1 {
		t.Errorf("ink in the candidate only: %.3f, want 1", got)
	}

	// The candidate draws one thing more: the share of its outline that
	// is alone, and nothing of the reference's.
	extra := chart(0, 0)
	before := count(outline(extra, inkStep))
	extra.fill(30, 105, 60, 110, black)
	added := count(outline(extra, inkStep)) - before
	want := float64(added) / float64(before+added)
	if got := inkDiff(chart(0, 0), extra, id, 1); math.Abs(got-want) > 1e-9 || want < 0.05 {
		t.Errorf("extra box in the candidate: %.4f, want %.4f", got, want)
	}
	if got := inkDiff(extra, chart(0, 0), id, 1); math.Abs(got-want) > 1e-9 {
		t.Errorf("extra box in the reference: %.4f, want %.4f", got, want)
	}

	// A faint line on one side only (the edge pdftoppm draws around a
	// picture with a transparent border) is not ink.
	faint := chart(0, 0)
	faint.fill(10, 10, 190, 10.25, [3]uint8{219, 219, 219})
	if got := inkDiff(chart(0, 0), faint, id, 1); got != 0 {
		t.Errorf("faint line in the candidate: %.4f, want 0", got)
	}
}

func TestInkDiffEdgeNearTheStep(t *testing.T) {
	// The same box a little darker than inkStep on one page and a little
	// lighter on the other is the same box.
	a, b := blank(50, 50), blank(50, 50)
	a.fill(10, 10, 40, 40, [3]uint8{185, 185, 185}) // step 70
	b.fill(10, 10, 40, 40, [3]uint8{205, 205, 205}) // step 50
	if got := inkDiff(a, b, affine{S: 1}, 1); got != 0 {
		t.Errorf("diff %.3f, want 0", got)
	}
	if got := inkDiff(b, a, affine{S: 1}, 1); got != 0 {
		t.Errorf("diff %.3f the other way round, want 0", got)
	}
	// Below inkPartnerStep it is no partner.
	c := blank(50, 50)
	c.fill(10, 10, 40, 40, [3]uint8{230, 230, 230}) // step 25
	if got := inkDiff(a, c, affine{S: 1}, 1); got != 1 {
		t.Errorf("box against a much fainter box: diff %.3f, want 1", got)
	}
}

func TestInkDiffInsideEdgeMoved(t *testing.T) {
	// Only the border between the two bars moves by 3 pt. In gray the two
	// fills are 3 values apart and the move would not be seen.
	ref := chart(0, 0)
	cand := chart(0, 0)
	cand.fill(80, 55, 83, 90, blue)
	if got := inkDiff(ref, cand, affine{S: 1}, 1); got < 0.02 {
		t.Errorf("moved border between two hues: diff %.4f, want at least 0.02", got)
	}
}

// pictureDocs is a chart page: no words in the reference, nine in the
// candidate.
func pictureDocs() (ref, cand *pdfWords) {
	ref = &pdfWords{Pages: []pageSize{{595, 842}}}
	cand = testDoc(595, [][]string{{"판매", "1", "분기", "2", "분기", "3", "분기", "4", "분기"}})
	return ref, cand
}

func TestComparePDFPicturePage(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	ref, cand := pictureDocs()
	r := comparePDF(ref, cand, o)
	if len(r.Page) != 1 || !r.Page[0].Picture {
		t.Fatalf("pages %+v, want one picture page", r.Page)
	}
	if r.RefOnly != 0 || r.CandOnly != 0 || r.PictureWords != 9 || r.Page[0].CandOnly != 9 {
		t.Errorf("unmatched %d/%d, picture words %d, page cand-only %d, want 0/0, 9, 9",
			r.RefOnly, r.CandOnly, r.PictureWords, r.Page[0].CandOnly)
	}
	if r.pass(o) {
		t.Errorf("a picture page passes before its picture is compared")
	}
	r.Page[0].InkDone, r.Page[0].Ink = true, 0.0039
	if !r.pass(o) {
		t.Errorf("picture difference 0.39%% at limit 1%% fails")
	}
	r.Page[0].Ink = 0.0203
	if r.pass(o) {
		t.Errorf("picture difference 2.03%% at limit 1%% passes")
	}
	r.Page[0].InkDone, r.Page[0].InkErr = false, "pdftoppm: not found"
	if r.pass(o) {
		t.Errorf("a picture page that could not be drawn passes")
	}

	// The other way round, words in the reference only, is a candidate
	// that lost its text: not a picture page, and it fails on its words
	// however alike the pictures are.
	r = comparePDF(cand, ref, o)
	if r.Page[0].Picture || r.RefOnly != 9 || r.PictureWords != 0 {
		t.Errorf("words in the reference only: %v, picture %v, picture words %d", r, r.Page[0].Picture, r.PictureWords)
	}
	r.Page[0].InkDone = true
	if r.pass(o) {
		t.Errorf("a candidate without the reference's words passes")
	}
}

func TestComparePDFPicturePageIsPerPage(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	// Page 1 has words on both sides and one more in the candidate; page
	// 2 is a chart. Only the chart's words are set aside.
	ref := testDoc(595, [][]string{{"가", "나", "다"}, {}})
	cand := testDoc(595, [][]string{{"가", "나", "다", "라"}, {"판매", "분기"}})
	r := comparePDF(ref, cand, o)
	if r.Page[0].Picture || !r.Page[1].Picture {
		t.Fatalf("picture pages %v %v, want false true", r.Page[0].Picture, r.Page[1].Picture)
	}
	if r.CandOnly != 1 || r.PictureWords != 2 {
		t.Errorf("cand-only %d, picture words %d, want 1 and 2", r.CandOnly, r.PictureWords)
	}
	r.Page[1].InkDone = true
	if r.pass(o) {
		t.Errorf("a word without a partner on a page with words passes")
	}

	// A page that only one PDF has is not a picture page.
	ref = testDoc(595, [][]string{{"가", "나"}})
	cand = testDoc(595, [][]string{{"가", "나"}, {"다"}})
	r = comparePDF(ref, cand, o)
	if len(r.Page) != 1 || r.CandOnly != 1 || r.PictureWords != 0 || r.pass(o) {
		t.Errorf("extra candidate page: %v, pages %d, picture words %d", r, len(r.Page), r.PictureWords)
	}
}

func TestReportMarksPictureAndType3(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	ref, cand := pictureDocs()
	pic := comparePDF(ref, cand, o)
	pic.Page[0].InkDone, pic.Page[0].Ink = true, 0.0039
	bad := comparePDF(ref, cand, o)
	bad.Page[0].InkDone, bad.Page[0].Ink = true, 0.0203
	t3ref, t3cand := type3Pair(0, nil)
	t3 := comparePDF(t3ref, t3cand, o)
	plainDoc := testDoc(595, [][]string{{"가", "나", "다"}})
	plain := comparePDF(plainDoc, plainDoc, o)
	rows := []row{
		{Name: "chart.hwpx", PDF: &pic, Pass: pic.pass(o)},
		{Name: "chart2.hwpx", PDF: &bad, Pass: bad.pass(o)},
		{Name: "type3.hwp", PDF: &t3, Pass: t3.pass(o)},
		{Name: "plain.hwp", PDF: &plain, Pass: plain.pass(o)},
		{Name: "nomutool.hwp", PDF: &plain, Pass: plain.pass(o), BaseNote: errNoMutool.Error()},
	}
	rep := renderReport(rows, o, "cand", false)
	for _, want := range []string{
		"4 of 5 documents pass (1 of them by picture).",
		"| chart.hwpx | 1/1 | 0.00 | 0/0 | 0 | 0 | - | 0 |  | - | - | p1 0.39 %, 9 words not paired | pass (picture) |",
		"| chart2.hwpx | 1/1 | 0.00 | 0/0 | 0 | 0 | - | 0 |  | - | - | p1 2.03 %, 9 words not paired | FAIL (picture) |",
		"| type3.hwp | 1/1 | 0.00 | 0/0 | 0 | 0 | 1.0000 | 0 |  | - | 16 edges at baseline | - | pass |",
		"| plain.hwp | 1/1 | 0.00 | 0/0 | 0 | 0 | 1.0000 | 0 |  | - | - | - | pass |",
		"| nomutool.hwp | 1/1 | 0.00 | 0/0 | 0 | 0 | 1.0000 | 0 |  | - | box middle: mutool not found | - | pass |",
		"- PDF: page 1 is judged by its picture (words in cand only, 9 not paired): 2.03 % of the outline is more than 1.00 pt off, limit 1.00 %",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q\n%s", want, rep)
		}
	}
	if strings.Contains(rep, "Picture difference of every page") {
		t.Errorf("the page table is in a report without -ink-all")
	}

	o.InkAll = true
	plain.Page[0].InkDone, plain.Page[0].Ink = true, 0.0004
	rep = renderReport(rows[:4], o, "cand", false)
	for _, want := range []string{
		"## Picture difference of every page",
		"| chart.hwpx | 1 | 0/9 | 0/9 | 0.00 | 0.39 | picture |",
		"| plain.hwp | 1 | 3/3 | 0/0 | 0.00 | 0.04 | words |",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report with -ink-all lacks %q\n%s", want, rep)
		}
	}
}
