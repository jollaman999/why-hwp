package hwp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/jollaman999/why-hwp/internal/hwpx"
	"github.com/jollaman999/why-hwp/internal/model"
)

const (
	sampleHWP  = "../../testdata/sample.hwp"
	sampleHWPX = "../../testdata/sample.hwpx"
)

// The two samples are the same document saved by Hwp in both formats, so
// everything except the binary item naming must agree.
func TestSampleMatchesHWPX(t *testing.T) {
	got, err := Read(sampleHWP)
	if err != nil {
		t.Fatal(err)
	}
	want, err := hwpx.Read(sampleHWPX)
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != "hwp" || got.Summary.Format != "hwp" {
		t.Errorf("format = %q / %q", got.Format, got.Summary.Format)
	}
	if !reflect.DeepEqual(got.Blocks, want.Blocks) {
		t.Errorf("blocks differ\n got %+v\nwant %+v", got.Blocks, want.Blocks)
	}

	g, w := got.Summary, want.Summary
	for _, c := range []struct {
		name      string
		got, want any
	}{
		{"sections", g.Sections, w.Sections},
		{"paragraphs", g.Paragraphs, w.Paragraphs},
		{"tables", g.Tables, w.Tables},
		{"memos", g.Memos, w.Memos},
		{"owner marks", g.OwnerMarks, w.OwnerMarks},
		{"track changes", g.TrackChanges, w.TrackChanges},
		{"para shapes", g.ParaShapes, w.ParaShapes},
		{"char shapes", g.CharShapes, w.CharShapes},
		{"styles", g.Styles, w.Styles},
		{"pictures", len(g.Pictures), len(w.Pictures)},
		{"bin data", len(g.BinData), len(w.BinData)},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %+v, want %+v", c.name, c.got, c.want)
		}
	}
	if len(g.Pictures) == 1 && len(g.BinData) == 1 {
		p, b := g.Pictures[0], g.BinData[0]
		if p.Loc != w.Pictures[0].Loc || p.Ref != b.ID {
			t.Errorf("picture = %+v, bin data = %+v", p, b)
		}
		if b.Path != "BinData/BIN0001.PNG" || b.MediaType != "image/png" || b.Size != w.BinData[0].Size {
			t.Errorf("bin data = %+v, want size %d", b, w.BinData[0].Size)
		}
	}
}

// patchHeader returns the sample with FileHeader bytes changed in place.
// The FileHeader stream is stored contiguously, so the file stays a valid
// compound file.
func patchHeader(t *testing.T, f func(fh []byte)) []byte {
	t.Helper()
	b, err := os.ReadFile(sampleHWP)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(b, []byte("HWP Document File"))
	if i < 0 {
		t.Fatal("signature not found in sample")
	}
	f(b[i : i+256])
	return b
}

func TestRejects(t *testing.T) {
	setProp := func(bit uint32) func([]byte) {
		return func(fh []byte) {
			p := binary.LittleEndian.Uint32(fh[36:])
			binary.LittleEndian.PutUint32(fh[36:], p|bit)
		}
	}
	sample, err := os.ReadFile(sampleHWP)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"password", patchHeader(t, setProp(propPassword)), ErrEncrypted},
		{"drm", patchHeader(t, setProp(propDRM)), ErrEncrypted},
		{"distribution", patchHeader(t, setProp(propDistribution)), ErrDistribution},
		{"bad signature", patchHeader(t, func(fh []byte) { copy(fh, "XWP") }), ErrNotHWP},
		{"version 4", patchHeader(t, func(fh []byte) { fh[35] = 4 }), ErrNotHWP},
		{"hwp 3", append([]byte("HWP Document File V3.00 \x1a\x01\x02\x03\x04\x05"), make([]byte, 512)...), ErrNotHWP},
		{"not cfb", bytes.Repeat([]byte("not a document "), 64), ErrNotHWP},
		{"zip", append([]byte("PK\x03\x04"), make([]byte, 512)...), ErrNotHWP},
		{"empty", nil, ErrNotHWP},
	} {
		_, err := Parse(bytes.NewReader(tc.data))
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	// A truncated file must fail with an error, not panic or succeed.
	if _, err := Parse(bytes.NewReader(sample[:len(sample)/2])); err == nil {
		t.Error("truncated: no error")
	}
}

func TestReadRecords(t *testing.T) {
	hdr := func(tag, level, size int) []byte {
		return binary.LittleEndian.AppendUint32(nil, uint32(tag|level<<10|size<<20))
	}
	var b []byte
	b = append(b, hdr(tagParaHeader, 0, 2)...)
	b = append(b, 1, 2)
	b = append(b, hdr(tagParaText, 1, 0xfff)...)
	b = binary.LittleEndian.AppendUint32(b, 5000)
	b = append(b, make([]byte, 5000)...)
	recs, err := readRecords(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].tag != tagParaHeader || len(recs[0].data) != 2 ||
		recs[1].tag != tagParaText || recs[1].level != 1 || len(recs[1].data) != 5000 {
		t.Fatalf("records = %+v", recs)
	}
	roots := buildTree(recs)
	if len(roots) != 1 || len(roots[0].kids) != 1 {
		t.Errorf("tree: %d roots", len(roots))
	}

	for _, bad := range [][]byte{
		{1, 2},
		hdr(tagParaText, 0, 10),
		hdr(tagParaText, 0, 0xfff),
	} {
		if _, err := readRecords(bad); err == nil {
			t.Errorf("readRecords(% x): no error", bad)
		}
	}
}

