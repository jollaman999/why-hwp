package main

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseType3Names(t *testing.T) {
	out := `name                                 type              encoding         emb sub uni object ID
------------------------------------ ----------------- ---------------- --- --- --- ---------
INPILL+HCRBatang                     CID TrueType      Identity-H       yes yes yes     12  0
T3                                   Type 3            Custom           yes no  yes     15  0
My Font                              Type 3            Custom           yes no  no      37  0
T3                                   Type 3            Custom           yes no  yes     40  0
Type3Like                            Type 1            WinAnsi          no  no  no      41  0
`
	got := parseType3Names(strings.NewReader(out))
	want := []string{"T3", "My Font"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestParseStext(t *testing.T) {
	// A control character reference (&#x1f;) is in real mutool output and
	// is not allowed in XML.
	out := `<?xml version="1.0"?>
<document filename="a.pdf">
<page id="page1" width="595" height="841">
<block bbox="0 0 1 1" justify="unknown">
<line bbox="0 0 1 1" wmode="0" dir="1 0" flags="0" text="&#xccab;">
<font name="T3" size="11.2">
<char quad="85 212 95 212 85 223 95 223" x="85.03" y="221.99" bidi="0" color="#000000" alpha="#ff" flags="16" c="&#xccab;"/>
<char quad="95 212 99 212 95 223 99 223" x="95.5" y="221.99" bidi="0" color="#000000" alpha="#ff" flags="16" c="&#x1f;"/>
</font>
<font name="HCRBatang" size="10">
<char quad="1 1 2 2 1 1 2 2" x="100.25" y="222.5" bidi="0" color="#000000" alpha="#ff" flags="16" c="&amp;"/>
</font>
</line>
</block>
</page>
<page id="page2" width="595" height="841">
<font name="T3" size="11.2">
<char quad="1 1 2 2 1 1 2 2" x="10" y="20" bidi="0" color="#000000" alpha="#ff" flags="16" c="&quot;"/>
</font>
</page>
</document>
`
	got, err := parseStext(strings.NewReader(out), []string{"T3"})
	if err != nil {
		t.Fatal(err)
	}
	want := []glyph{
		{0, 85.03, 221.99, '첫', "T3", true},
		{0, 95.5, 221.99, 0x1f, "T3", true},
		{0, 100.25, 222.5, '&', "HCRBatang", false},
		{1, 10, 20, '"', "T3", true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d glyphs %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("glyph %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if _, err := parseStext(strings.NewReader("<char x=\"1\" y=\"2\" c=\"a\"/>\n"), nil); err == nil {
		t.Errorf("a character outside a page was accepted")
	}
	if _, err := parseStext(strings.NewReader("<page id=\"p\">\n<char x=\"1\" c=\"a\"/>\n"), nil); err == nil {
		t.Errorf("a character without y was accepted")
	}
}

// type3Box is the box pdftotext makes for a Type 3 word: it takes 0.95 and
// 0.35 of a made-up font size above and below the baseline.
func type3Box(base, size float64) (y0, y1 float64) {
	return base - 0.95*size, base + 0.35*size
}

func TestAttachBaselines(t *testing.T) {
	// Three lines 10 pt apart start with the same character at the same
	// x. The Type 3 boxes are 14.3 pt high, so the box of a line also
	// holds the baseline of the line above. On the first line that
	// character is inside the word "가나" of the body font, which starts
	// further left, so nothing claims it before line two.
	d := &pdfWords{Pages: []pageSize{{595, 842}}}
	var glyphs []glyph
	add := func(text string, x, y0, y1 float64) {
		d.Words = append(d.Words, word{Text: text, Page: 0, X0: x, Y0: y0, X1: x + 20, Y1: y1})
	}
	add("가나", 90, 92, 102) // body font, baseline 100
	glyphs = append(glyphs, glyph{0, 90, 100, '가', "Body", false}, glyph{0, 100, 100, '나', "T5", true})
	for i, base := range []float64{110, 120} {
		y0, y1 := type3Box(base, 11)
		add("나다", 100, y0, y1)
		glyphs = append(glyphs, glyph{0, 100.004, base, '나', "T5", true}, glyph{0, 110, base, '다', "T5", true})
		_ = i
	}
	// A Type 3 word alone on its line teaches where the baseline is in a
	// T5 box.
	y0, y1 := type3Box(200, 11)
	add("라", 300, y0, y1)
	glyphs = append(glyphs, glyph{0, 300, 200, '라', "T5", true})
	// A body word whose second glyph is Type 3, and a word with no glyph.
	add("(마", 400, 292, 302)
	glyphs = append(glyphs, glyph{0, 400, 300, '(', "Body", false}, glyph{0, 405, 300, '마', "T5", true})
	add("바", 500, 292, 302)

	if n := attachBaselines(d, glyphs); n != 5 {
		t.Errorf("%d words got a baseline, want 5", n)
	}
	want := []wordBase{
		{100, true, true}, // holds the Type 3 glyph 나
		{110, true, true}, // not 100, the baseline of the line above
		{120, true, true}, // not 110
		{200, true, true},
		{300, true, true},
		{},
	}
	for i, w := range want {
		if got := d.Base[i]; got != w {
			t.Errorf("word %d %q: base %+v, want %+v", i, d.Words[i].Text, got, w)
		}
	}
}

func TestAttachBaselinesLearnsTheFontsFraction(t *testing.T) {
	// A font whose baseline is in the middle of its 20 pt box, on lines 9
	// pt apart: the box of the middle word holds three baselines. The one
	// below is nearer to the usual place of a baseline than the word's
	// own; the lone word of the same font tells which is right.
	d := &pdfWords{Pages: []pageSize{{595, 842}}, Words: []word{
		{"x", 0, 50, 190, 60, 210},  // baseline 200
		{"a", 0, 100, 99, 110, 119}, // baseline 109
	}}
	glyphs := []glyph{
		{0, 50, 200, 'x', "Sym", false},
		{0, 100, 100, 'a', "Sym", false},
		{0, 100, 109, 'a', "Sym", false},
		{0, 100, 118, 'a', "Sym", false},
	}
	attachBaselines(d, glyphs)
	if got := d.Base[1]; !got.OK || got.Y != 109 {
		t.Errorf("base %+v, want 109", got)
	}
}

// type3Pair builds a reference whose words are drawn with a Type 3 font
// and a candidate that draws the same words with a normal font. The
// baselines are the same; the candidate's baselines are moved down by drop
// for the words in moved.
func type3Pair(drop float64, moved map[int]bool) (ref, cand *pdfWords) {
	ref = &pdfWords{Pages: []pageSize{{595, 842}}}
	cand = &pdfWords{Pages: []pageSize{{595, 842}}}
	texts := []string{"첫째항과", "공비가", "모두", "양수", "인", "등비수열", "이", "만족시킬"}
	for i, s := range texts {
		x := 60 + 50*float64(i%4)
		base := 100 + 20*float64(i/4)
		// Reference: one font gets a 30 pt box, the next a 7 pt box, as
		// pdftotext does for two Type 3 fonts of the same 11 pt text.
		size := 23.0
		if i%2 == 1 {
			size = 5.4
		}
		y0, y1 := type3Box(base, size)
		ref.Words = append(ref.Words, word{Text: s, Page: 0, X0: x, Y0: y0, X1: x + 40, Y1: y1})
		ref.Base = append(ref.Base, wordBase{Y: base, OK: true, Type3: true})
		cb := base
		if moved[i] {
			cb += drop
		}
		cand.Words = append(cand.Words, word{Text: s, Page: 0, X0: x, Y0: cb - 9.2, X1: x + 40, Y1: cb + 2.3})
		cand.Base = append(cand.Base, wordBase{Y: cb, OK: true})
	}
	return ref, cand
}

func TestComparePDFType3WordsAtBaseline(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale}
	ref, cand := type3Pair(0, nil)
	r := comparePDF(ref, cand, o)
	if !r.pass(o) || r.MaxDev > 1e-6 {
		t.Errorf("same baselines: %v, want deviation 0", r)
	}
	if r.BaseEdges != 16 || r.BoxEdges != 0 {
		t.Errorf("%d edges at baseline, %d at box middle, want 16 and 0", r.BaseEdges, r.BoxEdges)
	}

	// Without baselines the same words are compared at the box middles,
	// which are 3.45 pt (30 pt box) and 0.19 pt (7 pt box) from the
	// candidate's although the glyphs are in the same place.
	ref.Base, cand.Base = nil, nil
	r = comparePDF(ref, cand, o)
	if r.pass(o) || r.BaseEdges != 0 {
		t.Errorf("box middles: %v with %d baseline edges, want a failure and 0", r, r.BaseEdges)
	}
}

func TestComparePDFType3WordMovedDownStillFails(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale}
	ref, cand := type3Pair(2, map[int]bool{5: true})
	r := comparePDF(ref, cand, o)
	if r.pass(o) {
		t.Fatalf("a Type 3 word 2 pt too low passes: %v", r)
	}
	if math.Abs(r.MaxDev-2) > 0.3 || r.Worst.Text != "등비수열" {
		t.Errorf("max dev %.2f at %q, want about 2 at 등비수열", r.MaxDev, r.Worst.Text)
	}

	// Every word 2 pt lower is a shifted page, which the fit takes out,
	// as it does for other fonts.
	all := map[int]bool{}
	for i := 0; i < 8; i++ {
		all[i] = true
	}
	ref, cand = type3Pair(2, all)
	if r := comparePDF(ref, cand, o); !r.pass(o) {
		t.Errorf("all words moved alike: %v, want pass", r)
	}
	if r := comparePDF(ref, cand, cmpOptions{Tol: 1, Fit: fitNone}); r.pass(cmpOptions{Tol: 1}) || math.Abs(r.MaxDev-2) > 1e-6 {
		t.Errorf("all words moved alike, no fit: %v, want max dev 2", r)
	}
}

func TestComparePDFType3WordMovedSidewaysStillFails(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale}
	ref, cand := type3Pair(0, nil)
	cand.Words[2].X0 += 2
	cand.Words[2].X1 += 2
	r := comparePDF(ref, cand, o)
	if r.pass(o) || math.Abs(r.MaxDev-2) > 0.3 || r.Worst.Text != "모두" {
		t.Errorf("%v at %q, want max dev about 2 at 모두", r, r.Worst.Text)
	}
}

