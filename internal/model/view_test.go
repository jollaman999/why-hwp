package model

import (
	"reflect"
	"testing"
)

// trackedDoc is a section as apply writes it: s0/p1 is a replacement, s0/p2
// a new paragraph, s0/p3 a deleted one, and the last paragraph s0/p5 is
// deleted with the break before it, so s0/p4 loses its break instead.
func trackedDoc() *Document {
	return &Document{Format: "hwpx", Tracked: true, Blocks: []Block{
		{Loc: "s0/p0", Kind: KindPara, Text: "제목 (A)"},
		{Loc: "s0/p1", Kind: KindPara, Text: "새 글", Spans: Spans{{"옛 글 (B)", Deleted}, {"새 글", Inserted}}},
		{Loc: "s0/p2", Kind: KindPara, Text: "넣은 문단", Spans: Spans{{"넣은 문단", Inserted}}, Break: Inserted, Whole: Inserted},
		{Loc: "s0/p3", Kind: KindPara, Text: "", Spans: Spans{{"지운 문단", Deleted}}, Break: Deleted, Whole: Deleted},
		{Loc: "s0/p4", Kind: KindPara, Text: "표 앞", Break: Deleted},
		{Loc: "s0/p4/t0/r0/c0", Kind: KindCell, Text: "가\n다", Spans: Spans{{"가", 0}, {"\n나", Deleted}, {"\n다", Inserted}}},
		{Loc: "s0/p5", Kind: KindPara, Text: "", Spans: Spans{{"끝", Deleted}}},
		{Loc: "s1/p0", Kind: KindPara, Text: "다음 구역"},
	}}
}

func blockTexts(d *Document) []string {
	var out []string
	for _, b := range d.Blocks {
		out = append(out, b.Loc+" "+b.Text)
	}
	return out
}

func TestInView(t *testing.T) {
	for _, tc := range []struct {
		view  View
		want  []string
		marks []string
	}{
		{ViewAccepted, []string{
			"s0/p0 제목 (A)",
			"s0/p1 새 글",
			"s0/p2 넣은 문단",
			"s0/p4 표 앞",
			"s0/p4/t0/r0/c0 가\n다",
			"s1/p0 다음 구역",
		}, []string{"(A)"}},
		{ViewOriginal, []string{
			"s0/p0 제목 (A)",
			"s0/p1 옛 글 (B)",
			"s0/p3 지운 문단",
			"s0/p4 표 앞",
			"s0/p4/t0/r0/c0 가\n나",
			"s0/p5 끝",
			"s1/p0 다음 구역",
		}, []string{"(A)", "(B)"}},
		{ViewMarkup, []string{
			"s0/p0 제목 (A)",
			"s0/p1 [-옛 글 (B)-][+새 글+]",
			"s0/p2 [+넣은 문단\n+]",
			"s0/p3 [-지운 문단\n-]",
			"s0/p4 표 앞[-\n-]",
			"s0/p4/t0/r0/c0 가[-\n나-][+\n다+]",
			"s0/p5 [-끝-]",
			"s1/p0 다음 구역",
		}, []string{"(A)", "(B)"}},
	} {
		got := trackedDoc().InView(tc.view)
		if w := blockTexts(got); !reflect.DeepEqual(w, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.view, w, tc.want)
		}
		var marks []string
		for _, m := range got.Summary.OwnerMarks {
			marks = append(marks, m.Mark)
		}
		if !reflect.DeepEqual(marks, tc.marks) {
			t.Errorf("%s: owner marks %q, want %q", tc.view, marks, tc.marks)
		}
		if got.Summary.View != tc.view {
			t.Errorf("%s: summary view %q", tc.view, got.Summary.View)
		}
		for _, b := range got.Blocks {
			if b.Spans != nil || b.Break != 0 || b.Whole != 0 {
				t.Errorf("%s: %s keeps change data", tc.view, b.Loc)
			}
		}
	}
}

// A paragraph whose break is gone is joined to the next one, and the text
// stays at the first paragraph the view still has.
func TestInViewJoins(t *testing.T) {
	d := &Document{Tracked: true, Blocks: []Block{
		{Loc: "s0/p0", Kind: KindPara, Text: "앞", Break: Deleted},
		{Loc: "s0/p1", Kind: KindPara, Text: "뒤"},
	}}
	if got := blockTexts(d.InView(ViewAccepted)); !reflect.DeepEqual(got, []string{"s0/p0 앞뒤"}) {
		t.Errorf("accepted %q", got)
	}
	if got := blockTexts(d.InView(ViewOriginal)); !reflect.DeepEqual(got, []string{"s0/p0 앞", "s0/p1 뒤"}) {
		t.Errorf("original %q", got)
	}
}

func TestInViewUntracked(t *testing.T) {
	d := &Document{Format: "hwp", Blocks: []Block{{Loc: "s0/p0", Kind: KindPara, Text: "x"}}}
	if got := d.InView(ViewOriginal); got != d || got.Summary.View != "" {
		t.Errorf("reader without views: %+v", got)
	}
}

func TestParseView(t *testing.T) {
	for _, s := range []string{"accepted", "original", "markup"} {
		if v, err := ParseView(s); err != nil || string(v) != s {
			t.Errorf("%s: %v %v", s, v, err)
		}
	}
	if _, err := ParseView("final"); err == nil {
		t.Error("final: want error")
	}
}
