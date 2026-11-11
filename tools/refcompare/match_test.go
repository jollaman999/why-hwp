package main

import (
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
