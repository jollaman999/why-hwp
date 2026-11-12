package docx

import (
	"archive/zip"
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/jollaman999/why-hwp/internal/model"
)

const docNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
	`xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" ` +
	`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
	`xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture" ` +
	`xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape" ` +
	`xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" ` +
	`xmlns:v="urn:schemas-microsoft-com:vml"`

func run(s string) string { return `<w:r><w:t xml:space="preserve">` + s + `</w:t></w:r>` }

func para(body string) string { return `<w:p>` + body + `</w:p>` }

// anchoredPicture is a floating picture: its position numbers sit in
// wp:posOffset and must not show up as text.
const anchoredPicture = `<w:r><w:drawing><wp:anchor>` +
	`<wp:positionH relativeFrom="column"><wp:posOffset>1828800</wp:posOffset></wp:positionH>` +
	`<wp:positionV relativeFrom="paragraph"><wp:posOffset>-254000</wp:posOffset></wp:positionV>` +
	`<wp:extent cx="914400" cy="457200"/>` +
	`<a:graphic><a:graphicData><pic:pic><pic:blipFill><a:blip r:embed="rId5"/></pic:blipFill></pic:pic></a:graphicData></a:graphic>` +
	`</wp:anchor></w:drawing></w:r>`

// textBox is a text box written the way Word does: the same text once in
// mc:Choice (DrawingML) and again in mc:Fallback (VML).
const textBox = `<w:r><mc:AlternateContent>` +
	`<mc:Choice Requires="wps"><w:drawing><wp:anchor><wp:positionH relativeFrom="column"><wp:posOffset>3000</wp:posOffset></wp:positionH>` +
	`<a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent>` +
	`<w:p><w:r><w:t>상자 글</w:t></w:r></w:p>` +
	`</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:anchor></w:drawing></mc:Choice>` +
	`<mc:Fallback><w:pict><v:shape><v:textbox><w:txbxContent>` +
	`<w:p><w:r><w:t>상자 글</w:t></w:r></w:p>` +
	`</w:txbxContent></v:textbox></v:shape></w:pict></mc:Fallback>` +
	`</mc:AlternateContent></w:r>`

func cellXML(span int, body string) string {
	pr := ""
	if span > 1 {
		pr = `<w:tcPr><w:gridSpan w:val="` + string(rune('0'+span)) + `"/></w:tcPr>`
	}
	return `<w:tc>` + pr + body + `</w:tc>`
}

