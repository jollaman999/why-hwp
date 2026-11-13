package apply

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jollaman999/why-hwp/internal/hwpx"
	"github.com/jollaman999/why-hwp/internal/model"
)

const samplePath = "../../testdata/sample.hwpx"

var testDate = time.Date(2026, 11, 11, 5, 0, 0, 0, time.UTC)

func readSample(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func str(s string) *string { return &s }

func applyOps(t *testing.T, src []byte, track bool, ops ...Op) *Result {
	t.Helper()
	res, err := Apply(src, &Spec{Ops: ops}, Options{Track: track, Author: "Tester", Date: testDate, BaseDir: "../../testdata/apply"})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func runErr(t *testing.T, src []byte, track bool, ops ...Op) error {
	t.Helper()
	_, err := Apply(src, &Spec{Ops: ops}, Options{Track: track, Author: "Tester", Date: testDate, BaseDir: "../../testdata/apply"})
	if err == nil {
		t.Fatalf("ops %+v: no error", ops)
	}
	return err
}

func parse(t *testing.T, data []byte) *model.Document {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := hwpx.Parse(zr)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func texts(t *testing.T, data []byte) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, b := range parse(t, data).Blocks {
		m[b.Loc] = b.Text
	}
	return m
}

// viewTexts returns the text of each block in view v.
func viewTexts(t *testing.T, data []byte, v model.View) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, b := range parse(t, data).InView(v).Blocks {
		m[b.Loc] = b.Text
	}
	return m
}

func part(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			b, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
	}
	t.Fatalf("no %s in package", name)
	return ""
}

// paraXML returns the raw XML of the paragraph at loc in the given package.
func paraXML(t *testing.T, data []byte, loc string) string {
	t.Helper()
	raw := part(t, data, "Contents/section0.xml")
	s, err := indexSection(0, "Contents/section0.xml", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	p := s.byLoc[loc]
	if p == nil {
		if c := s.cells[loc]; c != nil {
			p = c.paras[0]
		}
	}
	if p == nil {
		t.Fatalf("no %s", loc)
	}
	return raw[p.start:p.end]
}

func TestIndexMatchesReader(t *testing.T) {
	for _, src := range [][]byte{readSample(t), nestedDoc(t)} {
		raw := part(t, src, "Contents/section0.xml")
		s, err := indexSection(0, "Contents/section0.xml", []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		// A cell with one paragraph is one block at the cell's loc, a cell
		// with more is one block per paragraph.
		var got []string
		for loc := range s.byLoc {
			if strings.Count(loc, "/t") == 0 {
				got = append(got, loc)
			}
		}
		for loc, c := range s.cells {
			if len(c.paras) == 1 {
				got = append(got, loc)
				continue
			}
			for _, p := range c.paras {
				got = append(got, p.loc)
			}
		}
		var want []string
		for _, b := range parse(t, src).Blocks {
			want = append(want, b.Loc)
		}
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("locs:\n got %v\nwant %v", got, want)
		}
	}
}

// nestedDoc puts a 1x2 table inside cell r0/c0 of the sample's table.
func nestedDoc(t *testing.T) []byte {
	t.Helper()
	src := readSample(t)
	raw := part(t, src, "Contents/section0.xml")
	inner := `<hp:p id="0" paraPrIDRef="0" styleIDRef="0" pageBreak="0" columnBreak="0" merged="0"><hp:run charPrIDRef="0"><hp:tbl id="7" rowCnt="1" colCnt="2"><hp:tr>` +
		`<hp:tc><hp:subList><hp:p id="0" paraPrIDRef="0" styleIDRef="0"><hp:run charPrIDRef="0"><hp:t>안쪽 왼쪽</hp:t></hp:run></hp:p></hp:subList><hp:cellAddr colAddr="0" rowAddr="0"/></hp:tc>` +
		`<hp:tc><hp:subList><hp:p id="0" paraPrIDRef="0" styleIDRef="0"><hp:run charPrIDRef="0"><hp:t>안쪽 오른쪽</hp:t></hp:run></hp:p></hp:subList><hp:cellAddr colAddr="1" rowAddr="0"/></hp:tc>` +
		`</hp:tr></hp:tbl><hp:t/></hp:run></hp:p>`
	anchor := `<hp:t>항목</hp:t></hp:run>`
	i := strings.Index(raw, anchor)
	if i < 0 {
		t.Fatal("anchor not found")
	}
	j := strings.Index(raw[i:], "</hp:p>") + i + len("</hp:p>")
	return withSection(t, src, raw[:j]+inner+raw[j:])
}

// withSection returns src with section0.xml replaced, written the plain
// way (no apply code involved).
func withSection(t *testing.T, src []byte, section string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: f.Method})
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == "Contents/section0.xml" {
			io.WriteString(w, section)
			continue
		}
		rc, _ := f.Open()
		io.Copy(w, rc)
		rc.Close()
	}
	zw.Close()
	return buf.Bytes()
}

