package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const settingsHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes" ?><ha:HWPApplicationSetting xmlns:ha="http://www.hancom.co.kr/hwpml/2011/app" xmlns:config="urn:oasis:names:tc:opendocument:xmlns:config:1.0"><config:config-item-set name="PrintInfo"><config:config-item name="PrintAutoFootNote" type="boolean">false</config:config-item>`
const settingsTail = `<config:config-item name="OverlapSize" type="short">4</config:config-item><config:config-item name="ZoomX" type="short">100</config:config-item></config:config-item-set></ha:HWPApplicationSetting>`

// settingsXML is a settings.xml whose PrintMethod is method. Another item
// has the value 4 too.
func settingsXML(method string) string {
	return settingsHead + `<config:config-item name="PrintMethod" type="short">` + method + `</config:config-item>` + settingsTail
}

// writeHWPX makes an HWPX file the way Hancom Office orders it: mimetype
// first and stored, settings.xml among the compressed entries. settings is
// the content of settings.xml; "" leaves the entry out.
func writeHWPX(t *testing.T, path, settings string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	add := func(name string, method uint16, data string) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, data); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", zip.Store, "application/hwp+zip")
	add("Contents/header.xml", zip.Deflate, strings.Repeat("<hh:head/>", 200))
	add("BinData/image1.png", zip.Store, "\x89PNG picture bytes")
	add("Contents/section0.xml", zip.Deflate, strings.Repeat("<hp:p>4</hp:p>", 300))
	if settings != "" {
		add(settingsEntry, zip.Deflate, settings)
	}
	add("META-INF/manifest.xml", zip.Deflate, "<odf:manifest/>")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintMethod(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		settings, want string
		ok             bool
	}{
		{settingsXML("4"), "4", true},
		{settingsXML("0"), "0", true},
		{settingsXML("12"), "12", true},
		{settingsHead + `<config:config-item type="short" name="PrintMethod"> 4 </config:config-item>` + settingsTail, "4", true},
		{settingsHead + settingsTail, "", true},
		{"", "", true},
		{settingsXML("4") + settingsXML("0"), "", false},
	}
	for i, c := range cases {
		p := filepath.Join(dir, "a.hwpx")
		writeHWPX(t, p, c.settings)
		got, err := printMethod(p)
		if got != c.want || (err == nil) != c.ok {
			t.Errorf("case %d: PrintMethod %q, error %v, want %q and ok %v", i, got, err, c.want, c.ok)
		}
	}
	if _, err := printMethod(filepath.Join(dir, "missing.hwpx")); err == nil {
		t.Errorf("a missing file gives no error")
	}
}

