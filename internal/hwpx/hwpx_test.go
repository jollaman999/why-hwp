package hwpx

import (
	"archive/zip"
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/jollaman999/why-hwp/internal/model"
)

func TestSampleBlocks(t *testing.T) {
	doc, err := Read("../../testdata/sample.hwpx")
	if err != nil {
		t.Fatal(err)
	}
	para := func(loc, text string) model.Block { return model.Block{Loc: loc, Kind: model.KindPara, Text: text} }
	cell := func(loc, text string) model.Block { return model.Block{Loc: loc, Kind: model.KindCell, Text: text} }
	want := []model.Block{
		para("s0/p0", "샘플 사업 계획서"),
		para("s0/p1", "1. 개요"),
		para("s0/p2", "이 문서는 why-hwp 시험용으로 만든 가짜 문서다. 실제 내용은 없다."),
		para("s0/p3", "2. 추진 일정 및 담당"),
		para("s0/p4", ""),
		cell("s0/p4/t0/r0/c0", "항목"),
		cell("s0/p4/t0/r0/c1", "내용"),
		cell("s0/p4/t0/r0/c2", "담당"),
		cell("s0/p4/t0/r1/c0", "요구사항 정리"),
		cell("s0/p4/t0/r1/c1", "범위와 일정 확정"),
		cell("s0/p4/t0/r1/c2", "(A)"),
		cell("s0/p4/t0/r2/c0", "시제품 개발"),
		cell("s0/p4/t0/r2/c1", "핵심 기능 구현"),
		cell("s0/p4/t0/r2/c2", "(A/B)"),
		para("s0/p5", ""),
		para("s0/p6", "3. 기대 효과"),
		para("s0/p7", "문서 구조를 프로그램이 읽을 수 있다 (가)."),
		para("s0/p8", "그림 1. 가짜 도형"),
		para("s0/p9", ""),
	}
	if !reflect.DeepEqual(doc.Blocks, want) {
		t.Errorf("blocks:\n got %q\nwant %q", doc.Blocks, want)
	}
}

func TestSampleSummary(t *testing.T) {
	doc, err := Read("../../testdata/sample.hwpx")
	if err != nil {
		t.Fatal(err)
	}
	s := doc.Summary
	if s.Format != "hwpx" || s.Sections != 1 || s.Paragraphs != 10 {
		t.Errorf("format %q sections %d paragraphs %d", s.Format, s.Sections, s.Paragraphs)
	}
	if want := []model.Table{{Loc: "s0/p4/t0", Rows: 3, Cols: 3}}; !reflect.DeepEqual(s.Tables, want) {
		t.Errorf("tables = %+v", s.Tables)
	}
	if want := []model.Memo{{Loc: "s0/p7", Text: "가짜 메모: 표현을 다듬을 것"}}; !reflect.DeepEqual(s.Memos, want) {
		t.Errorf("memos = %+v", s.Memos)
	}
	if s.TrackChanges != 0 || s.ParaShapes != 20 || s.CharShapes != 7 {
		t.Errorf("trackChanges %d paraShapes %d charShapes %d", s.TrackChanges, s.ParaShapes, s.CharShapes)
	}
	if len(s.Styles) != 23 || s.Styles[0] != (model.Style{ID: "0", Type: "PARA", Name: "바탕글", EngName: "Normal"}) {
		t.Errorf("styles = %+v", s.Styles)
	}
	if want := []model.Picture{{Loc: "s0/p9", Ref: "image1"}}; !reflect.DeepEqual(s.Pictures, want) {
		t.Errorf("pictures = %+v", s.Pictures)
	}
	if len(s.BinData) != 1 || s.BinData[0].ID != "image1" || s.BinData[0].Path != "BinData/image1.png" || s.BinData[0].Size == 0 {
		t.Errorf("binData = %+v", s.BinData)
	}
	want := []model.OwnerMark{
		{Mark: "(A)", Count: 1, Locs: []string{"s0/p4/t0/r1/c2"}},
		{Mark: "(A/B)", Count: 1, Locs: []string{"s0/p4/t0/r2/c2"}},
		{Mark: "(가)", Count: 1, Locs: []string{"s0/p7"}},
	}
	if !reflect.DeepEqual(s.OwnerMarks, want) {
		t.Errorf("owner marks = %+v", s.OwnerMarks)
	}
}