func TestSetText(t *testing.T) {
	src := readSample(t)
	old := "이 문서는 why-hwp 시험용으로 만든 가짜 문서다. 실제 내용은 없다."

	res := applyOps(t, src, true, Op{Op: "set_text", Loc: "s0/p2", Text: str("새 글")})
	if got := viewTexts(t, res.Data, model.ViewAccepted)["s0/p2"]; got != "새 글" {
		t.Errorf("tracked s0/p2 accepted = %q", got)
	}
	if got := viewTexts(t, res.Data, model.ViewOriginal)["s0/p2"]; got != old {
		t.Errorf("tracked s0/p2 original = %q", got)
	}
	x := paraXML(t, res.Data, "s0/p2")
	want := `<hp:t><hp:deleteBegin Id="2" TcId="2"/>` + old + `<hp:deleteEnd Id="2" TcId="2" paraend="0"/><hp:insertBegin Id="1" TcId="1"/>새 글<hp:insertEnd Id="1" TcId="1" paraend="0"/></hp:t>`
	if !strings.Contains(x, want) {
		t.Errorf("tracked XML:\n%s\nwant part\n%s", x, want)
	}
	if strings.Contains(x, "linesegarray") {
		t.Error("edited paragraph keeps its linesegarray")
	}

	res = applyOps(t, src, false, Op{Op: "set_text", Loc: "s0/p2", Text: str("새 글")})
	if got := texts(t, res.Data)["s0/p2"]; got != "새 글" {
		t.Errorf("untracked s0/p2 = %q", got)
	}
	if x := paraXML(t, res.Data, "s0/p2"); strings.Contains(x, "Begin") {
		t.Errorf("untracked edit has markers: %s", x)
	}

	// A cell names its first paragraph.
	res = applyOps(t, src, false, Op{Op: "set_text", Loc: "s0/p4/t0/r1/c2", Text: str("(C)")})
	if got := texts(t, res.Data)["s0/p4/t0/r1/c2"]; got != "(C)" {
		t.Errorf("cell = %q", got)
	}
	// An empty paragraph without hp:t gets one.
	res = applyOps(t, src, true, Op{Op: "set_text", Loc: "s0/p5", Text: str("빈 칸 채움")})
	if got := texts(t, res.Data)["s0/p5"]; got != "빈 칸 채움" {
		t.Errorf("empty paragraph = %q", got)
	}
}

func TestSetTextKeepsMemo(t *testing.T) {
	src := readSample(t)
	for _, track := range []bool{true, false} {
		res := applyOps(t, src, track, Op{Op: "set_text", Loc: "s0/p7", Text: str("바뀐 기대 효과")})
		doc := parse(t, res.Data)
		if len(doc.Summary.Memos) != 1 || doc.Summary.Memos[0].Text != "가짜 메모: 표현을 다듬을 것" {
			t.Errorf("track=%v memos = %+v", track, doc.Summary.Memos)
		}
		if !strings.Contains(paraXML(t, res.Data, "s0/p7"), `type="MEMO"`) {
			t.Errorf("track=%v memo field gone", track)
		}
	}
}

func TestReplace(t *testing.T) {
	src := readSample(t)
	res := applyOps(t, src, true, Op{Op: "replace", Loc: "s0/p7", Find: "프로그램이", With: str("도구가")})
	if got := viewTexts(t, res.Data, model.ViewAccepted)["s0/p7"]; got != "문서 구조를 도구가 읽을 수 있다 (가)." {
		t.Errorf("tracked accepted = %q", got)
	}
	if got := viewTexts(t, res.Data, model.ViewMarkup)["s0/p7"]; got != "문서 구조를 [-프로그램이-][+도구가+] 읽을 수 있다 (가)." {
		t.Errorf("tracked markup = %q", got)
	}
	res = applyOps(t, src, false, Op{Op: "replace", Loc: "s0/p7", Find: "프로그램이", With: str("도구가")})
	if got := texts(t, res.Data)["s0/p7"]; got != "문서 구조를 도구가 읽을 수 있다 (가)." {
		t.Errorf("untracked = %q", got)
	}

	if err := runErr(t, src, true, Op{Op: "replace", Loc: "s0/p7", Find: "없는 말", With: str("x")}); !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing find: %v", err)
	}
	if err := runErr(t, src, true, Op{Op: "replace", Loc: "s0/p2", Find: "다.", With: str("x")}); !strings.Contains(err.Error(), "occurs 2 times") {
		t.Errorf("ambiguous find: %v", err)
	}
	res = applyOps(t, src, false, Op{Op: "replace", Loc: "s0/p2", Find: "다.", With: str("다!"), All: true})
	if got := texts(t, res.Data)["s0/p2"]; got != "이 문서는 why-hwp 시험용으로 만든 가짜 문서다! 실제 내용은 없다!" {
		t.Errorf("all = %q", got)
	}
}

// TestReplaceAcrossRuns splits s0/p2 into two runs with different
// character shapes and replaces text that spans both.
func TestReplaceAcrossRuns(t *testing.T) {
	src := readSample(t)
	raw := part(t, src, "Contents/section0.xml")
	from := `<hp:t>이 문서는 why-hwp 시험용으로 만든 가짜 문서다. 실제 내용은 없다.</hp:t></hp:run>`
	to := `<hp:t>이 문서는 why-hwp 시험용</hp:t></hp:run><hp:run charPrIDRef="6"><hp:t>으로 만든 가짜 문서다. 실제 내용은 없다.</hp:t></hp:run>`
	if !strings.Contains(raw, from) {
		t.Fatal("anchor not found")
	}
	src = withSection(t, src, strings.Replace(raw, from, to, 1))

	res := applyOps(t, src, true, Op{Op: "replace", Loc: "s0/p2", Find: "시험용으로", With: str("예시로")})
	x := paraXML(t, res.Data, "s0/p2")
	for _, want := range []string{
		`<hp:t>이 문서는 why-hwp <hp:deleteBegin Id="2" TcId="2"/>시험용<hp:deleteEnd Id="2" TcId="2" paraend="0"/></hp:t>`,
		`<hp:run charPrIDRef="6"><hp:t><hp:deleteBegin Id="3" TcId="2"/>으로<hp:deleteEnd Id="3" TcId="2" paraend="0"/><hp:insertBegin Id="1" TcId="1"/>예시로<hp:insertEnd Id="1" TcId="1" paraend="0"/> 만든`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("XML:\n%s\nwant part\n%s", x, want)
		}
	}
	res = applyOps(t, src, false, Op{Op: "replace", Loc: "s0/p2", Find: "시험용으로", With: str("예시로")})
	if got := texts(t, res.Data)["s0/p2"]; got != "이 문서는 why-hwp 예시로 만든 가짜 문서다. 실제 내용은 없다." {
		t.Errorf("untracked = %q", got)
	}
}