func TestComparePDFType3WordWithoutBaseline(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale}
	ref, cand := type3Pair(0, nil)
	cand.Base[0].OK = false
	r := comparePDF(ref, cand, o)
	if r.BoxEdges != 2 || r.BaseEdges != 14 {
		t.Errorf("%d edges at box middle, %d at baseline, want 2 and 14", r.BoxEdges, r.BaseEdges)
	}
	if r.pass(o) || r.Worst.Text != "첫째항과" {
		t.Errorf("%v at %q, want the box middle of 첫째항과 to fail", r, r.Worst.Text)
	}
}

// splitPair is the word that pdftotext splits in the reference because
// "(" is drawn with a Type 3 font: "(" and "상위" against "(상위". The
// candidate box comes from the font of "(", which sits 1.85 pt lower than
// the box of the reference "상위". Twelve words that are the same on both
// sides surround it.
func splitPair(candDrop float64, withBase bool) (ref, cand *pdfWords) {
	ref = &pdfWords{Pages: []pageSize{{595, 842}}}
	cand = &pdfWords{Pages: []pageSize{{595, 842}}}
	both := func(text string, x, base float64) {
		w := word{Text: text, Page: 0, X0: x, Y0: base - 10.4, X1: x + 26, Y1: base + 2.6}
		ref.Words = append(ref.Words, w)
		cand.Words = append(cand.Words, w)
		ref.Base = append(ref.Base, wordBase{Y: base, OK: true})
		cand.Base = append(cand.Base, wordBase{Y: base, OK: true})
	}
	for i, s := range []string{"업종별", "동향", "구분", "금융", "보험업", "제조업", "부동산업", "정보통신업", "도소매업", "순투자", "증감률", "합계"} {
		both(s, 60+40*float64(i%3), 440+60*float64(i/3))
	}
	const base = 527.2
	y0, y1 := type3Box(base, 8.6)
	ref.Words = append(ref.Words,
		word{Text: "(", Page: 0, X0: 161.6, Y0: y0, X1: 165.9, Y1: y1},
		word{Text: "상위", Page: 0, X0: 166.0, Y0: base - 10.33, X1: 191.9, Y1: base + 2.61})
	ref.Base = append(ref.Base, wordBase{Y: base, OK: true, Type3: true}, wordBase{Y: base, OK: true})
	cb := base + candDrop
	cand.Words = append(cand.Words, word{Text: "(상위", Page: 0, X0: 161.6, Y0: cb - 8.62, X1: 191.9, Y1: cb + 4.59})
	cand.Base = append(cand.Base, wordBase{Y: cb, OK: true})
	if !withBase {
		ref.Base, cand.Base = nil, nil
	}
	return ref, cand
}

