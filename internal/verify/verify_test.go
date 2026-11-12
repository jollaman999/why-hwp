package verify

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jollaman999/why-hwp/internal/load"
	"github.com/jollaman999/why-hwp/internal/model"
)

const (
	sampleHWPX = "../../testdata/sample.hwpx"
	sampleHWP  = "../../testdata/sample.hwp"
)

var (
	em = string(emDash)
	en = string(enDash)
)

// rewriteHWPX copies sample.hwpx into a temp dir with section0.xml passed
// through edit, keeping every other entry and its compression as is.
func rewriteHWPX(t *testing.T, edit func(string) string) string {
	t.Helper()
	zr, err := zip.OpenReader(sampleHWPX)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	name := filepath.Join(t.TempDir(), "edited.hwpx")
	out, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == "Contents/section0.xml" {
			edited := edit(string(data))
			if edited == string(data) {
				t.Fatal("edit changed nothing")
			}
			data = []byte(edited)
		}
		hdr := f.FileHeader
		w, err := zw.CreateHeader(&zip.FileHeader{Name: hdr.Name, Method: hdr.Method, Modified: hdr.Modified})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func mustLoad(t *testing.T, name string) *model.Document {
	t.Helper()
	doc, err := load.Any(name)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func mustReplace(t *testing.T, s, old, repl string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("%q not in section0.xml", old)
	}
	return strings.Replace(s, old, repl, 1)
}

func plain(t *testing.T, r *Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.WritePlain(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func para(text string) string {
	return `<hp:p id="0" paraPrIDRef="0" styleIDRef="0" pageBreak="0" columnBreak="0" merged="0"><hp:run charPrIDRef="0"><hp:t>` + text + `</hp:t></hp:run></hp:p>`
}

// sample.hwp and sample.hwpx are the same document saved in both formats.
func TestHWPAgainstHWPX(t *testing.T) {
	r := Check(mustLoad(t, sampleHWP), mustLoad(t, sampleHWPX), Options{Allow: &AllowList{}})
	if len(r.Changes) != 0 || r.Violations != 0 || r.Warnings != 0 {
		t.Fatalf("report = %+v\n%s", r, plain(t, r))
	}
	if !strings.HasPrefix(plain(t, r), "PASS: 0 violations, 0 warnings\n") {
		t.Errorf("plain:\n%s", plain(t, r))
	}
}

func TestChangesAgainstAllowList(t *testing.T) {
	edited := rewriteHWPX(t, func(s string) string {
		s = mustReplace(t, s, "핵심 기능 구현", "핵심 기능 구현 및 시험 2027")
		s = mustReplace(t, s, "<hp:t>(A/B)</hp:t>", "<hp:t>(B)</hp:t>")
		caption := strings.Index(s, "<hp:t>그림 1. 가짜 도형")
		start := strings.LastIndex(s[:caption], "<hp:p ")
		return s[:start] + para("새 문단 하나") + para("새 문단 둘") + s[start:]
	})
	allow, err := ParseAllow(strings.NewReader("# cells the writer owns\ns0/p4/t0/r2/c1\n\ns0/p7\n"))
	if err != nil {
		t.Fatal(err)
	}
	orig := mustLoad(t, sampleHWPX)
	r := Check(orig, mustLoad(t, edited), Options{Allow: allow})

	want := []Change{
		{Op: "changed", Loc: "s0/p4/t0/r2/c1", OrigLoc: "s0/p4/t0/r2/c1", Old: "핵심 기능 구현", New: "핵심 기능 구현 및 시험 2027", Severity: Info},
		{Op: "changed", Loc: "s0/p4/t0/r2/c2", OrigLoc: "s0/p4/t0/r2/c2", Old: "(A/B)", New: "(B)", Severity: Violation},
		{Op: "added", Loc: "s0/p8", After: "s0/p7", New: "새 문단 하나", Severity: Info},
		{Op: "added", Loc: "s0/p9", After: "s0/p7", New: "새 문단 둘", Severity: Info},
	}
	if len(r.Changes) != len(want) {
		t.Fatalf("changes = %+v\n%s", r.Changes, plain(t, r))
	}
	for i := range want {
		if r.Changes[i] != want[i] {
			t.Errorf("change %d = %+v, want %+v", i, r.Changes[i], want[i])
		}
	}
	if len(r.Preserved) != 0 {
		t.Errorf("preservation = %+v (the table and picture only moved)", r.Preserved)
	}
	if len(r.NewNumbers) != 1 || r.NewNumbers[0].Loc != "s0/p4/t0/r2/c1" || r.NewNumbers[0].Numbers[0] != "2027" {
		t.Errorf("new numbers = %+v", r.NewNumbers)
	}
	if r.Violations != 1 || r.Warnings != 1 {
		t.Errorf("violations %d, warnings %d; want 1, 1", r.Violations, r.Warnings)
	}
	out := plain(t, r)
	for _, s := range []string{
		"FAIL: 1 violation, 1 warning\n",
		"  changes       2 changed, 2 added, 0 removed, 1 outside -allow\n",
		"! changed  s0/p4/t0/r2/c2  [not in -allow]\n",
		"  added    2 after original s0/p7\n",
		"+ s0/p8\t새 문단 하나\n",
		"? s0/p4/t0/r2/c1  2027\n",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("plain lacks %q:\n%s", s, out)
		}
	}

	// Without -allow the same changes are only listed.
	if r := Check(orig, mustLoad(t, edited), Options{}); r.Violations != 0 || len(r.Changes) != 4 {
		t.Errorf("without allow list: %d violations, %d changes", r.Violations, len(r.Changes))
	}
}

func TestMemoRemoved(t *testing.T) {
	memo := regexp.MustCompile(`(?s)<hp:ctrl><hp:fieldBegin [^>]*type="MEMO".*?</hp:fieldBegin></hp:ctrl>`)
	end := regexp.MustCompile(`<hp:ctrl><hp:fieldEnd [^>]*/></hp:ctrl>`)
	edited := rewriteHWPX(t, func(s string) string {
		return end.ReplaceAllString(memo.ReplaceAllString(s, ""), "")
	})
	cur := mustLoad(t, edited)
	if len(cur.Summary.Memos) != 0 {
		t.Fatalf("memo still there: %+v", cur.Summary.Memos)
	}
	r := Check(mustLoad(t, sampleHWPX), cur, Options{})
	if len(r.Changes) != 0 {
		t.Errorf("changes = %+v (the memo is not body text)", r.Changes)
	}
	want := Finding{What: "memo", Loc: "s0/p7", Orig: "가짜 메모: 표현을 다듬을 것", Severity: Violation}
	if len(r.Preserved) != 1 || r.Preserved[0] != want || r.Violations != 1 {
		t.Fatalf("preservation = %+v", r.Preserved)
	}
	if out := plain(t, r); !strings.Contains(out, "! memo at s0/p7 is gone or changed: 가짜 메모: 표현을 다듬을 것\n") {
		t.Errorf("plain:\n%s", out)
	}
}

func TestPreservationCounts(t *testing.T) {
	orig := &model.Document{Format: "hwpx", Blocks: []model.Block{
		{Loc: "s0/p0", Kind: model.KindPara, Text: "표"},
		{Loc: "s0/p0/t0/r0/c0", Kind: model.KindCell, Text: "a"},
	}, Summary: model.Summary{
		Tables:       []model.Table{{Loc: "s0/p0/t0", Rows: 3, Cols: 3}, {Loc: "s0/p0/t0/r0/c0/p0/t0", Rows: 1, Cols: 2}},
		Pictures:     []model.Picture{{Loc: "s0/p0", Ref: "image1"}},
		TrackChanges: 2,
	}}
	cur := &model.Document{Format: "hwpx", Blocks: []model.Block{
		{Loc: "s0/p0", Kind: model.KindPara, Text: "앞 문단"},
		{Loc: "s0/p1", Kind: model.KindPara, Text: "표"},
		{Loc: "s0/p1/t0/r0/c0", Kind: model.KindCell, Text: "a"},
	}, Summary: model.Summary{
		Tables:       []model.Table{{Loc: "s0/p1/t0", Rows: 2, Cols: 3}, {Loc: "s0/p1/t0/r0/c0/p0/t0", Rows: 1, Cols: 2}},
		TrackChanges: 1,
	}}
	r := Check(orig, cur, Options{})
	want := []Finding{
		{What: "table", Loc: "s0/p0/t0", Orig: "3x3", New: "s0/p1/t0 2x3", Severity: Violation},
		{What: "pictures", Orig: "1", New: "0", Severity: Violation},
		{What: "trackChanges", Orig: "2", New: "1", Severity: Violation},
	}
	if len(r.Preserved) != len(want) {
		t.Fatalf("preservation = %+v", r.Preserved)
	}
	for i := range want {
		if r.Preserved[i] != want[i] {
			t.Errorf("finding %d = %+v, want %+v", i, r.Preserved[i], want[i])
		}
	}

	cur.Summary.Tables = cur.Summary.Tables[:1]
	r = Check(orig, cur, Options{})
	if r.Preserved[0] != (Finding{What: "tables", Orig: "2", New: "1", Severity: Violation}) {
		t.Errorf("table count finding = %+v", r.Preserved[0])
	}
}

func TestBanned(t *testing.T) {
	list := "# product names\nWHY-HWP\nre:가짜\\s*(문서|도형)\n"
	pats, err := ParseBanned(strings.NewReader(list), true)
	if err != nil {
		t.Fatal(err)
	}
	r := Check(nil, mustLoad(t, sampleHWPX), Options{Banned: pats})
	type hit struct{ loc, match, pattern string }
	want := []hit{
		{"s0/p2", "why-hwp", "WHY-HWP"},
		{"s0/p2", "가짜 문서", `re:가짜\s*(문서|도형)`},
		{"s0/p8", "가짜 도형", `re:가짜\s*(문서|도형)`},
	}
	if len(r.Banned) != len(want) || r.Violations != 3 {
		t.Fatalf("banned = %+v", r.Banned)
	}
	for i, w := range want {
		if h := r.Banned[i]; h.Loc != w.loc || h.Match != w.match || h.Pattern != w.pattern || h.Severity != Violation {
			t.Errorf("hit %d = %+v, want %+v", i, h, w)
		}
	}
	if h := r.Banned[0]; h.Before != "이 문서는 " || h.After != " 시험용으로 만든 가짜 문서" {
		t.Errorf("context = %q / %q", h.Before, h.After)
	}
	if out := plain(t, r); !strings.Contains(out, "! s0/p2  why-hwp (WHY-HWP)  이 문서는 [why-hwp] 시험용으로 만든 가짜 문서\n") {
		t.Errorf("plain:\n%s", out)
	}

	pats, err = ParseBanned(strings.NewReader("WHY-HWP\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if r := Check(nil, mustLoad(t, sampleHWPX), Options{Banned: pats}); len(r.Banned) != 0 {
		t.Errorf("case-sensitive match found %+v", r.Banned)
	}

	for _, bad := range []string{"re:(", "re:x*"} {
		if _, err := ParseBanned(strings.NewReader(bad), false); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestMarkdown(t *testing.T) {
	orig := load.ParseText([]byte("# 제목\n\n본문 "+em+" 원래 있던 줄\n끝\n"), "md")
	cur := load.ParseText([]byte("# 제목\n\n새 줄 2024"+en+"2025\n본문 "+em+" 원래 있던 줄\n덧붙인 줄 "+em+" 7개\n끝\n"), "md")
	r := Check(orig, cur, Options{})
	want := []Change{
		{Op: "added", Loc: "l3", After: "l2", New: "새 줄 2024" + en + "2025", Severity: Info},
		{Op: "added", Loc: "l5", After: "l3", New: "덧붙인 줄 " + em + " 7개", Severity: Info},
	}
	if len(r.Changes) != len(want) {
		t.Fatalf("changes = %+v", r.Changes)
	}
	for i := range want {
		if r.Changes[i] != want[i] {
			t.Errorf("change %d = %+v, want %+v", i, r.Changes[i], want[i])
		}
	}
	type dash struct {
		loc       string
		unchanged bool
		sev       Severity
	}
	wantDash := []dash{{"l3", false, Warning}, {"l4", true, Warning}, {"l5", false, Violation}}
	if len(r.Dashes) != len(wantDash) {
		t.Fatalf("dashes = %+v", r.Dashes)
	}
	for i, w := range wantDash {
		if h := r.Dashes[i]; h.Loc != w.loc || h.Unchanged != w.unchanged || h.Severity != w.sev {
			t.Errorf("dash %d = %+v, want %+v", i, h, w)
		}
	}
	if len(r.NewNumbers) != 2 || r.NewNumbers[0].Loc != "l3" || r.NewNumbers[1].Loc != "l5" {
		t.Errorf("new numbers = %+v", r.NewNumbers)
	}
	if r.Violations != 1 || r.Warnings != 4 {
		t.Errorf("violations %d, warnings %d; want 1, 4", r.Violations, r.Warnings)
	}
	out := plain(t, r)
	for _, s := range []string{
		"  added    1 after original l2\n",
		"! l5  em dash  덧붙인 줄 [" + em + "] 7개\n",
		"? l4  em dash  본문 [" + em + "] 원래 있던 줄  [unchanged from original]\n",
		"md and txt files carry no memos",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("plain lacks %q:\n%s", s, out)
		}
	}
}

func TestAlignPairsSimilarBlocks(t *testing.T) {
	blocks := func(texts ...string) []model.Block {
		var out []model.Block
		for i, s := range texts {
			out = append(out, model.Block{Loc: "l" + string(rune('1'+i)), Kind: model.KindPara, Text: s})
		}
		return out
	}
	orig := blocks("머리", "사업의 목적은 문서 처리 자동화다", "꼬리")
	cur := blocks("머리", "완전히 새로 넣은 문단", "사업의 목적은 문서 처리의 자동화다", "꼬리")
	pairs, approx := align(orig, cur)
	want := []pair{{0, 0}, {-1, 1}, {1, 2}, {2, 3}}
	if approx || len(pairs) != len(want) {
		t.Fatalf("pairs = %v (approx %v)", pairs, approx)
	}
	for i := range want {
		if pairs[i] != want[i] {
			t.Errorf("pair %d = %v, want %v", i, pairs[i], want[i])
		}
	}

	// An empty cell that gets filled stays the same cell.
	o := []model.Block{{Loc: "s0/p0/t0/r1/c1", Kind: model.KindCell, Text: ""}}
	n := []model.Block{{Loc: "s0/p0/t0/r1/c1", Kind: model.KindCell, Text: "채움"}}
	if pairs, _ := align(o, n); len(pairs) != 1 || pairs[0] != (pair{0, 0}) {
		t.Errorf("filled cell pairs = %v", pairs)
	}
}

func TestAllowMatch(t *testing.T) {
	a, err := ParseAllow(strings.NewReader("s0/p4/t0/*\ns0/p7\ns0/p9/t0/r0/c1\n"))
	if err != nil {
		t.Fatal(err)
	}
	for loc, want := range map[string]bool{
		"s0/p4/t0/r1/c2": true,
		"s0/p4/t0":       false,
		"s0/p4":          false,
		"s0/p7":          true,
		"s0/p70":         false,
		"":               false,
		// A listed cell covers its paragraphs, nothing nested or beside it.
		"s0/p9/t0/r0/c1":             true,
		"s0/p9/t0/r0/c1/p2":          true,
		"s0/p9/t0/r0/c1/p2/t0/r0/c0": false,
		"s0/p9/t0/r0/c1#box":         false,
		"s0/p9/t0/r0/c1/p2#box":      false,
		"s0/p9/t0/r0/c10":            false,
		"s0/p7/p1":                   false,
		"s0/p7#box":                  false,
	} {
		if got := a.Match(loc); got != want {
			t.Errorf("Match(%q) = %v, want %v", loc, got, want)
		}
	}
}

func TestTrackNotes(t *testing.T) {
	doc := func(format string, view model.View) *model.Document {
		return &model.Document{Format: format, Summary: model.Summary{TrackChanges: 1, View: view}}
	}
	for _, tc := range []struct {
		orig, cur *model.Document
		want      []string
	}{
		{nil, doc("hwpx", model.ViewAccepted), []string{"hwpx has tracked changes: read in the accepted view (inserted text kept, deleted text left out)"}},
		{doc("docx", model.ViewOriginal), doc("hwpx", model.ViewOriginal), []string{
			"docx has tracked changes: read in the original view (deleted text kept, inserted text left out)",
			"hwpx has tracked changes: read in the original view (deleted text kept, inserted text left out)",
		}},
		{nil, doc("hwpx", model.ViewMarkup), []string{"hwpx has tracked changes: read in the markup view (inserted text as [+...+], deleted text as [-...-])"}},
		{nil, doc("hwp", ""), []string{"hwp has tracked changes: views are not supported for hwp, so tracked insertions and deletions are not told apart"}},
	} {
		r := Check(tc.orig, tc.cur, Options{})
		if strings.Join(r.Notes, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("notes %q, want %q", r.Notes, tc.want)
		}
	}
}