func TestInsert(t *testing.T) {
	src := readSample(t)
	ops := []Op{
		{Op: "insert_after", Loc: "s0/p1", Paras: []NewPara{{Text: "가"}, {Text: "나", Like: "s0/p7"}}},
		{Op: "insert_before", Loc: "s0/p3", Paras: []NewPara{{Text: "다"}}},
		{Op: "insert_after", Loc: "s0/p2", Paras: []NewPara{{Text: "라"}}},
		{Op: "insert_after", Loc: "s0/p4/t0/r2/c1", Paras: []NewPara{{Text: "셀 둘째 줄"}}},
	}
	for _, track := range []bool{true, false} {
		res := applyOps(t, src, track, ops...)
		got := texts(t, res.Data)
		want := map[string]string{
			"s0/p1": "1. 개요", "s0/p2": "가", "s0/p3": "나", "s0/p4": "이 문서는 why-hwp 시험용으로 만든 가짜 문서다. 실제 내용은 없다.",
			"s0/p5": "라", "s0/p6": "다", "s0/p7": "2. 추진 일정 및 담당",
			"s0/p8/t0/r2/c1/p0": "핵심 기능 구현", "s0/p8/t0/r2/c1/p1": "셀 둘째 줄",
		}
		for loc, w := range want {
			if got[loc] != w {
				t.Errorf("track=%v %s = %q, want %q", track, loc, got[loc], w)
			}
		}
		x := paraXML(t, res.Data, "s0/p2")
		if track && !strings.Contains(x, `paraend="1"`) {
			t.Errorf("inserted paragraph is not marked as a new paragraph: %s", x)
		}
		if !strings.HasPrefix(x, `<hp:p id="0" paraPrIDRef="0" styleIDRef="0"`) {
			t.Errorf("inserted paragraph: %s", x)
		}
	}
	// The original neighbours keep their bytes, line layout included.
	res := applyOps(t, src, true, ops...)
	if paraXML(t, res.Data, "s0/p1") != paraXML(t, src, "s0/p1") {
		t.Error("s0/p1 changed")
	}
}

func TestInsertLike(t *testing.T) {
	src := readSample(t)
	raw := part(t, src, "Contents/section0.xml")
	from := `<hp:p id="0" paraPrIDRef="0" styleIDRef="0" pageBreak="0" columnBreak="0" merged="0"><hp:run charPrIDRef="0"><hp:t>3. 기대 효과</hp:t>`
	to := `<hp:p id="0" paraPrIDRef="5" styleIDRef="2" pageBreak="0" columnBreak="0" merged="0"><hp:run charPrIDRef="5"><hp:t>3. 기대 효과</hp:t>`
	if !strings.Contains(raw, from) {
		t.Fatal("anchor not found")
	}
	src = withSection(t, src, strings.Replace(raw, from, to, 1))
	res := applyOps(t, src, true, Op{Op: "insert_after", Loc: "s0/p2", Paras: []NewPara{{Text: "새 제목", Like: "s0/p6"}}})
	x := paraXML(t, res.Data, "s0/p3")
	if !strings.HasPrefix(x, `<hp:p id="0" paraPrIDRef="5" styleIDRef="2" pageBreak="0" columnBreak="0" merged="0"><hp:run charPrIDRef="5">`) {
		t.Errorf("like shapes not copied: %s", x)
	}
}