func TestComparePDFSplitWordAtBaseline(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale}
	ref, cand := splitPair(0, true)
	r := comparePDF(ref, cand, o)
	if !r.pass(o) || r.MaxDev > 0.01 {
		t.Errorf("same baseline: %v, want deviation 0", r)
	}
	// "(" and "(상위" start together and "상위" and "(상위" end together:
	// two edges, both at the baseline. The twelve other words are
	// compared at their box middles.
	if r.BaseEdges != 2 || r.Edges != 26 {
		t.Errorf("%d of %d edges at baseline, want 2 of 26", r.BaseEdges, r.Edges)
	}

	ref, cand = splitPair(2, true)
	r = comparePDF(ref, cand, o)
	if r.pass(o) || math.Abs(r.MaxDev-2) > 0.3 {
		t.Errorf("merged word 2 pt too low: %v, want max dev about 2", r)
	}

	// A document without Type 3 fonts has no baselines: the right edge of
	// "상위" is compared at the box middles.
	ref, cand = splitPair(0, false)
	r = comparePDF(ref, cand, o)
	if r.BaseEdges != 0 || math.Abs(r.MaxDev-1.85) > 0.3 {
		t.Errorf("no baselines: %v with %d baseline edges, want max dev about 1.85 and 0", r, r.BaseEdges)
	}
}

