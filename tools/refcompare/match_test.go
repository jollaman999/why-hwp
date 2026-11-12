package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lcsLen is the textbook O(n*m) LCS length, used to check lcsPairs.
func lcsLen(a, b []string) int {
	t := make([][]int, len(a)+1)
	for i := range t {
		t[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	return t[0][0]
}

func TestLCSPairsIsLongestCommonSubsequence(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alpha := []string{"a", "b", "c", "d"}
	for n := 0; n < 300; n++ {
		a := make([]string, rng.Intn(25))
		b := make([]string, rng.Intn(25))
		for i := range a {
			a[i] = alpha[rng.Intn(len(alpha))]
		}
		for i := range b {
			b[i] = alpha[rng.Intn(len(alpha))]
		}
		pairs := lcsPairs(a, b)
		if want := lcsLen(a, b); len(pairs) != want {
			t.Fatalf("lcsPairs(%v, %v) has %d pairs, want %d", a, b, len(pairs), want)
		}
		for k, p := range pairs {
			if a[p[0]] != b[p[1]] {
				t.Fatalf("pair %v joins %q and %q", p, a[p[0]], b[p[1]])
			}
			if k > 0 && (p[0] <= pairs[k-1][0] || p[1] <= pairs[k-1][1]) {
				t.Fatalf("pairs not increasing: %v", pairs)
			}
		}
	}
}

func TestLCSPairsKeepsOrderAroundInsertions(t *testing.T) {
	a := strings.Fields("가 나 다 라 마")
	b := strings.Fields("가 X 나 다 Y 라 마")
	got := lcsPairs(a, b)
	want := [][2]int{{0, 0}, {1, 2}, {2, 3}, {3, 5}, {4, 6}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestFitPointsRecoversScaleAndShift(t *testing.T) {
	ref := []point{{10, 10}, {100, 20}, {50, 300}, {400, 700}}
	cand := make([]point, len(ref))
	for i, p := range ref {
		cand[i] = point{0.9*p.X + 3, 0.9*p.Y - 7}
	}
	f := fitPoints(ref, cand, fitScale)
	if !near(f.S, 0.9) || !near(f.TX, 3) || !near(f.TY, -7) {
		t.Errorf("scale fit = %+v, want S 0.9 TX 3 TY -7", f)
	}
	f = fitPoints(ref, cand, fitShift)
	if f.S != 1 {
		t.Errorf("shift fit scale = %v, want 1", f.S)
	}
	if f = fitPoints(ref, cand, fitNone); f != (affine{S: 1}) {
		t.Errorf("none fit = %+v, want identity", f)
	}
}

func TestFitPointsIgnoresOneMovedPoint(t *testing.T) {
	var ref, cand []point
	for i := 0; i < 20; i++ {
		p := point{float64(10 + 20*i), float64(50 + 30*i)}
		ref = append(ref, p)
		cand = append(cand, point{p.X + 5, p.Y + 5})
	}
	cand[7].Y += 40
	f := fitPoints(ref, cand, fitScale)
	if !near(f.S, 1) || !near(f.TX, 5) || !near(f.TY, 5) {
		t.Errorf("fit = %+v, want S 1 TX 5 TY 5 with the moved point dropped", f)
	}
}

// page builds a one-page document from words laid out on a line each.
func testDoc(pageW float64, pages [][]string) *pdfWords {
	d := &pdfWords{}
	for pi, words := range pages {
		d.Pages = append(d.Pages, pageSize{pageW, 842})
		for i, s := range words {
			y := 60 + 14*float64(i)
			x := 60 + 7*float64(i%5)
			d.Words = append(d.Words, word{Text: s, Page: pi, X0: x, Y0: y, X1: x + 30, Y1: y + 10})
		}
	}
	return d
}

func transform(d *pdfWords, s, tx, ty float64) *pdfWords {
	out := &pdfWords{Pages: d.Pages}
	for _, w := range d.Words {
		out.Words = append(out.Words, word{w.Text, w.Page, s*w.X0 + tx, s*w.Y0 + ty, s*w.X1 + tx, s*w.Y1 + ty})
	}
	return out
}

func TestComparePDFIdentical(t *testing.T) {
	d := testDoc(595, [][]string{{"샘플", "사업", "계획서"}, {"둘째", "쪽"}})
	opt := cmpOptions{Tol: 1, Fit: fitScale}
	r := comparePDF(d, d, opt)
	if !r.pass(opt) || r.MaxDev != 0 || r.Matched != 5 {
		t.Errorf("identical documents: %s, pass %v", r, r.pass(opt))
	}
}

func TestComparePDFScaledReference(t *testing.T) {
	cand := testDoc(595, [][]string{{"a", "b", "c", "d", "e", "f"}})
	ref := transform(cand, 0.8, 4, 9) // Hancom shrank the body for memos
	scaled := cmpOptions{Tol: 1, Fit: fitScale}
	if r := comparePDF(ref, cand, scaled); !r.pass(scaled) || r.MaxDev > 1e-6 {
		t.Errorf("scale fit: %s, want pass with no deviation", r)
	}
	none := cmpOptions{Tol: 1, Fit: fitNone}
	if r := comparePDF(ref, cand, none); r.pass(none) || r.MaxDev < 10 {
		t.Errorf("no fit: %s, want a failure with a large deviation", r)
	}
}

func TestComparePDFMeasuresOneShiftedWord(t *testing.T) {
	ref := testDoc(595, [][]string{{"a", "b", "c", "d", "e", "f", "g", "h"}})
	cand := transform(ref, 1, 0, 0)
	cand.Words[3].X0 += 2.5
	cand.Words[3].X1 += 2.5
	opt := cmpOptions{Tol: 1, Fit: fitScale}
	r := comparePDF(ref, cand, opt)
	if r.pass(opt) || r.Worst.Text != "d" || math.Abs(r.MaxDev-2.5) > 0.2 {
		t.Errorf("got %s worst %q, want a failure at d near 2.5pt", r, r.Worst.Text)
	}
	opt.Tol = 3
	if !comparePDF(ref, cand, opt).pass(opt) {
		t.Errorf("2.5pt deviation should pass with tolerance 3")
	}
}

func TestComparePDFUnmatchedAndMovedWords(t *testing.T) {
	ref := testDoc(595, [][]string{{"a", "b", "c"}, {"d", "e"}})
	cand := testDoc(595, [][]string{{"a", "b", "X", "c", "d"}, {"e"}})
	opt := cmpOptions{Tol: 100, Fit: fitShift}
	r := comparePDF(ref, cand, opt)
	if r.RefOnly != 0 || r.CandOnly != 1 || r.CandOnlySample[0].Text != "X" {
		t.Errorf("unmatched: ref-only %d cand-only %d %v, want 0, 1 [X]", r.RefOnly, r.CandOnly, r.CandOnlySample)
	}
	if r.PageMoves != 1 {
		t.Errorf("page moves = %d, want 1 (d moved to page 1)", r.PageMoves)
	}
	if r.pass(opt) {
		t.Errorf("result passed, want failure")
	}
}

func TestComparePDFSkipsMemoWords(t *testing.T) {
	cand := testDoc(595, [][]string{{"a", "b", "c", "d", "e"}})
	ref := transform(cand, 0.75, 0, 0)
	// A memo balloon to the right of the shrunken body.
	ref.Words = append(ref.Words, word{Text: "메모", Page: 0, X0: 470, Y0: 60, X1: 500, Y1: 70})
	opt := cmpOptions{Tol: 1, Fit: fitScale}
	if r := comparePDF(ref, cand, opt); r.RefOnly != 1 || r.pass(opt) {
		t.Errorf("without -skip-memo: ref-only %d, want 1 and a failure", r.RefOnly)
	}
	// An unscaled page has no memo area.
	plain := transform(cand, 1, 0, 0)
	plain.Words = append(plain.Words, word{Text: "메모", Page: 0, X0: 470, Y0: 60, X1: 500, Y1: 70})
	if r := comparePDF(plain, cand, cmpOptions{Tol: 1, Fit: fitScale, SkipMemo: true}); r.MemoSkipped != 0 || r.RefOnly != 1 {
		t.Errorf("unscaled page: memo %d ref-only %d, want 0 and 1", r.MemoSkipped, r.RefOnly)
	}
	opt.SkipMemo = true
	r := comparePDF(ref, cand, opt)
	if r.RefOnly != 0 || r.MemoSkipped != 1 || !r.pass(opt) {
		t.Errorf("with -skip-memo: ref-only %d memo %d pass %v, want 0, 1, true", r.RefOnly, r.MemoSkipped, r.pass(opt))
	}
	// A reference-only word inside the page is not memo text.
	ref.Words = append(ref.Words, word{Text: "본문", Page: 0, X0: 40, Y0: 400, X1: 70, Y1: 410})
	if r := comparePDF(ref, cand, opt); r.RefOnly != 1 || r.MemoSkipped != 1 {
		t.Errorf("body word: ref-only %d memo %d, want 1 and 1", r.RefOnly, r.MemoSkipped)
	}
}

func TestComparePDFPageCount(t *testing.T) {
	ref := testDoc(595, [][]string{{"a"}, {}})
	cand := testDoc(595, [][]string{{"a"}})
	opt := cmpOptions{Tol: 1, Fit: fitScale}
	if r := comparePDF(ref, cand, opt); r.pass(opt) {
		t.Errorf("2 vs 1 pages passed")
	}
}

const bboxSample = `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd"><html xmlns="http://www.w3.org/1999/xhtml">
<head>
<title></title>
<meta name="Producer" content="Hancom PDF"/>
</head>
<body>
<doc>
  <page width="595.000000" height="841.000000">
    <word xMin="68.123080" yMin="79.639765" xMax="83.713035" yMax="87.670988">샘플</word>
    <word xMin="87.672485" yMin="79.639765" xMax="103.262440" yMax="87.670988">R&amp;D</word>
  </page>
  <page width="842.000000" height="595.000000">
    <word xMin="1" yMin="2" xMax="3" yMax="4">&lt;b&gt;</word>
  </page>
</doc>
</body>
</html>
`

func TestParseBBox(t *testing.T) {
	d, err := parseBBox(strings.NewReader(bboxSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Pages) != 2 || d.Pages[1] != (pageSize{842, 595}) {
		t.Fatalf("pages = %v", d.Pages)
	}
	want := []word{
		{"샘플", 0, 68.123080, 79.639765, 83.713035, 87.670988},
		{"R&D", 0, 87.672485, 79.639765, 103.262440, 87.670988},
		{"<b>", 1, 1, 2, 3, 4},
	}
	if len(d.Words) != len(want) {
		t.Fatalf("words = %v", d.Words)
	}
	for i := range want {
		if d.Words[i] != want[i] {
			t.Errorf("word %d = %+v, want %+v", i, d.Words[i], want[i])
		}
	}
}

func TestCompareText(t *testing.T) {
	ref := []byte("\xef\xbb\xbf첫 줄\r\n\r\n둘째&#12539;줄  \r\n셋째 줄\r\n")
	cand := []byte("첫 줄\n둘째・줄\n\n\n넷째 줄\n")
	r := compareText(ref, cand)
	if r.RefLines != 3 || r.CandLines != 3 || r.RefOnly != 1 || r.CandOnly != 1 {
		t.Errorf("got %+v, want 3/3 lines and 1/1 different", r)
	}
	if r.FirstRefOnly != "셋째 줄" || r.FirstCandOnly != "넷째 줄" {
		t.Errorf("first differences %q / %q", r.FirstRefOnly, r.FirstCandOnly)
	}
	if !compareText(ref, []byte("첫 줄\n둘째・줄\n셋째 줄")).same() {
		t.Errorf("same text after blank-line and CRLF cleanup should be the same")
	}
}

func TestManifestFresh(t *testing.T) {
	dir := t.TempDir()
	m, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.hwp.pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.hwp.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.hwp.hwpx"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if m.fresh("a.hwp.pdf", "s1") {
		t.Errorf("unrecorded output is fresh")
	}
	for _, n := range []string{"a.hwp.pdf", "a.hwp.txt", "a.hwp.hwpx"} {
		if err := m.record(n, "s1"); err != nil {
			t.Fatal(err)
		}
	}
	m2, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m2.fresh("a.hwp.pdf", "s1") {
		t.Errorf("recorded output with the same input is not fresh")
	}
	if m2.fresh("a.hwp.pdf", "s2") {
		t.Errorf("output of a changed input is fresh")
	}
	if !m2.fresh("a.hwp.txt", "s1") {
		t.Errorf("empty text output is not fresh")
	}
	if m2.fresh("a.hwp.hwpx", "s1") {
		t.Errorf("empty hwpx output is fresh")
	}
}

func TestNewRemoteChecksEnvironment(t *testing.T) {
	cases := []struct {
		host, dir string
		ok        bool
	}{
		{"user@pc", "/mnt/c/Users/u/ref", true},
		{"", "/mnt/c/Users/u/ref", false},
		{"user@pc", "", false},
		{"-oProxyCommand=x", "/mnt/c/ref", false},
		{"user@pc", "/srv/ref", false},
		{"user@pc", "/mnt/c/a b", false},
		{"user@pc", "/mnt/c/a/../b", false},
		{"user@pc", "/mnt/c/a;rm", false},
	}
	for _, c := range cases {
		t.Setenv(envHost, c.host)
		t.Setenv(envDir, c.dir)
		r, err := newRemote(0, func(string, ...any) {})
		if (err == nil) != c.ok {
			t.Errorf("host %q dir %q: err %v, want ok %v", c.host, c.dir, err, c.ok)
		}
		if err == nil && r.work != c.dir+"/"+remoteSub {
			t.Errorf("work folder %q", r.work)
		}
	}
}

func TestComparePDFSplitWordAndSymbol(t *testing.T) {
	ref := &pdfWords{Pages: []pageSize{{595, 842}}, Words: []word{
		{"Ÿ", 0, 60, 100, 66, 110},
		{"온·오프라인", 0, 70, 100, 130, 110},
		{"\u00ad3D", 0, 140, 100, 160, 110},
		{"끝", 0, 170, 100, 180, 110},
		{"다음", 0, 60, 130, 80, 140},
		{"줄", 0, 84, 130, 94, 140},
	}}
	cand := &pdfWords{Pages: []pageSize{{595, 842}}, Words: []word{
		// Written in another order: the second line first.
		{"다음", 0, 60, 130, 80, 140},
		{"줄", 0, 84, 130, 94, 140},
		{"•", 0, 60, 100, 66, 110},
		{"온·", 0, 70, 100, 90, 110},
		{"오프라인", 0, 90.4, 100, 130.5, 110},
		{"-", 0, 140, 100, 145, 110},
		{"3D", 0, 146, 100, 160, 110},
		{"끝", 0, 170, 100, 180, 110},
	}}
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	r := comparePDF(ref, cand, opt)
	if r.RefOnly != 0 || r.CandOnly != 0 {
		t.Fatalf("ref-only %v cand-only %v, want none", r.RefOnlySample, r.CandOnlySample)
	}
	if r.Substituted != 1 || r.SubstSample[0] != `"Ÿ">"•" p1` {
		t.Errorf("substitutions %d %v, want Ÿ>•", r.Substituted, r.SubstSample)
	}
	if math.Abs(r.MaxDev-0.5) > 1e-9 || r.Worst.Text != "온·오프라인" {
		t.Errorf("max dev %.3f at %q, want 0.5 at the right edge of 온·오프라인", r.MaxDev, r.Worst.Text)
	}
	if !r.pass(opt) {
		t.Errorf("result failed: %s", r)
	}
}

func TestComparePDFLongReplacementIsUnmatched(t *testing.T) {
	ref := testDoc(595, [][]string{{"a", "xyz", "b"}})
	cand := testDoc(595, [][]string{{"a", "pqr", "b"}})
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	r := comparePDF(ref, cand, opt)
	if r.RefOnly != 1 || r.CandOnly != 1 || r.Substituted != 0 || r.pass(opt) {
		t.Errorf("got %s, want one unmatched word on each side", r)
	}
}

func TestReadingOrder(t *testing.T) {
	d := &pdfWords{Pages: []pageSize{{595, 842}, {595, 842}}, Words: []word{
		{"p2", 1, 10, 10, 20, 20},
		{"c", 0, 200, 51, 220, 61},
		{"d", 0, 10, 80, 20, 90},
		{"a", 0, 10, 50, 20, 60},
		{"b", 0, 100, 49, 120, 59},
		{"*", 0, 300, 50, 304, 56},
	}}
	var got []string
	for _, i := range readingOrder(d) {
		got = append(got, d.Words[i].Text)
	}
	if strings.Join(got, " ") != "a b c * d p2" {
		t.Errorf("reading order %v, want a b c * d p2", got)
	}
}

func TestSymbolSubst(t *testing.T) {
	cases := []struct {
		ref, cand string
		want      bool
	}{
		{"Ÿ", "•", true},
		{"ü", "✔", true},
		{"", "►", true},
		{"", "", true},
		{"0", "3", false},
		{"년", "(", false},
		{"ü", "u", false},
		{"", "함", false},
	}
	for _, c := range cases {
		if got := symbolSubst(c.ref, c.cand); got != c.want {
			t.Errorf("symbolSubst(%q, %q) = %v, want %v", c.ref, c.cand, got, c.want)
		}
	}
}

func TestComparePDFChangedDigitIsUnmatched(t *testing.T) {
	ref := testDoc(595, [][]string{{"합계", "100원"}})
	cand := testDoc(595, [][]string{{"합계", "300원"}})
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	r := comparePDF(ref, cand, opt)
	if r.Substituted != 0 || r.RefOnly != 1 || r.CandOnly != 1 || r.pass(opt) {
		t.Errorf("got %s, want the changed digit as one unmatched word each side", r)
	}
}

func TestComparePDFPairsTransposedWords(t *testing.T) {
	// Two table cells on one line: the reference has the right cell's text
	// slightly higher, so reading order puts it first.
	ref := &pdfWords{Pages: []pageSize{{595, 842}}, Words: []word{
		{"왼쪽", 0, 60, 100, 90, 110},
		{"오른쪽", 0, 300, 99, 340, 109},
		{"아래", 0, 60, 130, 90, 140},
	}}
	cand := &pdfWords{Pages: []pageSize{{595, 842}}, Words: []word{
		{"오른쪽", 0, 300, 95, 340, 105},
		{"왼쪽", 0, 60, 100, 90, 110},
		{"아래", 0, 60, 130, 90, 140},
	}}
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	r := comparePDF(ref, cand, opt)
	if r.RefOnly != 0 || r.CandOnly != 0 || r.Reordered != 1 {
		t.Fatalf("got %s reordered %d, want every word paired, one out of order", r, r.Reordered)
	}
	if math.Abs(r.MaxDev-4) > 1e-9 || r.Worst.Text != "오른쪽" {
		t.Errorf("max dev %.2f at %q, want 4 at 오른쪽", r.MaxDev, r.Worst.Text)
	}
}

func TestComparePDFWordMovedToAnotherPage(t *testing.T) {
	ref := testDoc(595, [][]string{{"a", "b", "c", "d", "e"}, {"f", "g", "h", "i", "j", "k"}})
	cand := testDoc(595, [][]string{{"a", "b", "c", "d"}, {"e", "f", "g", "h", "i", "j", "k"}})
	opt := cmpOptions{Tol: 1000, Fit: fitShift}
	r := comparePDF(ref, cand, opt)
	if r.PageMoves != 1 || r.RefOnly != 0 || r.CandOnly != 0 || r.pass(opt) {
		t.Errorf("got %s, want one word moved to another page and a failure", r)
	}
}

func TestComparePDFLongSymbolRunIsUnmatched(t *testing.T) {
	ref := testDoc(595, [][]string{{"a", "", "b"}})
	cand := testDoc(595, [][]string{{"a", "•••", "b"}})
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	if r := comparePDF(ref, cand, opt); r.Substituted != 0 || r.RefOnly != 1 || r.CandOnly != 1 {
		t.Errorf("got %s, want a run longer than maxSubst left unmatched", r)
	}
	ref = testDoc(595, [][]string{{"a", "", "b"}})
	cand = testDoc(595, [][]string{{"a", "••", "b"}})
	if r := comparePDF(ref, cand, opt); r.Substituted != 2 || r.RefOnly != 0 || r.CandOnly != 0 {
		t.Errorf("got %s, want two substitutions", r)
	}
}

// tableDoc is one page with a header line of words that occur once and a
// table of cells: rows from y 200, 14pt apart, columns from x 100, 60pt
// apart. A cell word is 6pt wide and 10pt high; cell gives its text, ""
// for an empty cell.
func tableDoc(rows, cols int, cell func(r, c int) string) *pdfWords {
	d := &pdfWords{Pages: []pageSize{{595, 842}}}
	for i, s := range []string{"표", "제목", "가나", "다라마", "바사"} {
		x := 100 + 60*float64(i)
		d.Words = append(d.Words, word{s, 0, x, 100, x + 20, 110})
	}
	for r := range rows {
		for c := range cols {
			if s := cell(r, c); s != "" {
				x, y := 100+60*float64(c), 200+14*float64(r)
				d.Words = append(d.Words, word{s, 0, x, y, x + 6, y + 10})
			}
		}
	}
	return d
}

// moveWords returns d with the words for which sel is true moved by dx, dy.
func moveWords(d *pdfWords, sel func(w word) bool, dx, dy float64) *pdfWords {
	out := &pdfWords{Pages: d.Pages}
	for _, w := range d.Words {
		if sel(w) {
			w.X0, w.X1, w.Y0, w.Y1 = w.X0+dx, w.X1+dx, w.Y0+dy, w.Y1+dy
		}
		out.Words = append(out.Words, w)
	}
	return out
}

func dashes(r, c int) string { return "-" }

func dashesAndZeros(r, c int) string {
	if (r+c)%3 == 0 {
		return "0"
	}
	return "-"
}

// cellAt selects the word of table cell (r, c) of tableDoc.
func cellAt(r, c int) func(w word) bool {
	x, y := 100+60*float64(c), 200+14*float64(r)
	return func(w word) bool { return w.X0 == x && w.Y0 == y }
}

func rowAt(r int) func(w word) bool {
	y := 200 + 14*float64(r)
	return func(w word) bool { return w.Y0 == y }
}

func TestComparePDFRepeatedWordsMoved(t *testing.T) {
	for _, fill := range []struct {
		name string
		cell func(r, c int) string
	}{{"dashes", dashes}, {"dashes and zeros", dashesAndZeros}} {
		ref := tableDoc(8, 5, fill.cell)
		for _, fit := range []string{fitScale, fitShift, fitNone} {
			opt := cmpOptions{Tol: 1, Fit: fit}
			name := fill.name + ", -fit " + fit
			if r := comparePDF(ref, ref, opt); !r.pass(opt) || r.MaxDev != 0 {
				t.Errorf("%s: same table: %s", name, r)
			}
			// One cell 3pt to the right: it still overlaps its own
			// place and no other cell is near.
			r := comparePDF(ref, moveWords(ref, cellAt(3, 2), 3, 0), opt)
			if r.pass(opt) || math.Abs(r.MaxDev-3) > 1e-9 || r.RefOnly != 0 || r.CandOnly != 0 {
				t.Errorf("%s: one cell 3pt right: %s, want a failure with 3pt", name, r)
			}
			// One row a row lower, on top of the next row: one of the two
			// rows there is 14pt from where it should be.
			r = comparePDF(ref, moveWords(ref, rowAt(3), 0, 14), opt)
			if r.pass(opt) || math.Abs(r.MaxDev-14) > 1e-9 || r.RefOnly != 0 || r.CandOnly != 0 {
				t.Errorf("%s: one row a row lower: %s, want a failure with 14pt", name, r)
			}
			// Rows 3 on a row lower, as if a row had been put in: the
			// words cannot all be paired with words at their places.
			from3 := func(w word) bool { return w.Y0 >= 200+14*3 }
			r = comparePDF(ref, moveWords(ref, from3, 0, 14), opt)
			if r.pass(opt) || r.MaxDev < 14 || r.RefOnly != 0 || r.CandOnly != 0 {
				t.Errorf("%s: rows from 3 a row lower: %s, want a failure with 14pt or more", name, r)
			}
			// One cell missing.
			cand := &pdfWords{Pages: ref.Pages}
			for _, w := range ref.Words {
				if !cellAt(2, 1)(w) {
					cand.Words = append(cand.Words, w)
				}
			}
			r = comparePDF(ref, cand, opt)
			if r.pass(opt) || r.RefOnly != 1 || r.CandOnly != 0 || r.RefOnlySample[0].Text != fill.cell(2, 1) {
				t.Errorf("%s: one cell missing: %s %v, want it unmatched", name, r, r.RefOnlySample)
			}
		}
	}
}

func TestComparePDFRepeatedWordMovedFarFromItsPlace(t *testing.T) {
	// The candidate puts the dash of some cells 6.5pt to the left, off
	// its place; every other cell holds a dash too. The deviation is the
	// 6.5pt to the dash's own new place, not the distance to a dash in a
	// cell far away.
	ref := tableDoc(8, 5, dashes)
	moved := func(w word) bool { return w.X0 == 100+60*2 && w.Y0 >= 200+14*5 }
	cand := transform(moveWords(ref, moved, -6.5, 0), 1.27, 3, -8)
	opt := cmpOptions{Tol: 1, Fit: fitScale}
	r := comparePDF(ref, cand, opt)
	if r.pass(opt) || math.Abs(r.MaxDev-6.5*1.27) > 1e-6 || r.RefOnly != 0 || r.CandOnly != 0 {
		t.Errorf("got %s, want a failure with %.3fpt", r, 6.5*1.27)
	}
	if math.Abs(r.Page[0].Fit.S-1.27) > 1e-9 {
		t.Errorf("fit scale %.6f, want 1.27", r.Page[0].Fit.S)
	}
	// The candidate repeats the header words once more at the foot of the
	// page, where the reference has nothing. They are not once on both
	// pages, so the first fit does not take them: it comes from the
	// reading-order pairs, and the result is the same.
	foot := &pdfWords{Pages: cand.Pages, Words: append([]word(nil), cand.Words...)}
	for _, w := range cand.Words[:5] {
		w.Y0, w.Y1 = w.Y0+600, w.Y1+600
		foot.Words = append(foot.Words, w)
	}
	r = comparePDF(ref, foot, opt)
	if math.Abs(r.MaxDev-6.5*1.27) > 1e-6 || r.RefOnly != 0 || r.CandOnly != 5 {
		t.Errorf("header repeated at the foot: got %s, want %.3fpt and the five words unmatched", r, 6.5*1.27)
	}
}

func TestComparePDFRepeatedWordsInAnotherReadingOrder(t *testing.T) {
	// A fraction: two dashes sit 6pt above the others on the line. In the
	// reference a tall word (a Type 3 box) holds the line together; the
	// candidate's box for that word is short, so its reading order puts
	// the raised dashes on a line of their own, before the rest. Paired in
	// reading order, every dash would take another dash's partner.
	line := func(tall float64) *pdfWords {
		d := tableDoc(0, 0, nil)
		d.Words = append(d.Words, word{"함수", 0, 100, 305 - tall/2, 130, 305 + tall/2})
		for _, x := range []float64{140, 160, 180} {
			d.Words = append(d.Words, word{"-", 0, x, 300, x + 6, 310})
		}
		for _, x := range []float64{200, 220} {
			d.Words = append(d.Words, word{"-", 0, x, 294, x + 6, 304})
		}
		return d
	}
	ref, cand := line(30), line(10)
	var order []string
	for _, i := range readingOrder(cand) {
		if cand.Words[i].Y0 > 200 {
			order = append(order, fmt.Sprintf("%s@%.0f", cand.Words[i].Text, cand.Words[i].X0))
		}
	}
	if got := strings.Join(order, " "); got != "-@200 -@220 함수@100 -@140 -@160 -@180" {
		t.Fatalf("candidate reading order %s: the case below tests nothing", got)
	}
	for _, fit := range []string{fitScale, fitNone} {
		opt := cmpOptions{Tol: 1, Fit: fit}
		r := comparePDF(ref, transform(cand, 1, 0, 0), opt)
		if !r.pass(opt) || r.MaxDev > 1e-9 {
			t.Errorf("-fit %s: got %s, want every dash at its place", fit, r)
		}
	}
}

func TestComparePDFSplitWordAtItsPlace(t *testing.T) {
	// The reference has "fx=" as one word, the candidate "f", "x" and "="
	// (one PDF starts a word where a font changes), and the reading orders
	// differ as in TestComparePDFRepeatedWordsInAnotherReadingOrder.
	ref := tableDoc(0, 0, nil)
	cand := tableDoc(0, 0, nil)
	ref.Words = append(ref.Words, word{"함수", 0, 60, 290, 90, 320}, word{"fx=", 0, 100, 300, 130, 310}, word{"x", 0, 140, 300, 146, 310},
		word{"-", 0, 200, 294, 206, 304})
	cand.Words = append(cand.Words, word{"함수", 0, 60, 300, 90, 310}, word{"f", 0, 100, 300, 106, 310}, word{"x", 0, 110, 300, 116, 310},
		word{"=", 0, 120, 300, 130, 310}, word{"x", 0, 140, 300, 146, 310}, word{"-", 0, 200, 294, 206, 304})
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	r := comparePDF(ref, cand, opt)
	if !r.pass(opt) || r.MaxDev > 1e-9 || r.Matched != len(ref.Words) {
		t.Errorf("got %s, want every word paired at its place", r)
	}
	// Different words that only overlap are not a split word: "5)" on top
	// of "7)" shares the ")" but is not paired by place.
	ref = tableDoc(0, 0, nil)
	cand = tableDoc(0, 0, nil)
	ref.Words = append(ref.Words, word{"5)", 0, 100, 300, 112, 310}, word{"7)", 0, 100, 312, 112, 322})
	cand.Words = append(cand.Words, word{"7)", 0, 100, 300, 112, 310})
	r = comparePDF(ref, cand, opt)
	if r.RefOnly != 1 || r.RefOnlySample[0].Text != "5)" || math.Abs(r.MaxDev-12) > 1e-9 {
		t.Errorf("got %s %v, want 5) unmatched and 7) 12pt off", r, r.RefOnlySample)
	}
}

func TestComparePDFBlockOfRepeatedWordsMoved(t *testing.T) {
	// Footnotes two lines higher: every line moves by 25pt. A word that
	// repeats from line to line may land on the place of another instance
	// of itself and be paired with it, but the pairing is one to one, so
	// the instances left over show the move: when every instance moves by
	// the same distance, the pairs move by that much on average, and the
	// largest deviation is never below it.
	lines := [][]string{{"5)", "여기서의", "기대효과"}, {"6)", "다양한", "대한", "제품의"}, {"7)", "업체에서", "대한", "제품의"}, {"8)", "재정", "대한"}}
	ref := tableDoc(0, 0, nil)
	for i := range 16 {
		for k := range 6 {
			x, y := 60+50*float64(k), 300+20*float64(i)
			ref.Words = append(ref.Words, word{fmt.Sprintf("본문%d", 6*i+k), 0, x, y, x + 30, y + 10})
		}
	}
	for i, l := range lines {
		for k, s := range l {
			x, y := 60+50*float64(k), 700+12.5*float64(i)
			ref.Words = append(ref.Words, word{s, 0, x, y, x + 30, y + 10})
		}
	}
	notes := func(w word) bool { return w.Y0 >= 700 }
	cand := moveWords(ref, notes, 0, -25)
	for _, fit := range []string{fitScale, fitShift, fitNone} {
		opt := cmpOptions{Tol: 1, Fit: fit}
		r := comparePDF(ref, cand, opt)
		if r.pass(opt) || r.MaxDev < 25-1e-6 || r.RefOnly != 0 || r.CandOnly != 0 {
			t.Errorf("-fit %s: got %s, want a failure with 25pt or more and every word paired", fit, r)
		}
	}
}

func TestComparePDFSubstitutionAtItsPlace(t *testing.T) {
	// A symbol font code against a plain minus at the same place, with the
	// reading orders as in TestComparePDFRepeatedWordsInAnotherReadingOrder.
	ref := tableDoc(0, 0, nil)
	cand := tableDoc(0, 0, nil)
	ref.Words = append(ref.Words, word{"함수", 0, 60, 290, 90, 320}, word{"a", 0, 100, 300, 106, 310}, word{"", 0, 110, 300, 116, 310},
		word{"b", 0, 200, 294, 206, 304})
	cand.Words = append(cand.Words, word{"함수", 0, 60, 300, 90, 310}, word{"a", 0, 100, 300, 106, 310}, word{"-", 0, 110, 300, 116, 310},
		word{"b", 0, 200, 294, 206, 304})
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	r := comparePDF(ref, cand, opt)
	if !r.pass(opt) || r.Substituted != 1 || r.MaxDev > 1e-9 {
		t.Errorf("got %s, want one substitution at its place", r)
	}
}

func TestAssignIsCheapest(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	var best func(cost [][]float64, row int, used []bool) float64
	best = func(cost [][]float64, row int, used []bool) float64 {
		if row == len(cost) {
			return 0
		}
		b := math.Inf(1)
		for j := range cost[row] {
			if !used[j] {
				used[j] = true
				b = math.Min(b, cost[row][j]+best(cost, row+1, used))
				used[j] = false
			}
		}
		return b
	}
	for n := 1; n <= 5; n++ {
		for m := 1; m <= 5; m++ {
			for range 20 {
				cost := make([][]float64, n)
				for i := range cost {
					cost[i] = make([]float64, m)
					for j := range cost[i] {
						cost[i][j] = float64(rng.Intn(20))
					}
				}
				got := assign(cost)
				sum, seen, given := 0.0, map[int]bool{}, 0
				for i, j := range got {
					if j < 0 {
						continue
					}
					if seen[j] {
						t.Fatalf("%dx%d: column %d given twice: %v", n, m, j, got)
					}
					seen[j] = true
					sum += cost[i][j]
					given++
				}
				if given != min(n, m) {
					t.Fatalf("%dx%d: %d rows given a column, want %d", n, m, given, min(n, m))
				}
				var want float64
				if n <= m {
					want = best(cost, 0, make([]bool, m))
				} else {
					tr := make([][]float64, m)
					for j := range tr {
						tr[j] = make([]float64, n)
						for i := range n {
							tr[j][i] = cost[i][j]
						}
					}
					want = best(tr, 0, make([]bool, n))
				}
				if sum != want {
					t.Fatalf("%dx%d %v: sum %v, want %v", n, m, cost, sum, want)
				}
			}
		}
	}
}

func TestComparePDFRepeatedWordsMovedTogether(t *testing.T) {
	ref := tableDoc(8, 5, dashes)
	from3 := func(w word) bool { return w.Y0 >= 200+14*3 }
	row3 := rowAt(3)
	cases := []struct {
		name   string
		cand   *pdfWords
		maxDev float64
	}{
		// Each dash of the row lands between its own place and the
		// next cell's: it is 40pt from its own place and 20pt from the
		// next cell's. Taking the nearer stranger first would leave the
		// first dash 160pt from the last free one.
		{"row 3 40pt to the right", moveWords(ref, row3, 40, 0), 40},
		// Rows from 3 down by 0.6 of a row: every dash is 8.4pt below its
		// place and 5.6pt above the place of the dash below it.
		{"rows from 3 8.4pt lower", moveWords(ref, from3, 0, 8.4), 8.4},
	}
	for _, c := range cases {
		opt := cmpOptions{Tol: 1, Fit: fitNone}
		r := comparePDF(ref, c.cand, opt)
		if r.pass(opt) || math.Abs(r.MaxDev-c.maxDev) > 1e-9 || r.RefOnly != 0 || r.CandOnly != 0 {
			t.Errorf("%s: got %s, want a failure with %.1fpt", c.name, r, c.maxDev)
		}
	}
}

func TestComparePDFOverlappingRepeatedWords(t *testing.T) {
	// Two "1" whose boxes overlap: each also overlaps the other's partner,
	// and the nearer is its own.
	ref := tableDoc(0, 0, nil)
	ref.Words = append(ref.Words, word{"1", 0, 100, 300, 106, 310}, word{"1", 0, 104, 300, 110, 310})
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	if r := comparePDF(ref, ref, opt); !r.pass(opt) || r.MaxDev != 0 {
		t.Errorf("got %s, want both at their places", r)
	}
	// The candidate puts the first 3pt lower and the second 2pt higher,
	// so its reading order has the second first. Each is still nearest
	// its own place: 3pt and 2pt off, not 4pt across.
	cand := tableDoc(0, 0, nil)
	cand.Words = append(cand.Words, word{"1", 0, 100, 303, 106, 313}, word{"1", 0, 104, 298, 110, 308})
	if r := comparePDF(ref, cand, opt); math.Abs(r.MaxDev-3) > 1e-9 || r.RefOnly != 0 || r.CandOnly != 0 {
		t.Errorf("got %s, want 3pt", r)
	}
}

func TestComparePDFRepeatedWordsPairedByDistanceBothWays(t *testing.T) {
	// Two dashes off their places: the first 10pt to the right, the second
	// 10pt to the left and 10pt down. Across alone, each would be at the
	// other's place.
	ref := tableDoc(0, 0, nil)
	ref.Words = append(ref.Words, word{"-", 0, 100, 300, 106, 310}, word{"-", 0, 110, 330, 116, 340})
	cand := tableDoc(0, 0, nil)
	cand.Words = append(cand.Words, word{"-", 0, 110, 300, 116, 310}, word{"-", 0, 100, 340, 106, 350})
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	if r := comparePDF(ref, cand, opt); math.Abs(r.MaxDev-10) > 1e-9 || r.RefOnly != 0 || r.CandOnly != 0 {
		t.Errorf("got %s, want 10pt", r)
	}
}

func TestComparePDFShortWordOnALongOne(t *testing.T) {
	// A one-letter word on top of a long different word is not that word
	// split. It stays free for the reference's own "의", 40pt lower, and
	// that move is measured.
	ref := tableDoc(0, 0, nil)
	ref.Words = append(ref.Words, word{"주사부위의통증,", 0, 100, 300, 180, 310}, word{"의", 0, 140, 340, 150, 350})
	cand := tableDoc(0, 0, nil)
	cand.Words = append(cand.Words, word{"의", 0, 140, 300, 150, 310})
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	r := comparePDF(ref, cand, opt)
	if r.RefOnly != 1 || r.RefOnlySample[0].Text != "주사부위의통증," || r.CandOnly != 0 || math.Abs(r.MaxDev-40) > 1e-9 {
		t.Errorf("got %s %v, want the long word unmatched and 의 40pt off", r, r.RefOnlySample)
	}
}

func TestComparePDFSplitWordMoved(t *testing.T) {
	// The candidate splits the word and puts it 40pt lower, off its place:
	// reading order pairs it, and its move is measured.
	ref := tableDoc(0, 0, nil)
	cand := tableDoc(0, 0, nil)
	ref.Words = append(ref.Words, word{"온·오프라인", 0, 100, 300, 160, 310}, word{"끝", 0, 100, 400, 110, 410})
	cand.Words = append(cand.Words, word{"온·", 0, 100, 340, 120, 350}, word{"오프라인", 0, 120, 340, 160, 350}, word{"끝", 0, 100, 400, 110, 410})
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	r := comparePDF(ref, cand, opt)
	if r.pass(opt) || r.RefOnly != 0 || r.CandOnly != 0 || math.Abs(r.MaxDev-40) > 1e-9 {
		t.Errorf("got %s, want every word paired and a failure with 40pt", r)
	}
}

func TestComparePDFWorstOfEqualDeviationsInReadingOrder(t *testing.T) {
	// "a" (narrow, off its place) and "b" (wide, still at its place) both
	// move by 3pt. The first in reading order is reported, whichever way
	// each was paired.
	ref := tableDoc(0, 0, nil)
	ref.Words = append(ref.Words, word{"a", 0, 100, 300, 102, 310}, word{"b", 0, 200, 300, 230, 310})
	cand := moveWords(ref, func(w word) bool { return w.Text == "a" }, 3, 0)
	cand = moveWords(cand, func(w word) bool { return w.Text == "b" }, 0, 3)
	opt := cmpOptions{Tol: 1, Fit: fitNone}
	r := comparePDF(ref, cand, opt)
	if math.Abs(r.MaxDev-3) > 1e-9 || r.Worst.Text != "a" {
		t.Errorf("got %s at %q, want 3pt at a", r, r.Worst.Text)
	}
}