// TestInsertCharShape puts paragraphs with mixed character shapes after
// s0/p0 of the sample and checks the shape each new paragraph takes:
// insert_after the last run, insert_before the first run with text or a
// control, like the first run with text.
func TestInsertCharShape(t *testing.T) {
	src := readSample(t)
	raw := part(t, src, "Contents/section0.xml")
	const pOpen = `<hp:p id="0" paraPrIDRef="0" styleIDRef="0" pageBreak="0" columnBreak="0" merged="0">`
	run := func(c, body string) string {
		if body == "-" {
			return `<hp:run charPrIDRef="` + c + `"/>`
		}
		return `<hp:run charPrIDRef="` + c + `">` + body + `</hp:run>`
	}
	text := func(s string) string { return "<hp:t>" + s + "</hp:t>" }
	paras := pOpen + run("5", text("가")) + run("6", text("나")) + "</hp:p>" + // s0/p1
		pOpen + run("5", text("다")) + run("6", "-") + "</hp:p>" + // s0/p2
		pOpen + run("5", text("라")) + run("6", "<hp:t/>") + "</hp:p>" + // s0/p3
		pOpen + run("2", "-") + run("5", text("마")) + run("6", text("바")) + "</hp:p>" + // s0/p4
		pOpen + run("2", "<hp:t/>") + run("5", text("사")) + "</hp:p>" + // s0/p5
		pOpen + run("2", `<hp:ctrl><hp:bookmark name="b"/></hp:ctrl>`) + run("5", text("아")) + "</hp:p>" // s0/p6
	end := strings.Index(raw, "</hp:p>") + len("</hp:p>")
	if strings.Count(raw[:end], "<hp:p ") != 1 {
		t.Fatal("s0/p0 is not a flat paragraph")
	}
	src = withSection(t, src, raw[:end]+paras+raw[end:])
	if got := texts(t, src)["s0/p6"]; got != "아" {
		t.Fatalf("s0/p6 = %q", got)
	}
	for _, track := range []bool{true, false} {
		res := applyOps(t, src, track,
			Op{Op: "insert_after", Loc: "s0/p1", Paras: []NewPara{{Text: "A1"}, {Text: "A2"}}},
			Op{Op: "insert_after", Loc: "s0/p2", Paras: []NewPara{{Text: "B"}}},
			Op{Op: "insert_after", Loc: "s0/p3", Paras: []NewPara{{Text: "C"}, {Text: "L", Like: "s0/p4"}}},
			Op{Op: "insert_before", Loc: "s0/p1", Paras: []NewPara{{Text: "D"}}},
			Op{Op: "insert_before", Loc: "s0/p4", Paras: []NewPara{{Text: "E"}}},
			Op{Op: "insert_before", Loc: "s0/p5", Paras: []NewPara{{Text: "F"}}},
			Op{Op: "insert_before", Loc: "s0/p6", Paras: []NewPara{{Text: "G"}}},
			Op{Op: "image", After: "s0/p2", Src: "figure.png", WidthMM: 10},
		)
		got := texts(t, res.Data)
		want := []struct{ text, charPr string }{
			{"D", "5"}, {"가나", ""}, {"A1", "6"}, {"A2", "6"},
			{"다", ""}, {"B", "6"}, {"", "6"},
			{"라", ""}, {"C", "6"}, {"L", "5"},
			{"E", "5"}, {"마바", ""},
			{"F", "5"}, {"사", ""},
			{"G", "2"}, {"아", ""},
		}
		for i, w := range want {
			loc := "s0/p" + strconv.Itoa(i+1)
			if got[loc] != w.text {
				t.Fatalf("track=%v %s = %q, want %q", track, loc, got[loc], w.text)
			}
			if w.charPr == "" {
				continue
			}
			x := paraXML(t, res.Data, loc)
			if !strings.HasPrefix(x, pOpen+`<hp:run charPrIDRef="`+w.charPr+`">`) {
				t.Errorf("track=%v %s (%q) want charPr %s: %s", track, loc, w.text, w.charPr, x)
			}
		}
	}
}

func TestDelete(t *testing.T) {
	src := readSample(t)
	res := applyOps(t, src, true, Op{Op: "delete", Loc: "s0/p2"})
	x := paraXML(t, res.Data, "s0/p2")
	if !strings.Contains(x, `<hp:run charPrIDRef="0"><hp:t><hp:deleteBegin Id="1" TcId="1"/></hp:t><hp:t>`) ||
		!strings.Contains(x, `<hp:t><hp:deleteEnd Id="1" TcId="1" paraend="1"/></hp:t></hp:run>`) {
		t.Errorf("tracked delete XML: %s", x)
	}
	// The last paragraph of a list keeps its break; the break before it is
	// deleted instead, which is what Hancom Office records.
	res = applyOps(t, src, true, Op{Op: "delete", Loc: "s0/p9"})
	if x := paraXML(t, res.Data, "s0/p9"); !strings.Contains(x, `<hp:deleteEnd Id="1" TcId="1" paraend="0"/>`) {
		t.Errorf("last paragraph delete: %s", x)
	}
	if x := paraXML(t, res.Data, "s0/p8"); !strings.Contains(x, `<hp:t>그림 1. 가짜 도형<hp:deleteBegin Id="2" TcId="1"/><hp:deleteEnd Id="2" TcId="1" paraend="1"/></hp:t>`) {
		t.Errorf("break before the last paragraph: %s", x)
	}

	res = applyOps(t, src, false, Op{Op: "delete", Loc: "s0/p2"})
	got := texts(t, res.Data)
	if got["s0/p2"] != "2. 추진 일정 및 담당" {
		t.Errorf("untracked delete: s0/p2 = %q", got["s0/p2"])
	}

	for _, c := range []struct {
		loc, want string
		track     bool
	}{
		{"s0/p0", "section settings", true},
		{"s0/p4", "table", true},
		{"s0/p7", "memo", false},
		{"s0/p4/t0/r0/c0", "only paragraph", false},
	} {
		if err := runErr(t, src, c.track, Op{Op: "delete", Loc: c.loc}); !strings.Contains(err.Error(), c.want) {
			t.Errorf("delete %s: %v", c.loc, err)
		}
	}
}

func TestImage(t *testing.T) {
	src := readSample(t)
	png, err := os.ReadFile("../../testdata/apply/figure.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, track := range []bool{true, false} {
		res := applyOps(t, src, track,
			Op{Op: "image", After: "s0/p8", Src: "figure.png", WidthMM: 120},
			Op{Op: "image", Loc: "s0/p4/t0/r2/c1", Src: "figure.png", WidthMM: 30},
		)
		doc := parse(t, res.Data)
		var pics []string
		for _, p := range doc.Summary.Pictures {
			pics = append(pics, p.Loc+"="+p.Ref)
		}
		if got, want := strings.Join(pics, " "), "s0/p4/t0/r2/c1/p0=image3 s0/p9=image2 s0/p10=image1"; got != want {
			t.Errorf("track=%v pictures %s, want %s", track, got, want)
		}
		if b := part(t, res.Data, "BinData/image2.png"); b != string(png) {
			t.Errorf("track=%v stored image differs from the source PNG", track)
		}
		hpf := part(t, res.Data, "Contents/content.hpf")
		if !strings.Contains(hpf, `<opf:item id="image2" href="BinData/image2.png" media-type="image/png" isEmbeded="1"/>`) {
			t.Errorf("manifest: %s", hpf)
		}
		x := paraXML(t, res.Data, "s0/p9")
		// 120 mm = 34016 HWPUNIT; 1200x600 px keeps 2:1.
		for _, want := range []string{`<hp:sz width="34016" widthRelTo="ABSOLUTE" height="17008"`, `treatAsChar="1"`, `<hp:imgDim dimwidth="90000" dimheight="45000"/>`, `binaryItemIDRef="image2"`} {
			if !strings.Contains(x, want) {
				t.Errorf("track=%v pic XML lacks %s", track, want)
			}
		}
		if track && !strings.Contains(x, `paraend="1"/></hp:t></hp:run></hp:p>`) {
			t.Errorf("tracked picture paragraph: %s", x)
		}
	}
	if err := runErr(t, src, true, Op{Op: "image", After: "s0/p8", Src: "spec.json", WidthMM: 10}); !strings.Contains(err.Error(), "not a PNG") {
		t.Errorf("non-PNG: %v", err)
	}
}