func TestParaText(t *testing.T) {
	u := []uint16{'A', 9, 0, 0, 0, 0, 0, 0, 9, 'B', 10, 'C', 24, 30, 'D'}
	u = append(u, 3, 0x6d65, 0x2525, 0, 0, 0, 0, 3) // memo field start
	u = append(u, 'E', 4, 0, 0, 0, 0, 0, 0, 4, 0xd83d, 0xde00, 13)
	var data []byte
	for _, c := range u {
		data = binary.LittleEndian.AppendUint16(data, c)
	}
	field := []byte{0x6b, 0x6e, 0x75, 0x25}
	field = append(field, make([]byte, 5)...)
	field = append(field, 0, 0)       // empty command
	field = append(field, 0, 0, 0, 0) // id
	field = append(field, 7, 0, 0, 0) // memo number
	para := &node{record: record{tag: tagParaHeader}, kids: []*node{
		{record: record{tag: tagParaText, level: 1, data: data}},
		{record: record{tag: tagCtrlHeader, level: 1, data: field}},
	}}
	w := &walker{doc: &model.Document{}}
	if got, box := w.para(para, "s0/p0"); got != "A\tB\nC- DE\U0001F600" || box != "" {
		t.Errorf("para = %q, box = %q", got, box)
	}
	if len(w.memoAnchors) != 1 || w.memoAnchors[0].num != 7 || w.doc.Summary.Memos[0].Loc != "s0/p0" {
		t.Errorf("memo anchors = %+v, memos = %+v", w.memoAnchors, w.doc.Summary.Memos)
	}
	if got := plainText(para); got != "A\tB\nC- DE\U0001F600" {
		t.Errorf("plainText = %q", got)
	}
}

// textPara builds a paragraph record tree from UTF-16 units and the
// control records its extended characters point at.
func textPara(u []uint16, ctrls ...*node) *node {
	var data []byte
	for _, c := range u {
		data = binary.LittleEndian.AppendUint16(data, c)
	}
	kids := []*node{{record: record{tag: tagParaText, data: data}}}
	return &node{record: record{tag: tagParaHeader}, kids: append(kids, ctrls...)}
}

func units(s string) []uint16 {
	var u []uint16
	for _, r := range s {
		u = append(u, uint16(r))
	}
	return append(u, 13)
}

// A cell with one paragraph is one block; a cell with more is one block
// per paragraph. Text-box text is a block of its own after its anchor.
func TestCellParagraphsAndTextBox(t *testing.T) {
	gso := binary.LittleEndian.AppendUint32(nil, ctrlShape)
	box := &node{record: record{tag: tagCtrlHeader, data: gso}, kids: []*node{
		{record: record{tag: tagShapeComp}, kids: []*node{
			{record: record{tag: tagListHeader}},
			textPara(units("box one")),
			textPara(units("box two")),
		}},
	}}
	anchor := append([]uint16{'A', 11, 0, 0, 0, 0, 0, 0, 11}, units("B")...)
	addr := func(row, col uint16) *node {
		d := make([]byte, 12)
		binary.LittleEndian.PutUint16(d[8:], col)
		binary.LittleEndian.PutUint16(d[10:], row)
		return &node{record: record{tag: tagListHeader, data: d}}
	}
	w := &walker{doc: &model.Document{}}
	w.section(0, []*node{textPara(anchor, box)})
	w.cell(addr(0, 0), []*node{textPara(units("")), textPara(units("second")), textPara(anchor, box)}, "s0/p1/t0")
	w.cell(addr(0, 1), []*node{textPara(anchor, box)}, "s0/p1/t0")

	var got []string
	for _, b := range w.doc.Blocks {
		got = append(got, string(b.Kind)+" "+b.Loc+" "+b.Text)
	}
	want := []string{
		"para s0/p0 AB",
		"box s0/p0#box box one\nbox two",
		"cell s0/p1/t0/r0/c0/p0 ",
		"cell s0/p1/t0/r0/c0/p1 second",
		"cell s0/p1/t0/r0/c0/p2 AB",
		"box s0/p1/t0/r0/c0/p2#box box one\nbox two",
		"cell s0/p1/t0/r0/c1 AB",
		"box s0/p1/t0/r0/c1#box box one\nbox two",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("blocks:\n got %q\nwant %q", got, want)
	}
}