const ns = `xmlns:hp="http://www.hancom.co.kr/hwpml/2011/paragraph" xmlns:hs="http://www.hancom.co.kr/hwpml/2011/section" xmlns:hh="http://www.hancom.co.kr/hwpml/2011/head" xmlns:hc="http://www.hancom.co.kr/hwpml/2011/core" xmlns:opf="http://www.idpf.org/2007/opf/"`

func tc(row, col int, body string) string {
	return `<hp:tc><hp:subList>` + body + `</hp:subList><hp:cellAddr colAddr="` + itoa(col) + `" rowAddr="` + itoa(row) + `"/></hp:tc>`
}

func itoa(n int) string { return string(rune('0' + n)) }

func p(body string) string { return `<hp:p><hp:run>` + body + `</hp:run></hp:p>` }

func ht(s string) string { return `<hp:t>` + s + `</hp:t>` }

func buildHWPX(t *testing.T, parts map[string]string, order []string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("application/hwp+zip"))
	for _, name := range order {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(parts[name]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestStructure(t *testing.T) {
	// Section 1 is listed first in the spine; the spine order wins over
	// the file name.
	hpf := `<opf:package ` + ns + `><opf:manifest>` +
		`<opf:item id="header" href="Contents/header.xml" media-type="application/xml"/>` +
		`<opf:item id="section0" href="Contents/section0.xml" media-type="application/xml"/>` +
		`<opf:item id="section1" href="Contents/section1.xml" media-type="application/xml"/>` +
		`<opf:item id="img7" href="BinData/img7.bmp" media-type="image/bmp"/>` +
		`</opf:manifest><opf:spine><opf:itemref idref="header"/><opf:itemref idref="section1"/><opf:itemref idref="section0"/></opf:spine></opf:package>`
	header := `<hh:head ` + ns + `><hh:refList>` +
		`<hh:charProperties itemCnt="2"><hh:charPr id="0"/><hh:charPr id="1"/></hh:charProperties>` +
		`<hh:paraProperties itemCnt="1"><hh:paraPr id="0"/></hh:paraProperties>` +
		`<hh:styles itemCnt="1"><hh:style id="0" type="PARA" name="바탕글" engName="Normal"/></hh:styles>` +
		`<hh:trackChanges itemCnt="2"><hh:trackChange type="Insert" id="1"/><hh:trackChange type="Delete" id="2"/></hh:trackChanges>` +
		`</hh:refList></hh:head>`
	nested := `<hp:tbl rowCnt="1" colCnt="2"><hp:tr>` + tc(0, 0, p(ht("안1"))) + tc(0, 1, p(ht("안2 (B)"))) + `</hp:tr></hp:tbl>`
	sec1 := `<hs:sec ` + ns + `>` +
		p(ht("머리")+`<hp:ctrl><hp:header><hp:subList>`+p(ht("머리말 글"))+`</hp:subList></hp:header></hp:ctrl>`) +
		p(ht(`가<hp:tab width="100"/>나<hp:lineBreak/>다`)) +
		p(`<hp:insertBegin Id="1" TcId="1"/>`+ht("넣음")+`<hp:insertEnd Id="1" TcId="1"/>`+
			`<hp:ctrl><hp:footNote><hp:subList>`+p(ht("각주 글"))+`</hp:subList></hp:footNote></hp:ctrl>`) +
		p(`<hp:tbl rowCnt="2" colCnt="3"><hp:tr>`+
			tc(0, 0, p(ht("병합"))+p(ht("둘째 줄")))+
			tc(0, 2, p(ht("바깥 (A)"))+p(nested))+
			`</hp:tr><hp:tr>`+tc(1, 0, p(ht("a")))+tc(1, 1, p(ht("b")))+tc(1, 2, p(ht("c")))+
			`</hp:tr></hp:tbl>`) +
		p(`<hp:rect><hp:drawText><hp:subList>`+p(ht("상자 1"))+p(ht("상자 2"))+`</hp:subList></hp:drawText></hp:rect>`+ht("본문")) +
		`</hs:sec>`
	sec0 := `<hs:sec ` + ns + `>` + p(ht("둘째 구역")) + `</hs:sec>`

	zr := buildHWPX(t, map[string]string{
		"Contents/content.hpf":  hpf,
		"Contents/header.xml":   header,
		"Contents/section0.xml": sec0,
		"Contents/section1.xml": sec1,
		"BinData/img7.bmp":      "BM",
		"BinData/stray.png":     "png",
	}, []string{"Contents/content.hpf", "Contents/header.xml", "Contents/section0.xml", "Contents/section1.xml", "BinData/img7.bmp", "BinData/stray.png"})
	doc, err := Parse(zr)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, b := range doc.Blocks {
		got = append(got, string(b.Kind)+" "+b.Loc+" "+b.Text)
	}
	want := []string{
		"para s0/p0 머리",
		"para s0/p1 가\t나\n다",
		"para s0/p2 넣음",
		"para s0/p3 ",
		"cell s0/p3/t0/r0/c0/p0 병합",
		"cell s0/p3/t0/r0/c0/p1 둘째 줄",
		"cell s0/p3/t0/r0/c2/p0 바깥 (A)",
		"cell s0/p3/t0/r0/c2/p1 ",
		"cell s0/p3/t0/r0/c2/p1/t0/r0/c0 안1",
		"cell s0/p3/t0/r0/c2/p1/t0/r0/c1 안2 (B)",
		"cell s0/p3/t0/r1/c0 a",
		"cell s0/p3/t0/r1/c1 b",
		"cell s0/p3/t0/r1/c2 c",
		"para s0/p4 본문",
		"box s0/p4#box 상자 1\n상자 2",
		"para s1/p0 둘째 구역",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("blocks:\n got %q\nwant %q", got, want)
	}

	s := doc.Summary
	if s.Sections != 2 || s.Paragraphs != 6 || s.TrackChanges != 2 || s.ParaShapes != 1 || s.CharShapes != 2 || len(s.Styles) != 1 {
		t.Errorf("summary = %+v", s)
	}
	wantTables := []model.Table{
		{Loc: "s0/p3/t0", Rows: 2, Cols: 3},
		{Loc: "s0/p3/t0/r0/c2/p1/t0", Rows: 1, Cols: 2},
	}
	if !reflect.DeepEqual(s.Tables, wantTables) {
		t.Errorf("tables = %+v", s.Tables)
	}
	wantBin := []model.BinItem{
		{ID: "img7", Path: "BinData/img7.bmp", MediaType: "image/bmp", Size: 2},
		{Path: "BinData/stray.png", Size: 3},
	}
	if !reflect.DeepEqual(s.BinData, wantBin) {
		t.Errorf("binData = %+v", s.BinData)
	}
	for _, b := range doc.Blocks {
		if strings.Contains(b.Text, "머리말 글") || strings.Contains(b.Text, "각주 글") {
			t.Errorf("header or footnote text leaked into %s", b.Loc)
		}
	}
	if len(s.OwnerMarks) != 2 || s.OwnerMarks[0].Mark != "(A)" || s.OwnerMarks[1].Locs[0] != "s0/p3/t0/r0/c2/p1/t0/r0/c1" {
		t.Errorf("owner marks = %+v", s.OwnerMarks)
	}
}

func TestNoManifest(t *testing.T) {
	zr := buildHWPX(t, map[string]string{
		"Contents/section10.xml": `<hs:sec ` + ns + `>` + p(ht("열")) + `</hs:sec>`,
		"Contents/section2.xml":  `<hs:sec ` + ns + `>` + p(ht("둘")) + `</hs:sec>`,
	}, []string{"Contents/section10.xml", "Contents/section2.xml"})
	doc, err := Parse(zr)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 2 || doc.Blocks[0].Text != "둘" || doc.Blocks[1].Loc != "s1/p0" {
		t.Errorf("blocks = %+v", doc.Blocks)
	}
}

func TestTruncatedSection(t *testing.T) {
	zr := buildHWPX(t, map[string]string{
		"Contents/section0.xml": `<hs:sec ` + ns + `><hp:p><hp:run><hp:t>끊김`,
	}, []string{"Contents/section0.xml"})
	if _, err := Parse(zr); err == nil {
		t.Fatal("want error for truncated section")
	}
}

// TestTrackedChanges reads markers written the way apply writes them: a
// replacement, a new paragraph, a deleted paragraph, a deleted cell
// paragraph, and a paragraph added after the last one, which marks the
// break of the paragraph before it as inserted.
func TestTrackedChanges(t *testing.T) {
	ins := func(id, text, paraEnd string) string {
		return `<hp:insertBegin Id="` + id + `" TcId="1"/>` + text + `<hp:insertEnd Id="` + id + `" TcId="1" paraend="` + paraEnd + `"/>`
	}
	del := func(id, text, paraEnd string) string {
		return `<hp:deleteBegin Id="` + id + `" TcId="2"/>` + text + `<hp:deleteEnd Id="` + id + `" TcId="2" paraend="` + paraEnd + `"/>`
	}
	sec := `<hs:sec ` + ns + `>` +
		p(ht("제목")) +
		p(ht(del("1", "옛 글 (A)", "0")+ins("2", "새 글 (B)", "0"))) +
		p(ht(ins("3", "넣은 문단", "1"))) +
		p(ht(`<hp:deleteBegin Id="4" TcId="2"/>`)+ht("지운 문단")+ht(`<hp:deleteEnd Id="4" TcId="2" paraend="1"/>`)) +
		p(`<hp:tbl rowCnt="1" colCnt="1"><hp:tr>`+tc(0, 0, p(ht("가"))+p(ht(del("5", "나", "1")))+p(ht("다")))+`</hp:tr></hp:tbl>`) +
		p(`<hp:pic><hp:img binaryItemIDRef="i1"/></hp:pic>`+ht(ins("6", "", "1"))) +
		p(ht(ins("7", "끝 글", "0"))) +
		`</hs:sec>`
	zr := buildHWPX(t, map[string]string{"Contents/section0.xml": sec}, []string{"Contents/section0.xml"})
	doc, err := Parse(zr)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		view model.View
		want []string
	}{
		{model.ViewAccepted, []string{
			"s0/p0 제목",
			"s0/p1 새 글 (B)",
			"s0/p2 넣은 문단",
			"s0/p4 ",
			"s0/p4/t0/r0/c0/p0 가",
			"s0/p4/t0/r0/c0/p2 다",
			"s0/p5 ",
			"s0/p6 끝 글",
		}},
		{model.ViewOriginal, []string{
			"s0/p0 제목",
			"s0/p1 옛 글 (A)",
			"s0/p3 지운 문단",
			"s0/p4 ",
			"s0/p4/t0/r0/c0/p0 가",
			"s0/p4/t0/r0/c0/p1 나",
			"s0/p4/t0/r0/c0/p2 다",
			"s0/p5 ",
		}},
		{model.ViewMarkup, []string{
			"s0/p0 제목",
			"s0/p1 [-옛 글 (A)-][+새 글 (B)+]",
			"s0/p2 [+넣은 문단\n+]",
			"s0/p3 [-지운 문단\n-]",
			"s0/p4 ",
			"s0/p4/t0/r0/c0/p0 가",
			"s0/p4/t0/r0/c0/p1 [-나\n-]",
			"s0/p4/t0/r0/c0/p2 다",
			"s0/p5 [+\n+]",
			"s0/p6 [+끝 글+]",
		}},
	} {
		var got []string
		for _, b := range doc.InView(tc.view).Blocks {
			got = append(got, b.Loc+" "+b.Text)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.view, got, tc.want)
		}
	}
	// Without a view the blocks hold each paragraph's accepted text.
	if b := doc.Blocks[1]; b.Text != "새 글 (B)" || b.Break != 0 {
		t.Errorf("raw s0/p1 = %+v", b)
	}
	if b := doc.Blocks[3]; b.Text != "" || b.Break != model.Deleted || b.Whole != model.Deleted {
		t.Errorf("raw s0/p3 = %+v", b)
	}
	if b := doc.Blocks[6]; b.Loc != "s0/p4/t0/r0/c0/p1" || b.Text != "" || b.Break != model.Deleted || b.Whole != model.Deleted {
		t.Errorf("raw cell paragraph = %+v", b)
	}
	if b := doc.Blocks[8]; b.Break != model.Inserted || b.Whole != 0 {
		t.Errorf("raw s0/p5 = %+v", b)
	}
	if len(doc.Summary.Pictures) != 1 || doc.Summary.Pictures[0].Loc != "s0/p5" {
		t.Errorf("pictures = %+v", doc.Summary.Pictures)
	}
}