func TestPackage(t *testing.T) {
	src := readSample(t)
	res := applyOps(t, src, true,
		Op{Op: "set_text", Loc: "s0/p2", Text: str("x")},
		Op{Op: "image", After: "s0/p8", Src: "figure.png", WidthMM: 50},
	)
	in, _ := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	out, _ := zip.NewReader(bytes.NewReader(res.Data), int64(len(res.Data)))
	if out.File[0].Name != "mimetype" || out.File[0].Method != zip.Store {
		t.Errorf("first entry %s method %d", out.File[0].Name, out.File[0].Method)
	}
	if len(out.File) != len(in.File)+1 {
		t.Fatalf("%d entries, want %d", len(out.File), len(in.File)+1)
	}
	for i, f := range in.File {
		g := out.File[i]
		if g.Name != f.Name || g.Method != f.Method {
			t.Errorf("entry %d: %s/%d, want %s/%d", i, g.Name, g.Method, f.Name, f.Method)
		}
	}
	if last := out.File[len(out.File)-1]; last.Name != "BinData/image2.png" || last.Method != zip.Store {
		t.Errorf("new image entry %s method %d", last.Name, last.Method)
	}
	// Untouched paragraphs keep their bytes.
	for _, loc := range []string{"s0/p0", "s0/p1", "s0/p3", "s0/p4", "s0/p7"} {
		if paraXML(t, res.Data, loc) != paraXML(t, src, loc) {
			t.Errorf("%s changed", loc)
		}
	}
	if part(t, res.Data, "settings.xml") != part(t, src, "settings.xml") {
		t.Error("settings.xml changed")
	}
}

func TestHeader(t *testing.T) {
	src := readSample(t)
	res := applyOps(t, src, true,
		Op{Op: "set_text", Loc: "s0/p2", Text: str("x")},
		Op{Op: "delete", Loc: "s0/p3"},
	)
	h := part(t, res.Data, "Contents/header.xml")
	want := `<hh:trackChanges itemCnt="2"><hh:trackChange type="Insert" date="2026-11-11T05:00:00Z" authorID="1" hide="0" id="1"/>` +
		`<hh:trackChange type="Delete" date="2026-11-11T05:00:00Z" authorID="1" hide="0" id="2"/></hh:trackChanges>` +
		`<hh:trackChangeAuthors itemCnt="1"><hh:trackChangeAuthor name="Tester" mark="1" id="1"/></hh:trackChangeAuthors></hh:refList>`
	if !strings.Contains(h, want) {
		t.Errorf("header lacks\n%s", want)
	}
	if n := parse(t, res.Data).Summary.TrackChanges; n != 2 {
		t.Errorf("trackChanges = %d", n)
	}

	// A second run by the same author reuses the author and continues ids.
	res2, err := Apply(res.Data, &Spec{Ops: []Op{{Op: "set_text", Loc: "s0/p1", Text: str("y")}}}, Options{Track: true, Author: "Tester", Date: testDate})
	if err != nil {
		t.Fatal(err)
	}
	h2 := part(t, res2.Data, "Contents/header.xml")
	if res2.AuthorID != "1" || !strings.Contains(h2, `<hh:trackChangeAuthors itemCnt="1">`) || !strings.Contains(h2, `<hh:trackChanges itemCnt="4">`) {
		t.Errorf("second run: author %s\n%s", res2.AuthorID, h2[strings.Index(h2, "<hh:trackChanges"):])
	}
	ids := map[string]int{}
	for _, m := range regexp.MustCompile(`Begin Id="(\d+)"`).FindAllStringSubmatch(part(t, res2.Data, "Contents/section0.xml"), -1) {
		ids[m[1]]++
	}
	for id, n := range ids {
		if n != 1 {
			t.Errorf("marker id %s used by %d ranges", id, n)
		}
	}

	// Another author is added.
	res3, err := Apply(res.Data, &Spec{Ops: []Op{{Op: "set_text", Loc: "s0/p1", Text: str("z")}}}, Options{Track: true, Author: "Other & Co", Date: testDate})
	if err != nil {
		t.Fatal(err)
	}
	h3 := part(t, res3.Data, "Contents/header.xml")
	if res3.AuthorID != "2" || !strings.Contains(h3, `<hh:trackChangeAuthors itemCnt="2"><hh:trackChangeAuthor name="Tester" mark="1" id="1"/><hh:trackChangeAuthor name="Other &amp; Co" mark="1" id="2"/>`) {
		t.Errorf("new author: %s", res3.AuthorID)
	}

	// Without tracking the header is untouched.
	res4 := applyOps(t, src, false, Op{Op: "set_text", Loc: "s0/p2", Text: str("x")})
	if part(t, res4.Data, "Contents/header.xml") != part(t, src, "Contents/header.xml") {
		t.Error("untracked run changed header.xml")
	}
}

