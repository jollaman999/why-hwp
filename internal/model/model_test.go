package model

import (
	"reflect"
	"testing"
)

func TestFindOwnerMarks(t *testing.T) {
	blocks := []Block{
		{Loc: "s0/p0", Text: "제목 (참고용 아님)"},
		{Loc: "s0/p1", Text: "준비 (A) 검토 (A)"},
		{Loc: "s0/p2/t0/r0/c1", Text: "(A, B)"},
		{Loc: "s0/p3", Text: "(가/나) (a) (2026) (VERYLONGOWNERNAMEX)"},
	}
	want := []OwnerMark{
		{Mark: "(A)", Count: 2, Locs: []string{"s0/p1", "s0/p1"}},
		{Mark: "(A, B)", Count: 1, Locs: []string{"s0/p2/t0/r0/c1"}},
		{Mark: "(가/나)", Count: 1, Locs: []string{"s0/p3"}},
		{Mark: "(참고용 아님)", Count: 1, Locs: []string{"s0/p0"}},
	}
	if got := FindOwnerMarks(blocks); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}