func TestWriteOneUp(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src.hwpx"), filepath.Join(dir, "dst.hwpx")
	writeHWPX(t, src, settingsXML("4"))
	if err := writeOneUp(src, dst); err != nil {
		t.Fatal(err)
	}
	a, err := zip.OpenReader(src)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if len(a.File) != len(b.File) {
		t.Fatalf("%d entries, want %d", len(b.File), len(a.File))
	}
	if f := b.File[0]; f.Name != "mimetype" || f.Method != zip.Store {
		t.Errorf("first entry %q with method %d, want mimetype stored", f.Name, f.Method)
	}
	raw := func(f *zip.File) []byte {
		r, err := f.OpenRaw()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for i, fa := range a.File {
		fb := b.File[i]
		if fa.Name != fb.Name || fa.Method != fb.Method {
			t.Errorf("entry %d is %q with method %d, want %q with method %d", i, fb.Name, fb.Method, fa.Name, fa.Method)
			continue
		}
		if fa.Name != settingsEntry {
			if !bytes.Equal(raw(fa), raw(fb)) || fa.CRC32 != fb.CRC32 || fa.Flags != fb.Flags {
				t.Errorf("%s is not copied as it is stored", fa.Name)
			}
			continue
		}
		got, err := readEntry(fb)
		if err != nil {
			t.Fatalf("%s cannot be read back: %v", fb.Name, err)
		}
		if string(got) != settingsXML("0") {
			t.Errorf("settings.xml is\n%s\nwant\n%s", got, settingsXML("0"))
		}
		if fb.Flags&0x8 != 0 || fb.UncompressedSize64 != uint64(len(got)) {
			t.Errorf("settings.xml: flags %#x, size %d, want its sizes in its own header and %d", fb.Flags, fb.UncompressedSize64, len(got))
		}
	}
	if m, err := printMethod(dst); m != printOneUp || err != nil {
		t.Errorf("PrintMethod of the copy is %q (%v), want %q", m, err, printOneUp)
	}

	// Only a document that prints two pages to a sheet is copied.
	for _, settings := range []string{settingsXML("0"), settingsXML("5"), settingsHead + settingsTail, ""} {
		writeHWPX(t, src, settings)
		if err := writeOneUp(src, dst); err == nil {
			t.Errorf("settings %q: copied without an error", settings)
		}
	}
}

func TestOneUpRef(t *testing.T) {
	dir := t.TempDir()
	const name = "a.hwp"
	got := func() string {
		t.Helper()
		p, err := oneUpRef(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := got(); p != "" {
		t.Errorf("no file: %q", p)
	}
	path := filepath.Join(dir, name+oneUpSuffix)
	if err := os.WriteFile(path, []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := got(); p != "" {
		t.Errorf("a file that the manifest does not record is used: %q", p)
	}
	m, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for out, sum := range map[string]string{name + ".pdf": "s2", name + oneUpSuffix: "s1"} {
		if err := m.record(out, sum); err != nil {
			t.Fatal(err)
		}
	}
	if p := got(); p != "" {
		t.Errorf("a one-up reference made from another input than the reference PDF is used: %q", p)
	}
	if err := m.record(name+".pdf", "s1"); err != nil {
		t.Fatal(err)
	}
	if p := got(); p != path {
		t.Errorf("one-up reference %q, want %q", p, path)
	}
	// The name is not that of a document, so cmp does not list it.
	if err := os.WriteFile(filepath.Join(dir, name+".pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if names, err := refNames(dir); err != nil || !reflect.DeepEqual(names, []string{name}) {
		t.Errorf("documents %v (%v), want only %s", names, err, name)
	}
}

// onePerSheet prints pages one to a portrait sheet (595 x 842), page k
// scaled by scale(k) about its top left corner, as Hancom Office shrinks a
// page with memos.
func onePerSheet(pages [][]word, scale func(k int) float64) *pdfWords {
	d := &pdfWords{}
	for k, ws := range pages {
		d.Pages = append(d.Pages, pageSize{595, 842})
		s := scale(k)
		for _, w := range ws {
			d.Words = append(d.Words, word{w.Text, k, s * w.X0, s * w.Y0, s * w.X1, s * w.Y1})
		}
	}
	return d
}

func TestCompareOneUpHalves(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	pages := docPages(4)
	sheets := twoUp(pages, every(0.55))
	// Hancom Office shrank page 3 of the one-up reference for memos.
	memo := func(k int) float64 {
		if k == 2 {
			return 0.775
		}
		return 1
	}
	ref := onePerSheet(pages, memo)
	cand := twoUp(pages, every(0.71))
	r := compareOneUp(sheets, ref, cand, o)
	if r.OneUp == nil || !r.OneUp.Halved || r.TwoUp || r.RefPages != 4 || r.CandPages != 4 || len(r.Page) != 4 {
		t.Fatalf("one-up %+v, two-up %v, pages %d/%d, want the four halves against the four pages", r.OneUp, r.TwoUp, r.RefPages, r.CandPages)
	}
	if r.OneUp.RefSheets != 2 || r.OneUp.CandSheets != 2 || r.OneUp.SizeDiff != 0 {
		t.Errorf("sheets %d/%d, size difference %.2f, want 2/2 and 0", r.OneUp.RefSheets, r.OneUp.CandSheets, r.OneUp.SizeDiff)
	}
	if !r.pass(o) || r.MaxDev > 1e-6 || r.Matched != 48 || r.RefOnly != 0 || r.CandOnly != 0 || r.PageMoves != 0 {
		t.Errorf("same pages: %v", r)
	}
	for k, p := range r.Page {
		want := affine{S: 0.71 / memo(k), TX: 420.5 * float64(k%2)}
		if p.FitEdges != 24 || !near(p.Fit.S, want.S) || !near(p.Fit.TX, want.TX) || !near(p.Fit.TY, 0) {
			t.Errorf("page %d: fit %+v from %d edges, want %+v from 24", k+1, p.Fit, p.FitEdges, want)
		}
	}
	// Sheet by sheet against the two-up reference the document is the
	// same too; the one-up reference is not the looser test here.
	if two := compareSheets(sheets, cand, o); !two.pass(o) {
		t.Errorf("against the two-up reference: %v", two)
	}

	// A word 2.5 pt off on the sheet is 2.5 pt off, on its own page.
	moved := twoUp(pages, every(0.71))
	i := wordIndex(t, moved, "p3-w5")
	moved.Words[i].X0 += 2.5
	moved.Words[i].X1 += 2.5
	r = compareOneUp(sheets, ref, moved, o)
	if r.pass(o) || math.Abs(r.MaxDev-2.5) > 0.1 || r.Worst != (located{"p3-w5", 4}) || math.Abs(r.Page[3].MaxDev-2.5) > 0.1 || r.Page[2].MaxDev > 0.1 {
		t.Errorf("word 2.5 pt off: pass %v, max %.2f at %+v, page 3 %.2f, page 4 %.2f", r.pass(o), r.MaxDev, r.Worst, r.Page[2].MaxDev, r.Page[3].MaxDev)
	}

	// A word in the other half of its sheet is on another page.
	other := twoUp(pages, every(0.71))
	i = wordIndex(t, other, "p2-w0")
	other.Words[i].X0 += 420.5
	other.Words[i].X1 += 420.5
	if r = compareOneUp(sheets, ref, other, o); r.pass(o) || r.PageMoves != 1 {
		t.Errorf("word in the other half: pass %v, %d on another page, want 1", r.pass(o), r.PageMoves)
	}

	// A word the candidate lacks, and one only it has.
	less := twoUp(pages, every(0.71))
	i = wordIndex(t, less, "p1-w7")
	less.Words[i].Text = "딴말"
	if r = compareOneUp(sheets, ref, less, o); r.pass(o) || r.RefOnly != 1 || r.CandOnly != 1 || r.Page[1].RefOnly != 1 {
		t.Errorf("other word: pass %v, unmatched %d/%d, page 2 %d, want 1/1 on page 2", r.pass(o), r.RefOnly, r.CandOnly, r.Page[1].RefOnly)
	}
}

func TestCompareOneUpPageByPage(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	pages := docPages(4)
	sheets := twoUp(pages, every(0.55))
	ref := onePerSheet(pages, every(1))
	// A candidate that does not follow the print setting: four portrait
	// sheets. Its words are those of the reference.
	cand := onePerSheet(pages, every(1))
	r := compareOneUp(sheets, ref, cand, o)
	if r.OneUp.Halved || r.RefPages != 4 || r.CandPages != 4 || r.MaxDev > 1e-6 || r.RefOnly != 0 || r.CandOnly != 0 {
		t.Errorf("page by page: %+v, %v", r.OneUp, r)
	}
	if u := r.OneUp; u.RefSheets != 2 || u.CandSheets != 4 || u.SizeDiff != 247 || u.SizeSheet != 1 ||
		u.RefSize != (pageSize{841, 595}) || u.CandSize != (pageSize{595, 842}) {
		t.Errorf("sheets %+v, want 2/4 and sheet 1 247 pt off", u)
	}
	if r.pass(o) {
		t.Errorf("four sheets where Hancom Office prints two pass")
	}
	r.OneUp = nil
	if !r.pass(o) {
		t.Errorf("the words are the same and fail without the sheets: %v", r)
	}
}

func TestCompareOneUpSheets(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	pages := docPages(4)
	sheets := twoUp(pages, every(0.55))
	ref := onePerSheet(pages, every(1))
	cases := []struct {
		size pageSize
		diff float64
		pass bool
	}{
		{pageSize{841, 595}, 0, true},
		{pageSize{841.9, 595.3}, 0.9, true},
		{pageSize{841, 596.5}, 1.5, false},
		{pageSize{839, 595}, 2, false},
	}
	for _, c := range cases {
		cand := twoUp(pages, every(0.71))
		cand.Pages[1] = c.size
		r := compareOneUp(sheets, ref, cand, o)
		if math.Abs(r.OneUp.SizeDiff-c.diff) > 1e-9 || r.pass(o) != c.pass || (c.diff > 0 && r.OneUp.SizeSheet != 2) {
			t.Errorf("sheet 2 of %v: %.2f pt off on sheet %d, pass %v, want %.2f and %v", c.size, r.OneUp.SizeDiff, r.OneUp.SizeSheet, r.pass(o), c.diff, c.pass)
		}
	}
	// One sheet too many.
	cand := twoUp(docPages(6), every(0.71))
	if r := compareOneUp(sheets, onePerSheet(docPages(6), every(1)), cand, o); r.pass(o) || r.RefPages != r.CandPages || r.OneUp.CandSheets != 3 {
		t.Errorf("three sheets against two: pass %v, pages %d/%d, sheets %d", r.pass(o), r.RefPages, r.CandPages, r.OneUp.CandSheets)
	}
}

func TestCompareOneUpOddPages(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	pages := docPages(3)
	sheets := twoUp(pages, every(0.55))
	ref := onePerSheet(pages, every(1))
	cand := twoUp(pages, every(0.71))
	r := compareOneUp(sheets, ref, cand, o)
	if !r.OneUp.Trimmed || r.RefPages != 3 || r.CandPages != 3 || !r.pass(o) {
		t.Errorf("three pages on two sheets: %+v, pages %d/%d, pass %v", r.OneUp, r.RefPages, r.CandPages, r.pass(o))
	}
	// A word on the last half makes it a page.
	cand.Words = append(cand.Words, word{"덤", 1, 500, 100, 530, 110})
	r = compareOneUp(sheets, ref, cand, o)
	if r.OneUp.Trimmed || r.CandPages != 4 || r.pass(o) {
		t.Errorf("a fourth half with a word: %+v, pages %d/%d, pass %v", r.OneUp, r.RefPages, r.CandPages, r.pass(o))
	}
	// An empty half is left out only when it is the one page too many.
	four := onePerSheet(docPages(4), every(1))
	if r = compareOneUp(sheets, four, twoUp(pages, every(0.71)), o); r.OneUp.Trimmed || r.RefOnly != 12 || r.pass(o) {
		t.Errorf("reference with a fourth page: %+v, %v", r.OneUp, r)
	}
}

// fakeRenderPage puts a pdftoppm on PATH that gives <pdf>.<page>.ppm for
// the page renderPage asks for, and returns the folder for the PDFs.
func fakeRenderPage(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script in place of pdftoppm")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nexec cat \"$7.$4.ppm\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pdftoppm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestCheckTrimmedHalf(t *testing.T) {
	dir := fakeRenderPage(t)
	o := cmpOptions{Tol: 1, InkLimit: 0.01}
	path := filepath.Join(dir, "cand.pdf")
	result := func() pdfResult {
		return pdfResult{RefPages: 3, CandPages: 3, OneUp: &oneUp{Halved: true, Trimmed: true, RefSheets: 2, CandSheets: 2}}
	}
	// Sheet 2 with ink on the left half only.
	g := blank(200, 120)
	g.fill(20, 20, 80, 100, black)
	if err := os.WriteFile(path+".2.ppm", ppm(g), 0o644); err != nil {
		t.Fatal(err)
	}
	r := result()
	checkTrimmedHalf(path, &r)
	if r.OneUp.HalfErr != "" || !r.pass(o) {
		t.Errorf("empty right half: %q, pass %v", r.OneUp.HalfErr, r.pass(o))
	}
	// A picture on the right half: it is a page the reference lacks.
	g.fill(120, 20, 180, 100, blue)
	if err := os.WriteFile(path+".2.ppm", ppm(g), 0o644); err != nil {
		t.Fatal(err)
	}
	r = result()
	checkTrimmedHalf(path, &r)
	if r.OneUp.HalfErr == "" || r.pass(o) {
		t.Errorf("right half with a picture: %q, pass %v", r.OneUp.HalfErr, r.pass(o))
	}
	if rep := renderReport([]row{{Name: "odd.hwp", PDF: &r}}, o, "cand", false); !strings.Contains(rep, "| odd.hwp | 3/3, one-up ref; sheets 2/2, last half not empty |") ||
		!strings.Contains(rep, "- PDF: the right half of the candidate's last sheet is left out as empty, but something is drawn on it") {
		t.Errorf("report does not tell of the half\n%s", rep)
	}
	// A sheet that cannot be drawn is not known to be empty.
	r = result()
	checkTrimmedHalf(filepath.Join(dir, "missing.pdf"), &r)
	if r.OneUp.HalfErr == "" || r.pass(o) {
		t.Errorf("sheet that cannot be drawn: %q, pass %v", r.OneUp.HalfErr, r.pass(o))
	}
	// Nothing is drawn when no half was left out.
	r = result()
	r.OneUp.Trimmed = false
	checkTrimmedHalf(filepath.Join(dir, "missing.pdf"), &r)
	if r.OneUp.HalfErr != "" {
		t.Errorf("no half left out: %q", r.OneUp.HalfErr)
	}
}

func TestSheetOf(t *testing.T) {
	cases := []struct {
		r    pdfResult
		page int
		want [4]int
	}{
		{pdfResult{}, 5, [4]int{5, 0, 5, 0}},
		{pdfResult{TwoUp: true}, 4, [4]int{2, 1, 2, 1}},
		{pdfResult{TwoUp: true}, 5, [4]int{2, 2, 2, 2}},
		{pdfResult{OneUp: &oneUp{Halved: true}}, 4, [4]int{4, 0, 2, 1}},
		{pdfResult{OneUp: &oneUp{Halved: true}}, 5, [4]int{5, 0, 2, 2}},
		{pdfResult{OneUp: &oneUp{}}, 5, [4]int{5, 0, 5, 0}},
	}
	for _, c := range cases {
		var got [4]int
		got[0], got[1], got[2], got[3] = c.r.sheetOf(c.page)
		if got != c.want {
			t.Errorf("two-up %v, one-up %+v, page %d: %v, want %v", c.r.TwoUp, c.r.OneUp, c.page, got, c.want)
		}
	}
}

func TestMeasureInkOneUp(t *testing.T) {
	dir := fakeRenderPage(t)
	refPath, candPath := filepath.Join(dir, "ref.pdf"), filepath.Join(dir, "cand.pdf")
	// Page 2 of the one-up reference is the right half of candidate
	// sheet 1, at 0.7 of its size. The left half holds another page.
	fit := affine{S: 0.7, TX: 100}
	ref, cand := blank(140, 140), blank(200, 100)
	ref.fill(20, 20, 80, 100, black)
	cand.fill(114, 14, 156, 70, black)
	cand.fill(10, 10, 60, 60, blue)
	for path, g := range map[string]*pageImage{refPath + ".2.ppm": ref, candPath + ".1.ppm": cand} {
		if err := os.WriteFile(path, ppm(g), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o := cmpOptions{Tol: 1, InkLimit: 0.01, InkAll: true}
	result := func() pdfResult {
		return pdfResult{RefPages: 2, CandPages: 2, OneUp: &oneUp{Halved: true, RefSheets: 1, CandSheets: 1},
			Page: []pageStat{{Fit: affine{S: 0.7}, FitEdges: 8, RefWords: 4, CandWords: 4}, {Fit: fit, FitEdges: 8, RefWords: 4, CandWords: 4}}}
	}
	r := result()
	measureInk(refPath, candPath, &r, o)
	if p := r.Page[1]; !p.InkDone || p.Ink != 0 {
		t.Errorf("page 2 against the right half of sheet 1: done %v, difference %.3f, error %q, want 0", p.InkDone, p.Ink, p.InkErr)
	}
	if p := r.Page[0]; p.InkDone || p.InkErr == "" {
		t.Errorf("page 1 of the reference cannot be drawn and was measured: %+v", p)
	}
	// The same picture 3 pt lower in the candidate.
	low := blank(200, 100)
	low.fill(114, 17, 156, 73, black)
	if err := os.WriteFile(candPath+".1.ppm", ppm(low), 0o644); err != nil {
		t.Fatal(err)
	}
	r = result()
	measureInk(refPath, candPath, &r, o)
	if p := r.Page[1]; !p.InkDone || p.Ink < 0.2 {
		t.Errorf("picture 3 pt lower: done %v, difference %.3f, want a large one", p.InkDone, p.Ink)
	}
	// A page without words has no fit, and a half cannot be laid on a
	// page of another size without one.
	r = result()
	r.Page[1] = pageStat{Fit: affine{S: 1}, Picture: true}
	measureInk(refPath, candPath, &r, o)
	if p := r.Page[1]; p.InkDone || p.InkErr == "" || r.pass(o) {
		t.Errorf("picture page of a halved candidate: done %v, error %q, pass %v", p.InkDone, p.InkErr, r.pass(o))
	}
}

func TestMeasureDrawnOneUp(t *testing.T) {
	dir := fakePdftoppm(t)
	refPath, candPath := filepath.Join(dir, "ref.pdf"), filepath.Join(dir, "cand.pdf")
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	ref, cand := sheet(200, 300), sheet(200, 300)
	at := cand.label("2022", 140, 30, labelHeight)
	refLabel(ref, "2022", 140.5, 30)
	none := cand.label("2024", 160, 60, labelHeight)
	// Page 4 of the one-up reference is the right half of sheet 2.
	writeSheet(t, refPath, 4, ref)
	writeSheet(t, candPath, 2, cand)
	at.Page, none.Page = 3, 3
	refSizes := &pdfWords{Pages: []pageSize{{200, 300}, {200, 300}, {200, 300}, {200, 300}}}
	candSizes := &pdfWords{Pages: []pageSize{{200, 300}, {200, 300}}}
	r := pdfResult{RefPages: 4, CandPages: 4, CandOnly: 2, OneUp: &oneUp{Halved: true, RefSheets: 2, CandSheets: 2},
		Page:          []pageStat{{Fit: labelFit}, {Fit: labelFit}, {Fit: labelFit}, {Fit: labelFit, CandOnly: 2}},
		CandOnlyWords: []loneWord{{word: at}, {word: none}}}
	measureDrawn(refPath, candPath, refSizes, candSizes, &r, o)
	if r.DrawnWords != 1 || r.CandOnly != 1 || r.Page[3].Drawn != 1 || r.Page[3].CandOnly != 1 || r.Page[3].DrawnErr != "" {
		t.Errorf("page 4: drawn %d, cand-only %d, %+v, want 1 and 1", r.DrawnWords, r.CandOnly, r.Page[3])
	}
	if len(r.CandOnlyWords) != 1 || r.CandOnlyWords[0].Text != "2024" {
		t.Errorf("words left without a partner: %+v, want 2024", r.CandOnlyWords)
	}
}

func TestReportOneUp(t *testing.T) {
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	pages := docPages(4)
	sheets := twoUp(pages, every(0.55))
	ref := onePerSheet(pages, every(1))
	good := compareOneUp(sheets, ref, twoUp(pages, every(0.71)), o)
	old := compareOneUp(sheets, ref, onePerSheet(pages, every(1)), o)
	plainDoc := testDoc(595, [][]string{{"가", "나", "다"}})
	plain := comparePDF(plainDoc, plainDoc, o)
	rows := []row{
		{Name: "twoup.hwp", PDF: &good, Pass: good.pass(o)},
		{Name: "old.hwp", PDF: &old, Pass: old.pass(o)},
		{Name: "plain.hwp", PDF: &plain, Pass: plain.pass(o)},
	}
	rep := renderReport(rows, o, "cand", false)
	for _, want := range []string{
		"2 of 3 documents pass.",
		"A document marked one-up ref prints two pages to a sheet.",
		"number of sheets and their size (to within 1.00 pt).",
		"| twoup.hwp | 4/4, one-up ref; sheets 2/2 | 0.00 | 0/0 | 0 | 0 | halves: left 0.7100, right 0.7100 | 0 |  | - | - | - | - | pass |",
		"| old.hwp | 4/4, one-up ref; sheets 2/4, sheet 1 is 247.00 pt off in size | 0.00 | 0/0 | 0 | 0 | 1.0000 | 0 |  | - | - | - | - | FAIL |",
		"| plain.hwp | 1/1 | 0.00 | 0/0 | 0 | 0 | 1.0000 | 0 |  | - | - | - | - | pass |",
		"- PDF: 4/4 pages, 48/48 words, 48 matched (0 out of reading order), 0 on another page, 96 edges measured",
		"- PDF: compared with the one-up reference (Hancom Office's print of the document one page to a sheet), because the document prints two pages to a sheet and Hancom Office's two-up print can leave out parts of a page",
		"- PDF: the candidate does not print two pages to a sheet; its pages are compared with the pages of the one-up reference",
		"- PDF: sheets of the two-up reference and of the candidate: 2/4; sheet 1 is 841.00 x 595.00 pt in the reference and 595.00 x 842.00 pt in the candidate, more than 1.00 pt apart; the candidate does not print the sheets Hancom Office prints",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q\n%s", want, rep)
		}
	}
	if strings.Contains(rep, "### twoup.hwp") {
		t.Errorf("a passing document is listed under Differences\n%s", rep)
	}
	// A failed document whose candidate is halved tells so.
	bad := twoUp(pages, every(0.71))
	bad.Words[wordIndex(t, bad, "p3-w5")].X1 += 2.5
	res := compareOneUp(sheets, ref, bad, o)
	rep = renderReport([]row{{Name: "bad.hwp", PDF: &res}}, o, "cand", false)
	for _, want := range []string{
		"- PDF: the candidate prints two pages to a sheet; each half is compared with a page of the one-up reference: scale of the left halves 0.7100, of the right halves 0.7100",
		"- PDF: sheets of the two-up reference and of the candidate: 2/2\n",
		`- PDF: largest deviation 2.50 pt at "p3-w5" (ref page 4)`,
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q\n%s", want, rep)
		}
	}
	// A report without such a document says nothing about one-up.
	if rep = renderReport(rows[2:], o, "cand", false); strings.Contains(rep, "one-up") {
		t.Errorf("report of a plain document tells of one-up\n%s", rep)
	}
}

// fakeRemote puts an ssh and an scp on PATH. ssh only notes its command in
// <dir>/ssh.log. scp keeps every file that is sent in <dir>/sent/ under the
// name it gets on the remote, and gives the bytes "%PDF one-up" for every
// file that is fetched.
func fakeRemote(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses shell scripts in place of ssh and scp")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sent"), 0o755); err != nil {
		t.Fatal(err)
	}
	ssh := "#!/bin/sh\nfor a; do last=$a; done\nprintf '%s\\n' \"$last\" >> '" + dir + "/ssh.log'\n"
	scp := "#!/bin/sh\nfor a; do from=$to; to=$a; done\ncase \"$from\" in\n" +
		"*:*) printf '%%PDF one-up' > \"$to\" ;;\n" +
		"*) cp \"$from\" '" + dir + "/sent/'\"${to##*/}\" ;;\nesac\n"
	for name, script := range map[string]string{"ssh": ssh, "scp": scp} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(envHost, "user@pc")
	t.Setenv(envDir, "/mnt/c/ref")
	return dir
}

func TestRefMakesOneUpReference(t *testing.T) {
	remote := fakeRemote(t)
	corpus, out := t.TempDir(), t.TempDir()
	refDir := filepath.Join(out, "ref")
	if err := os.Mkdir(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(out, "why-hwp.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Three hwpx inputs: two pages to a sheet, one page to a sheet, and
	// no print setting. Two hwp inputs, which Hancom Office saved as
	// hwpx with two pages to a sheet; the hwpx of the second is from an
	// older input.
	writeHWPX(t, filepath.Join(corpus, "two.hwpx"), settingsXML("4"))
	writeHWPX(t, filepath.Join(corpus, "one.hwpx"), settingsXML("0"))
	writeHWPX(t, filepath.Join(corpus, "none.hwpx"), "")
	for _, n := range []string{"five.hwp", "stale.hwp"} {
		if err := os.WriteFile(filepath.Join(corpus, n), []byte("hwp "+n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	man, err := loadManifest(refDir)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := listCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	sums := map[string]string{}
	for _, d := range docs {
		sum, err := sha256File(d.Path)
		if err != nil {
			t.Fatal(err)
		}
		sums[d.Name] = sum
		for _, to := range d.Targets() {
			p := filepath.Join(refDir, d.Name+"."+to)
			if to == "hwpx" {
				writeHWPX(t, p, settingsHead+`<config:config-item type="short" name="PrintMethod">4</config:config-item>`+settingsTail)
			} else if err := os.WriteFile(p, []byte("made by Hancom Office"), 0o644); err != nil {
				t.Fatal(err)
			}
			if d.Name == "stale.hwp" && to == "hwpx" {
				sum = "older input"
			}
			if err := man.record(d.Name+"."+to, sum); err != nil {
				t.Fatal(err)
			}
		}
	}
	// stale.hwp.hwpx is made again; the fake remote gives a file that is
	// not an HWPX file, so its print setting cannot be read.
	run := func() (int, string) {
		var stdout, stderr bytes.Buffer
		code := runRef([]string{"-corpus", corpus, "-out", out, "-exe", exe}, &stdout, &stderr)
		return code, stdout.String() + stderr.String()
	}
	code, log := run()
	if code != 1 {
		t.Errorf("exit code %d, want 1 for the hwpx that cannot be read\n%s", code, log)
	}
	for _, want := range []string{
		"two.hwpx.oneup.pdf: made, 11 bytes",
		"five.hwp.oneup.pdf: made, 11 bytes",
		"stale.hwp.hwpx: made, 11 bytes",
		"stale.hwp.oneup.pdf: FAILED: ",
		"3 made, 1 failed, 14 up to date",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q\n%s", want, log)
		}
	}
	for _, n := range []string{"one.hwpx", "none.hwpx"} {
		if strings.Contains(log, n+oneUpSuffix) {
			t.Errorf("%s does not print two pages to a sheet and got a one-up reference\n%s", n, log)
		}
		if _, err := os.Stat(filepath.Join(refDir, n+oneUpSuffix)); err == nil {
			t.Errorf("%s%s was made", n, oneUpSuffix)
		}
	}
	man, err = loadManifest(refDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"two.hwpx", "five.hwp"} {
		if got, err := os.ReadFile(filepath.Join(refDir, n+oneUpSuffix)); err != nil || string(got) != "%PDF one-up" {
			t.Errorf("%s%s: %q, %v, want what the remote printed", n, oneUpSuffix, got, err)
		}
		if !man.fresh(n+oneUpSuffix, sums[n]) {
			t.Errorf("%s%s is not recorded for its input", n, oneUpSuffix)
		}
		if p, err := oneUpRef(refDir, n); err != nil || p == "" {
			t.Errorf("cmp does not use %s%s: %q, %v", n, oneUpSuffix, p, err)
		}
	}
	// What went to Hancom Office: the converter, the stale hwp, and two
	// hwpx files that print one page to a sheet.
	sent, err := os.ReadDir(filepath.Join(remote, "sent"))
	if err != nil {
		t.Fatal(err)
	}
	oneUps := 0
	for _, e := range sent {
		if !strings.HasSuffix(e.Name(), ".hwpx") {
			continue
		}
		if m, err := printMethod(filepath.Join(remote, "sent", e.Name())); m != printOneUp || err != nil {
			t.Errorf("sent %s with PrintMethod %q (%v), want %q", e.Name(), m, err, printOneUp)
		}
		oneUps++
	}
	if oneUps != 2 || len(sent) != 4 {
		t.Errorf("%d files sent, %d of them one-up copies, want 4 and 2", len(sent), oneUps)
	}
	// The inputs and Hancom Office's hwpx are as they were.
	for _, p := range []string{filepath.Join(corpus, "two.hwpx"), filepath.Join(refDir, "five.hwp.hwpx")} {
		if m, err := printMethod(p); m != printTwoUp || err != nil {
			t.Errorf("%s has PrintMethod %q (%v) after the run", p, m, err)
		}
	}

	// A second run prints nothing again but what failed.
	calls := func() int {
		b, err := os.ReadFile(filepath.Join(remote, "ssh.log"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "convert -engine hancom")
	}
	before := calls()
	if before != 3 {
		t.Errorf("%d conversions on the remote, want 3", before)
	}
	code, log = run()
	for _, want := range []string{"two.hwpx.oneup.pdf: up to date", "five.hwp.oneup.pdf: up to date", "stale.hwp.oneup.pdf: FAILED: ", "0 made, 1 failed, 17 up to date"} {
		if !strings.Contains(log, want) {
			t.Errorf("second run lacks %q\n%s", want, log)
		}
	}
	if code != 1 {
		t.Errorf("second run: exit code %d, want 1 for the one-up reference that failed", code)
	}
	if calls() != before {
		t.Errorf("second run made %d conversions on the remote", calls()-before)
	}
	// With -force it is printed again.
	var stdout, stderr bytes.Buffer
	runRef([]string{"-corpus", corpus, "-out", out, "-exe", exe, "-force"}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "two.hwpx.oneup.pdf: made") || calls() <= before {
		t.Errorf("-force did not print the one-up reference again\n%s", stdout.String())
	}
}

func TestRefWithoutTwoUpDocumentsNeedsNoRemote(t *testing.T) {
	t.Setenv(envHost, "")
	t.Setenv(envDir, "")
	corpus, out := t.TempDir(), t.TempDir()
	refDir := filepath.Join(out, "ref")
	if err := os.Mkdir(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(corpus, "one.hwpx")
	writeHWPX(t, src, settingsXML("0"))
	sum, err := sha256File(src)
	if err != nil {
		t.Fatal(err)
	}
	man, err := loadManifest(refDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"pdf", "txt", "hwp"} {
		if err := os.WriteFile(filepath.Join(refDir, "one.hwpx."+to), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := man.record("one.hwpx."+to, sum); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := runRef([]string{"-corpus", corpus, "-out", out}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "all 1 documents are up to date") {
		t.Errorf("exit code %d\n%s%s", code, stdout.String(), stderr.String())
	}
	// A document that prints two pages to a sheet needs the remote.
	writeHWPX(t, src, settingsXML("4"))
	if sum, err = sha256File(src); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"pdf", "txt", "hwp"} {
		if err := man.record("one.hwpx."+to, sum); err != nil {
			t.Fatal(err)
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := runRef([]string{"-corpus", corpus, "-out", out}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), envHost) {
		t.Errorf("exit code %d, want 2 and a message about %s\n%s%s", code, envHost, stdout.String(), stderr.String())
	}
}

// writePDFWords stores d as the PDF at path for the pdftotext of
// fakePdftotext.
func writePDFWords(t *testing.T, path string, d *pdfWords) {
	t.Helper()
	var b strings.Builder
	b.WriteString("<html><body><doc>\n")
	for i, p := range d.Pages {
		fmt.Fprintf(&b, "<page width=\"%f\" height=\"%f\">\n", p.W, p.H)
		for _, w := range d.Words {
			if w.Page == i {
				fmt.Fprintf(&b, "<word xMin=\"%f\" yMin=\"%f\" xMax=\"%f\" yMax=\"%f\">%s</word>\n", w.X0, w.Y0, w.X1, w.Y1, w.Text)
			}
		}
		b.WriteString("</page>\n")
	}
	b.WriteString("</doc></body></html>\n")
	for name, data := range map[string]string{path: "%PDF", path + ".bbox": b.String()} {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// fakePdftotext puts on PATH a pdftotext that gives what writePDFWords
// stored and a pdffonts that lists no font.
func fakePdftotext(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses shell scripts in place of pdftotext and pdffonts")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pdftotext"), []byte("#!/bin/sh\nexec cat \"$2.bbox\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeTool(t, dir, "pdffonts", "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCompareDocUsesOneUpReference(t *testing.T) {
	fakePdftotext(t)
	o := cmpOptions{Tol: 1, Fit: fitScale, InkLimit: 0.01}
	refDir, candDir := t.TempDir(), t.TempDir()
	const name = "a.hwp"
	pages := docPages(4)
	// Hancom Office's two-up print lacks the last four words of the
	// right pages; its one-up print has them.
	cut := twoUp(pages, every(0.55))
	var kept []word
	for _, w := range cut.Words {
		if !(w.X0 > 420.5 && w.Y0 > 0.55*380) {
			kept = append(kept, w)
		}
	}
	if len(cut.Words)-len(kept) != 8 {
		t.Fatalf("%d words cut, want 8", len(cut.Words)-len(kept))
	}
	cut.Words = kept
	writePDFWords(t, filepath.Join(refDir, name+".pdf"), cut)
	writePDFWords(t, filepath.Join(refDir, name+oneUpSuffix), onePerSheet(pages, every(1)))
	writePDFWords(t, filepath.Join(candDir, name+".pdf"), twoUp(pages, every(0.71)))
	man, err := loadManifest(refDir)
	if err != nil {
		t.Fatal(err)
	}
	record := func(out, sum string) {
		t.Helper()
		if err := man.record(out, sum); err != nil {
			t.Fatal(err)
		}
	}
	record(name+".pdf", "s1")
	record(name+oneUpSuffix, "s1")

	r := compareDoc(name, refDir, candDir, o, nil)
	if r.Err != "" || r.PDF == nil || r.PDF.OneUp == nil {
		t.Fatalf("not compared with the one-up reference: %+v", r)
	}
	if p := r.PDF; !r.Pass || !p.OneUp.Halved || p.RefPages != 4 || p.CandPages != 4 || p.Matched != 48 || p.CandOnly != 0 ||
		p.OneUp.RefSheets != 2 || p.OneUp.CandSheets != 2 {
		t.Errorf("against the one-up reference: pass %v, %+v, %v", r.Pass, p.OneUp, p)
	}

	// A candidate that prints one page to a sheet has the words and not
	// the sheets.
	writePDFWords(t, filepath.Join(candDir, name+".pdf"), onePerSheet(pages, every(1)))
	r = compareDoc(name, refDir, candDir, o, nil)
	if p := r.PDF; r.Pass || p == nil || p.OneUp == nil || p.OneUp.Halved || p.MaxDev > 1e-6 || p.RefOnly != 0 || p.CandOnly != 0 || p.OneUp.CandSheets != 4 {
		t.Errorf("candidate of four sheets: pass %v, %+v", r.Pass, r.PDF)
	}

	// Without a one-up reference for the same input the two-up reference
	// is what the candidate is compared with, and its words are missing.
	writePDFWords(t, filepath.Join(candDir, name+".pdf"), twoUp(pages, every(0.71)))
	record(name+oneUpSuffix, "s0")
	r = compareDoc(name, refDir, candDir, o, nil)
	if p := r.PDF; r.Pass || p == nil || p.OneUp != nil || !p.TwoUp || p.RefPages != 2 || p.CandOnly != 8 {
		t.Errorf("against the two-up reference: pass %v, %+v", r.Pass, r.PDF)
	}
}