func TestLocsAreOriginal(t *testing.T) {
	src := readSample(t)
	res := applyOps(t, src, true,
		Op{Op: "insert_after", Loc: "s0/p0", Paras: []NewPara{{Text: "a"}, {Text: "b"}}},
		Op{Op: "set_text", Loc: "s0/p1", Text: str("개요")},
		Op{Op: "delete", Loc: "s0/p2"},
	)
	got := viewTexts(t, res.Data, model.ViewOriginal)
	if got["s0/p3"] != "1. 개요" || !strings.HasPrefix(got["s0/p4"], "이 문서는") {
		t.Errorf("original p3 %q p4 %q", got["s0/p3"], got["s0/p4"])
	}
	got = viewTexts(t, res.Data, model.ViewAccepted)
	if _, ok := got["s0/p4"]; got["s0/p1"] != "a" || got["s0/p3"] != "개요" || ok {
		t.Errorf("accepted p1 %q p3 %q p4 %q (present %v)", got["s0/p1"], got["s0/p3"], got["s0/p4"], ok)
	}
}

func TestErrors(t *testing.T) {
	src := readSample(t)
	for _, c := range []struct {
		op   Op
		want string
	}{
		{Op{Op: "set_text", Loc: "s0/p99", Text: str("x")}, "not found"},
		{Op{Op: "set_text", Loc: "p3", Text: str("x")}, "bad loc"},
		{Op{Op: "set_text", Loc: "s1/p0", Text: str("x")}, "no section 1"},
		{Op{Op: "insert_before", Loc: "s0/p0", Paras: []NewPara{{Text: "x"}}}, "section settings"},
		{Op{Op: "insert_after", Loc: "s0/p1", Paras: []NewPara{{Text: "x", Like: "s0/p50"}}}, "like"},
	} {
		if err := runErr(t, src, true, c.op); !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.op, err)
		}
	}
	if err := runErr(t, src, true,
		Op{Op: "set_text", Loc: "s0/p2", Text: str("x")},
		Op{Op: "replace", Loc: "s0/p2", Find: "문서", With: str("y")},
	); !strings.Contains(err.Error(), "already changed") {
		t.Errorf("conflict: %v", err)
	}
	if _, err := Apply(src, &Spec{Ops: []Op{{Op: "delete", Loc: "s0/p2"}}}, Options{Track: true}); err == nil || !strings.Contains(err.Error(), "author") {
		t.Errorf("no author: %v", err)
	}
}

func TestParseSpec(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "testdata", "apply", "spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := ParseSpec(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Ops) != 8 {
		t.Errorf("%d ops", len(s.Ops))
	}
	for _, c := range []struct{ in, want string }{
		{`{"ops":[]}`, "no ops"},
		{`{"ops":[{"op":"set_text","loc":"s0/p1"}]}`, "text is required"},
		{`{"ops":[{"op":"set_txt","loc":"s0/p1"}]}`, "unknown op"},
		{`{"ops":[{"op":"set_text","loc":"s0/p1","txt":"x"}]}`, "unknown field"},
		{`{"ops":[{"op":"image","after":"s0/p1","loc":"s0/p2","src":"a.png","width_mm":1}]}`, "exactly one"},
		{`{"ops":[{"op":"image","after":"s0/p1","src":"a.png"}]}`, "width_mm"},
		{`{"ops":[{"op":"set_text","loc":"s0/p1","text":"a\u0001"}]}`, "control character"},
	} {
		_, err := ParseSpec(strings.NewReader(c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.in, err)
		}
	}
}