func buildDocx(t *testing.T, parts map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
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

func sampleParts() map[string]string {
	nested := `<w:tbl><w:tblGrid><w:gridCol/></w:tblGrid><w:tr>` + cellXML(1, para(run("안쪽 (B)"))) + `</w:tr></w:tbl>`
	body := para(`<w:pPr><w:pStyle w:val="Title"/><w:tabs><w:tab w:val="left" w:pos="720"/></w:tabs></w:pPr>`+run("제목")) +
		para(run("그림 앞 ")+anchoredPicture+run("그림 뒤")) +
		para(run("앞 ")+textBox+run("뒤")) +
		para(run("가")+`<w:r><w:tab/></w:r>`+run("나")+`<w:r><w:br/></w:r>`+run("다")) +
		para(`<w:ins w:id="1" w:author="x">`+run("넣음")+`</w:ins><w:del w:id="2" w:author="x"><w:r><w:delText>지움</w:delText></w:r></w:del>`) +
		`<w:tbl><w:tblGrid><w:gridCol/><w:gridCol/><w:gridCol/></w:tblGrid>` +
		`<w:tr>` + cellXML(2, para(run("합친 칸"))+para(run("둘째 줄"))) + cellXML(1, para(run("(A)"))) + `</w:tr>` +
		`<w:tr>` + cellXML(1, para(run("a"))) + cellXML(1, para(run("b"))) + cellXML(1, para(run("c"))+nested) + `</w:tr>` +
		`</w:tbl>` +
		para(run("메모 붙은 글")+`<w:r><w:commentReference w:id="0"/></w:r>`) +
		`<w:sectPr/>`
	return map[string]string{
		"_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId5" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/></Relationships>`,
		"word/document.xml": `<w:document ` + docNS + `><w:body>` + body + `</w:body></w:document>`,
		"word/styles.xml": `<w:styles ` + docNS + `>` +
			`<w:style w:type="paragraph" w:styleId="Normal"><w:name w:val="Normal"/></w:style>` +
			`<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/></w:style></w:styles>`,
		"word/comments.xml": `<w:comments ` + docNS + `><w:comment w:id="0" w:author="x">` +
			para(run("가짜 의견")) + `</w:comment></w:comments>`,
		"word/media/image1.png": "png!",
	}
}

func TestBlocks(t *testing.T) {
	doc, err := Parse(buildDocx(t, sampleParts()))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range doc.Blocks {
		got = append(got, string(b.Kind)+" "+b.Loc+" "+b.Text)
	}
	want := []string{
		"para s0/p0 제목",
		"para s0/p1 그림 앞 그림 뒤",
		"para s0/p2 앞 뒤",
		"box s0/p2#box 상자 글",
		"para s0/p3 가\t나\n다",
		"para s0/p4 넣음",
		"cell s0/p5/t0/r0/c0/p0 합친 칸",
		"cell s0/p5/t0/r0/c0/p1 둘째 줄",
		"cell s0/p5/t0/r0/c2 (A)",
		"cell s0/p5/t0/r1/c0 a",
		"cell s0/p5/t0/r1/c1 b",
		"cell s0/p5/t0/r1/c2 c",
		"cell s0/p5/t0/r1/c2/p1/t0/r0/c0 안쪽 (B)",
		"para s0/p6 메모 붙은 글",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("blocks:\n got %q\nwant %q", got, want)
	}
}

// TestTraps checks the two known docx extraction traps directly.
func TestTraps(t *testing.T) {
	doc, err := Parse(buildDocx(t, sampleParts()))
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, b := range doc.Blocks {
		all.WriteString(b.Text)
	}
	for _, bad := range []string{"1828800", "-254000", "3000", "지움"} {
		if strings.Contains(all.String(), bad) {
			t.Errorf("text contains %q:\n%s", bad, all.String())
		}
	}
	if n := strings.Count(all.String(), "상자 글"); n != 1 {
		t.Errorf("text box text appears %d times, want 1", n)
	}
}

func TestSummary(t *testing.T) {
	doc, err := Parse(buildDocx(t, sampleParts()))
	if err != nil {
		t.Fatal(err)
	}
	s := doc.Summary
	if s.Format != "docx" || s.Sections != 1 || s.Paragraphs != 6 || s.TrackChanges != 2 {
		t.Errorf("format %q sections %d paragraphs %d trackChanges %d", s.Format, s.Sections, s.Paragraphs, s.TrackChanges)
	}
	wantTables := []model.Table{
		{Loc: "s0/p5/t0", Rows: 2, Cols: 3},
		{Loc: "s0/p5/t0/r1/c2/p1/t0", Rows: 1, Cols: 1},
	}
	if !reflect.DeepEqual(s.Tables, wantTables) {
		t.Errorf("tables = %+v", s.Tables)
	}
	if want := []model.Memo{{Loc: "s0/p6", Text: "가짜 의견"}}; !reflect.DeepEqual(s.Memos, want) {
		t.Errorf("memos = %+v", s.Memos)
	}
	if want := []model.Picture{{Loc: "s0/p1", Ref: "rId5"}}; !reflect.DeepEqual(s.Pictures, want) {
		t.Errorf("pictures = %+v", s.Pictures)
	}
	if want := []model.BinItem{{ID: "rId5", Path: "word/media/image1.png", Size: 4}}; !reflect.DeepEqual(s.BinData, want) {
		t.Errorf("binData = %+v", s.BinData)
	}
	if len(s.Styles) != 2 || s.Styles[1] != (model.Style{ID: "Title", Type: "paragraph", Name: "Title"}) {
		t.Errorf("styles = %+v", s.Styles)
	}
	if len(s.OwnerMarks) != 2 || s.OwnerMarks[0].Mark != "(A)" || s.OwnerMarks[1].Locs[0] != "s0/p5/t0/r1/c2/p1/t0/r0/c0" {
		t.Errorf("owner marks = %+v", s.OwnerMarks)
	}
}

func TestMissingMainPart(t *testing.T) {
	if _, err := Parse(buildDocx(t, map[string]string{"word/styles.xml": "<x/>"})); err == nil {
		t.Fatal("want error when word/document.xml is missing")
	}
}

func TestTrackedChanges(t *testing.T) {
	const a = ` w:author="x"`
	body := para(run("제목")+`<w:r><w:rPr><w:b/></w:rPr><w:t>굵게</w:t></w:r>`) +
		para(`<w:del w:id="1"`+a+`><w:r><w:delText>옛 글</w:delText></w:r></w:del><w:ins w:id="2"`+a+`>`+run("새 글")+`</w:ins>`) +
		para(`<w:pPr><w:rPr><w:ins w:id="3"`+a+`/></w:rPr></w:pPr><w:ins w:id="4"`+a+`>`+run("넣은 문단")+`</w:ins>`) +
		para(`<w:pPr><w:rPr><w:del w:id="5"`+a+`/></w:rPr></w:pPr><w:del w:id="6"`+a+`><w:r><w:delText>지운 문단</w:delText></w:r></w:del>`) +
		para(run("앞")+`<w:moveFrom w:id="7"`+a+`><w:r><w:delText>옮긴 글</w:delText></w:r></w:moveFrom>`) +
		`<w:tbl><w:tblGrid><w:gridCol/></w:tblGrid><w:tr>` +
		cellXML(1, para(`<w:pPr><w:rPr><w:del w:id="8"`+a+`/></w:rPr></w:pPr>`+run("가"))+para(run("나"))) +
		`</w:tr></w:tbl>` +
		para(run("끝"))
	doc, err := Parse(buildDocx(t, map[string]string{
		"word/document.xml": `<w:document ` + docNS + `><w:body>` + body + `</w:body></w:document>`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		view model.View
		want []string
	}{
		{model.ViewAccepted, []string{
			"s0/p0 제목굵게",
			"s0/p1 새 글",
			"s0/p2 넣은 문단",
			"s0/p4 앞",
			"s0/p5/t0/r0/c0/p0 가나",
			"s0/p6 끝",
		}},
		{model.ViewOriginal, []string{
			"s0/p0 제목굵게",
			"s0/p1 옛 글",
			"s0/p3 지운 문단",
			"s0/p4 앞옮긴 글",
			"s0/p5/t0/r0/c0/p0 가",
			"s0/p5/t0/r0/c0/p1 나",
			"s0/p6 끝",
		}},
		{model.ViewMarkup, []string{
			"s0/p0 제목굵게",
			"s0/p1 [-옛 글-][+새 글+]",
			"s0/p2 [+넣은 문단\n+]",
			"s0/p3 [-지운 문단\n-]",
			"s0/p4 앞[-옮긴 글-]",
			"s0/p5/t0/r0/c0/p0 가[-\n-]",
			"s0/p5/t0/r0/c0/p1 나",
			"s0/p6 끝",
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
}