func TestReadGlyphsWithoutMutool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := readGlyphs("a.pdf", nil); err != errNoMutool {
		t.Errorf("err = %v, want errNoMutool", err)
	}
}

// fakeTool writes a shell script named name that prints out.
func fakeTool(t *testing.T, dir, name, out string) {
	t.Helper()
	// printf is built into the shell; PATH holds nothing else.
	script := "#!/bin/sh\nprintf '%s' '" + out + "'\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLoadBaselines(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses shell scripts in place of pdffonts and mutool")
	}
	const fonts = `name                                 type              encoding         emb sub uni object ID
------------------------------------ ----------------- ---------------- --- --- --- ---------
T3                                   Type 3            Custom           yes no  yes     15  0
`
	const noFonts = `name                                 type              encoding         emb sub uni object ID
------------------------------------ ----------------- ---------------- --- --- --- ---------
Body                                 TrueType          WinAnsi          yes yes yes     15  0
`
	stext := func(font string) string {
		return "<page id=\"page1\" width=\"595\" height=\"841\">\n<font name=\"" + font + "\" size=\"11\">\n" +
			"<char quad=\"0 0 0 0 0 0 0 0\" x=\"60\" y=\"100\" bidi=\"0\" c=\"a\"/>\n</font>\n</page>\n"
	}
	doc := func() *pdfWords {
		return &pdfWords{Pages: []pageSize{{595, 842}}, Words: []word{{"ab", 0, 60, 90, 80, 103}}}
	}

	// No Type 3 font: mutool is not run (it is not even there) and the
	// documents are left alone.
	dir := t.TempDir()
	fakeTool(t, dir, "pdffonts", noFonts)
	t.Setenv("PATH", dir)
	ref, cand := doc(), doc()
	if note := loadBaselines("r.pdf", "c.pdf", ref, cand); note != "" || ref.Base != nil || cand.Base != nil {
		t.Errorf("no Type 3 font: note %q, bases %v %v, want none", note, ref.Base, cand.Base)
	}

	// Type 3 font and no mutool: a note, and no baselines.
	fakeTool(t, dir, "pdffonts", fonts)
	ref, cand = doc(), doc()
	if note := loadBaselines("r.pdf", "c.pdf", ref, cand); note != errNoMutool.Error() || ref.Base != nil {
		t.Errorf("no mutool: note %q, base %v, want %q and none", note, ref.Base, errNoMutool)
	}

	// Type 3 font and mutool: both documents get baselines.
	fakeTool(t, dir, "mutool", stext("T3"))
	ref, cand = doc(), doc()
	if note := loadBaselines("r.pdf", "c.pdf", ref, cand); note != "" {
		t.Fatalf("note %q, want none", note)
	}
	want := wordBase{Y: 100, OK: true, Type3: true}
	if len(ref.Base) != 1 || ref.Base[0] != want || len(cand.Base) != 1 || cand.Base[0] != want {
		t.Errorf("bases %+v %+v, want %+v on both sides", ref.Base, cand.Base, want)
	}

	// mutool names the font differently: its words cannot be found.
	fakeTool(t, dir, "mutool", stext("Unnamed-T3"))
	ref, cand = doc(), doc()
	if note := loadBaselines("r.pdf", "c.pdf", ref, cand); !strings.Contains(note, "T3") || ref.Base != nil {
		t.Errorf("font not in mutool output: note %q, base %v", note, ref.Base)
	}
}