// TestSpecExample applies the documented example spec to the sample.
func TestSpecExample(t *testing.T) {
	f, err := os.Open("../../testdata/apply/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := ParseSpec(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(readSample(t), spec, Options{Track: false, BaseDir: "../../testdata/apply"})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, b := range parse(t, res.Data).Blocks {
		lines = append(lines, b.Loc+"\t"+b.Text)
	}
	want := []string{
		"s0/p0\t샘플 사업 계획서",
		"s0/p1\t1. 개요",
		"s0/p2\t1.1 목적",
		"s0/p3\t양식의 서식을 유지한 채 글을 채운다.",
		"s0/p4\t이 문서는 apply 명령 시험용 가짜 문서다.",
		"s0/p5\t2. 추진 일정 및 담당",
		"s0/p6\t",
		"s0/p6/t0/r0/c0\t항목",
		"s0/p6/t0/r0/c1\t내용",
		"s0/p6/t0/r0/c2\t담당",
		"s0/p6/t0/r1/c0\t요구사항 정리",
		"s0/p6/t0/r1/c1\t범위와 일정 확정",
		"s0/p6/t0/r1/c2\t(B)",
		"s0/p6/t0/r2/c0\t시제품 개발",
		"s0/p6/t0/r2/c1\t핵심 기능 구현",
		"s0/p6/t0/r2/c2\t(A/B)",
		"s0/p7\t표 1. 가짜 일정",
		"s0/p8\t3. 기대 효과",
		"s0/p9\t문서 구조를 도구가 읽을 수 있다 (가).",
		"s0/p10\t그림 1. 가짜 도형",
		"s0/p11\t",
		"s0/p12\t",
		"s0/p13\t그림 2. apply 로 넣은 그림",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("blocks:\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// TestInsertAfterLast checks the form Hancom Office records when text is
// added after the last paragraph of a list: the break of that paragraph
// is the inserted one and the new last paragraph keeps the list's break.
func TestInsertAfterLast(t *testing.T) {
	src := readSample(t)
	res := applyOps(t, src, true,
		Op{Op: "insert_after", Loc: "s0/p9", Paras: []NewPara{{Text: "가"}, {Text: "나"}}},
		Op{Op: "insert_after", Loc: "s0/p4/t0/r0/c0", Paras: []NewPara{{Text: "셀"}}},
	)
	x := paraXML(t, res.Data, "s0/p9")
	if !strings.HasSuffix(x, `<hp:t><hp:insertBegin Id="5" TcId="1"/><hp:insertEnd Id="5" TcId="1" paraend="1"/></hp:t></hp:run></hp:p>`) {
		t.Errorf("s0/p9: %s", x[strings.LastIndex(x, "</hp:pic>"):])
	}
	if x := paraXML(t, res.Data, "s0/p10"); !strings.Contains(x, `가<hp:insertEnd Id="1" TcId="1" paraend="1"/>`) {
		t.Errorf("s0/p10: %s", x)
	}
	if x := paraXML(t, res.Data, "s0/p11"); !strings.Contains(x, `나<hp:insertEnd Id="2" TcId="1" paraend="0"/>`) {
		t.Errorf("s0/p11: %s", x)
	}
	if x := paraXML(t, res.Data, "s0/p4/t0/r0/c0/p0"); !strings.Contains(x, `항목<hp:insertBegin Id="4" TcId="1"/><hp:insertEnd Id="4" TcId="1" paraend="1"/>`) {
		t.Errorf("cell p0: %s", x)
	}
	if x := paraXML(t, res.Data, "s0/p4/t0/r0/c0/p1"); !strings.Contains(x, `셀<hp:insertEnd Id="3" TcId="1" paraend="0"/>`) {
		t.Errorf("cell p1: %s", x)
	}
	got := texts(t, res.Data)
	if got["s0/p4/t0/r0/c0/p0"] != "항목" || got["s0/p4/t0/r0/c0/p1"] != "셀" {
		t.Errorf("cell = %q, %q", got["s0/p4/t0/r0/c0/p0"], got["s0/p4/t0/r0/c0/p1"])
	}
}

// boxDoc adds two paragraphs to the end of the sample: s0/p10 with a text
// box, own text and a 1x2 table whose first cell has three paragraphs
// (the first one empty) and whose second has one, and s0/p11 whose only
// text is in a text box.
func boxDoc(t *testing.T) []byte {
	t.Helper()
	src := readSample(t)
	raw := part(t, src, "Contents/section0.xml")
	const pOpen = `<hp:p id="0" paraPrIDRef="0" styleIDRef="0" pageBreak="0" columnBreak="0" merged="0">`
	p := func(body string) string {
		return pOpen + `<hp:run charPrIDRef="0">` + body + `</hp:run></hp:p>`
	}
	text := func(s string) string { return `<hp:t>` + s + `</hp:t>` }
	box := func(paras ...string) string {
		return `<hp:rect id="900"><hp:drawText><hp:subList>` + strings.Join(paras, "") + `</hp:subList></hp:drawText></hp:rect>`
	}
	cell := func(col int, paras ...string) string {
		return `<hp:tc><hp:subList>` + strings.Join(paras, "") + `</hp:subList><hp:cellAddr colAddr="` + strconv.Itoa(col) + `" rowAddr="0"/></hp:tc>`
	}
	added := p(box(p(text("상자 첫째")), p(text("상자 둘째")))+
		`<hp:tbl id="901" rowCnt="1" colCnt="2"><hp:tr>`+
		cell(0, pOpen+`<hp:run charPrIDRef="0"/></hp:p>`, p(text("둘째 문단 글")), p(text("셋째 문단 글")+box(p(text("칸 안 상자")))))+
		cell(1, p(text("홑문단 칸")+box(p(text("홑칸 상자")))))+
		`</hp:tr></hp:tbl>`+text("닻 문단 글")) +
		p(box(p(text("상자만 있는 글")))+`<hp:t/>`)
	i := strings.LastIndex(raw, "</hs:sec>")
	if i < 0 {
		t.Fatal("no section end tag")
	}
	return withSection(t, src, raw[:i]+added+raw[i:])
}

// What the reader lists for boxDoc after the sample's own blocks.
var boxDocBlocks = []string{
	"para s0/p10 닻 문단 글",
	"cell s0/p10/t0/r0/c0/p0 ",
	"cell s0/p10/t0/r0/c0/p1 둘째 문단 글",
	"cell s0/p10/t0/r0/c0/p2 셋째 문단 글",
	"box s0/p10/t0/r0/c0/p2#box 칸 안 상자",
	"cell s0/p10/t0/r0/c1 홑문단 칸",
	"box s0/p10/t0/r0/c1#box 홑칸 상자",
	"box s0/p10#box 상자 첫째\n상자 둘째",
	"para s0/p11 ",
	"box s0/p11#box 상자만 있는 글",
}

func TestBoxDocBlocks(t *testing.T) {
	var got []string
	for _, b := range parse(t, boxDoc(t)).Blocks {
		got = append(got, string(b.Kind)+" "+b.Loc+" "+b.Text)
	}
	n := len(parse(t, readSample(t)).Blocks)
	if len(got) != n+len(boxDocBlocks) || strings.Join(got[n:], "|") != strings.Join(boxDocBlocks, "|") {
		t.Errorf("blocks:\n got %q\nwant %q", got[min(n, len(got)):], boxDocBlocks)
	}
}

// A cell with several paragraphs is listed by paragraph, and each of
// those locations edits its own paragraph. The bare cell loc still names
// the first paragraph and says so when the text is in another one.
func TestCellParagraphLocs(t *testing.T) {
	src := boxDoc(t)
	const cell = "s0/p10/t0/r0/c0"
	err := runErr(t, src, false, Op{Op: "replace", Loc: cell, Find: "둘째", With: str("고친")})
	if !strings.Contains(err.Error(), `"둘째" not found in ""`) || !strings.Contains(err.Error(), "address the others as "+cell+"/pN") {
		t.Errorf("bare cell loc: %v", err)
	}
	for _, track := range []bool{false, true} {
		res := applyOps(t, src, track,
			Op{Op: "replace", Loc: cell + "/p1", Find: "둘째", With: str("고친")},
			Op{Op: "set_text", Loc: cell + "/p0", Text: str("채운 글")},
		)
		got := viewTexts(t, res.Data, model.ViewAccepted)
		want := map[string]string{
			cell + "/p0": "채운 글", cell + "/p1": "고친 문단 글", cell + "/p2": "셋째 문단 글",
			cell + "/p2#box": "칸 안 상자", "s0/p10/t0/r0/c1": "홑문단 칸",
		}
		for loc, w := range want {
			if got[loc] != w {
				t.Errorf("track=%v %s = %q, want %q", track, loc, got[loc], w)
			}
		}
		if _, ok := got[cell]; ok {
			t.Errorf("track=%v: cell with three paragraphs is also listed as one block", track)
		}
	}
}

// Text-box text is listed apart from its anchor paragraph and no op takes
// its location; the anchor's own text stays editable and the box is kept.
func TestTextBoxReadOnly(t *testing.T) {
	src := boxDoc(t)
	for _, op := range []Op{
		{Op: "replace", Loc: "s0/p11#box", Find: "상자만", With: str("x")},
		{Op: "set_text", Loc: "s0/p10#box", Text: str("x")},
		{Op: "insert_after", Loc: "s0/p10/t0/r0/c1#box", Paras: []NewPara{{Text: "x"}}},
		{Op: "insert_before", Loc: "s0/p10/t0/r0/c0/p2#box", Paras: []NewPara{{Text: "x"}}},
		{Op: "delete", Loc: "s0/p11#box"},
		{Op: "image", Loc: "s0/p11#box", Src: "figure.png", WidthMM: 10},
		{Op: "insert_after", Loc: "s0/p1", Paras: []NewPara{{Text: "x", Like: "s0/p11#box"}}},
	} {
		if err := runErr(t, src, false, op); !strings.Contains(err.Error(), "is text-box text, which apply cannot edit") {
			t.Errorf("%+v: %v", op, err)
		}
	}
	// The box text is not part of the anchor paragraph.
	if err := runErr(t, src, false, Op{Op: "replace", Loc: "s0/p11", Find: "상자만", With: str("x")}); !strings.Contains(err.Error(), `not found in ""`) {
		t.Errorf("box text through its anchor: %v", err)
	}
	for _, track := range []bool{false, true} {
		res := applyOps(t, src, track,
			Op{Op: "set_text", Loc: "s0/p11", Text: str("새 글")},
			Op{Op: "replace", Loc: "s0/p10", Find: "닻", With: str("바뀐")},
			Op{Op: "replace", Loc: "s0/p10/t0/r0/c1", Find: "홑문단", With: str("고친")},
		)
		got := viewTexts(t, res.Data, model.ViewAccepted)
		want := map[string]string{
			"s0/p11": "새 글", "s0/p11#box": "상자만 있는 글",
			"s0/p10": "바뀐 문단 글", "s0/p10#box": "상자 첫째\n상자 둘째",
			"s0/p10/t0/r0/c1": "고친 칸", "s0/p10/t0/r0/c1#box": "홑칸 상자",
		}
		for loc, w := range want {
			if got[loc] != w {
				t.Errorf("track=%v %s = %q, want %q", track, loc, got[loc], w)
			}
		}
	}
}

// Every location the reader lists is one apply takes: an edit at it
// changes that block and no other. Text-box blocks are the exception and
// are refused.
func TestReaderLocsAreApplyLocs(t *testing.T) {
	src := boxDoc(t)
	blocks := parse(t, src).Blocks
	edited, refused := 0, 0
	for _, track := range []bool{false, true} {
		for _, b := range blocks {
			op := Op{Op: "set_text", Loc: b.Loc, Text: str("채움")}
			want := "채움"
			if f := strings.Fields(b.Text); len(f) > 0 {
				op = Op{Op: "replace", Loc: b.Loc, Find: f[0], With: str("바뀜"), All: true}
				want = strings.ReplaceAll(b.Text, f[0], "바뀜")
			}
			if b.Kind == model.KindBox {
				if err := runErr(t, src, track, op); !strings.Contains(err.Error(), "text-box text") {
					t.Errorf("track=%v %s: %v", track, b.Loc, err)
				}
				refused++
				continue
			}
			res, err := Apply(src, &Spec{Ops: []Op{op}}, Options{Track: track, Author: "Tester", Date: testDate})
			if err != nil {
				t.Errorf("track=%v %s: %v", track, b.Loc, err)
				continue
			}
			edited++
			after := parse(t, res.Data).InView(model.ViewAccepted).Blocks
			if len(after) != len(blocks) {
				t.Errorf("track=%v %s: %d blocks, want %d", track, b.Loc, len(after), len(blocks))
				continue
			}
			for i, a := range after {
				w := blocks[i].Text
				if blocks[i].Loc == b.Loc {
					w = want
				}
				if a.Loc != blocks[i].Loc || a.Text != w {
					t.Errorf("track=%v edit at %s: %s = %q, want %s = %q", track, b.Loc, a.Loc, a.Text, blocks[i].Loc, w)
				}
			}
		}
	}
	if refused != 2*4 || edited != 2*(len(blocks)-4) {
		t.Errorf("edited %d, refused %d of %d blocks", edited, refused, len(blocks))
	}
}
