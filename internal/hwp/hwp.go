// Package hwp reads HWP 5.0 binary documents into the model package's
// block list and structure summary.
//
// An HWP 5.0 file is an OLE compound file (CFB). The FileHeader stream
// holds the signature, version and property bits, DocInfo holds the shape,
// style and binary-item tables, and BodyText/SectionN holds the body. When
// the compressed bit is set, DocInfo and the sections are raw deflate
// streams. Both are sequences of tagged records whose level field nests
// them into a tree.
//
// Record layouts, tag values and control character classes follow the
// public specification "한글 문서 파일 형식 5.0" (revision 1.3); section
// and table numbers in comments refer to it.
package hwp

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/jollaman999/why-hwp/internal/model"
	"github.com/richardlehane/mscfb"
)

var (
	// ErrNotHWP is returned for files that are not HWP 5.0 documents.
	ErrNotHWP = errors.New("not an HWP 5.0 document")
	// ErrEncrypted is returned for password-protected and DRM documents.
	ErrEncrypted = errors.New("document is encrypted (password or DRM); decryption is not supported")
	// ErrDistribution is returned for distribution (read-only) documents,
	// whose body is encrypted.
	ErrDistribution = errors.New("document is a distribution document whose body is encrypted; it is not supported")
)

// FileHeader property bits (3.2.1).
const (
	propCompressed   = 1 << 0
	propPassword     = 1 << 1
	propDistribution = 1 << 2
	propDRM          = 1 << 4
	propCertEncrypt  = 1 << 8
	propCertDRM      = 1 << 10
)

// Record tags (4.1, tables 13 and 57): HWPTAG_BEGIN is 0x010.
const (
	tagBegin        = 0x010
	tagIDMappings   = tagBegin + 1
	tagBinData      = tagBegin + 2
	tagCharShape    = tagBegin + 5
	tagParaShape    = tagBegin + 9
	tagStyle        = tagBegin + 10
	tagTrackChange  = tagBegin + 80
	tagParaHeader   = tagBegin + 50
	tagParaText     = tagBegin + 51
	tagParaRangeTag = tagBegin + 54
	tagCtrlHeader   = tagBegin + 55
	tagListHeader   = tagBegin + 56
	tagShapeComp    = tagBegin + 60
	tagTable        = tagBegin + 61
	tagShapePicture = tagBegin + 69
	tagMemoList     = tagBegin + 77
)

// Range tag kinds of tracked insertions and deletions (DocInfo
// TRACK_CHANGE kinds; not in the 1.3 specification).
const (
	rangeInsert = 0x10
	rangeDelete = 0x11
)

// idMapTrackChange is the index of the track-change count in
// HWPTAG_ID_MAPPINGS (table 16, file version 5.0.3 revision 2 and later).
const idMapTrackChange = 16

// Control ids are MAKE_4CHID(a, b, c, d) = a<<24 | b<<16 | c<<8 | d (4.3.9).
func ctrlID(s string) uint32 {
	return uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3])
}

var (
	ctrlTable = ctrlID("tbl ")
	ctrlShape = ctrlID("gso ")
	fieldMemo = ctrlID("%%me") // table 128
)

// Read opens the HWP file at name and parses it.
func Read(name string) (*model.Document, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse parses an HWP 5.0 document.
func Parse(ra io.ReaderAt) (*model.Document, error) {
	head := make([]byte, 32)
	n, _ := ra.ReadAt(head, 0)
	if bytes.HasPrefix(head[:n], []byte("HWP Document File V")) {
		return nil, fmt.Errorf("%w: HWP 3.x files are not supported", ErrNotHWP)
	}
	if bytes.HasPrefix(head[:n], []byte("PK\x03\x04")) {
		return nil, fmt.Errorf("%w: the file is a zip package; an HWPX file needs the .hwpx extension", ErrNotHWP)
	}
	cf, err := mscfb.New(ra)
	if err != nil {
		return nil, fmt.Errorf("%w: not an OLE compound file (%v)", ErrNotHWP, err)
	}
	streams := map[string][]byte{}
	for e, err := cf.Next(); err == nil; e, err = cf.Next() {
		if e.FileInfo().IsDir() {
			continue
		}
		b, err := io.ReadAll(e)
		if err != nil {
			return nil, fmt.Errorf("damaged file: read stream %s: %w", e.Name, err)
		}
		streams[strings.Join(append(append([]string{}, e.Path...), e.Name), "/")] = b
	}

	fh, ok := streams["FileHeader"]
	if !ok || len(fh) < 40 || !bytes.HasPrefix(fh, []byte("HWP Document File")) {
		return nil, fmt.Errorf("%w: missing or bad FileHeader signature", ErrNotHWP)
	}
	version := binary.LittleEndian.Uint32(fh[32:])
	if version>>24 != 5 {
		return nil, fmt.Errorf("%w: file version %d.%d.%d.%d", ErrNotHWP,
			version>>24, version>>16&0xff, version>>8&0xff, version&0xff)
	}
	props := binary.LittleEndian.Uint32(fh[36:])
	if props&(propPassword|propDRM|propCertEncrypt|propCertDRM) != 0 {
		return nil, ErrEncrypted
	}
	if props&propDistribution != 0 {
		return nil, ErrDistribution
	}
	compressed := props&propCompressed != 0

	doc := &model.Document{Format: "hwp"}
	sum := &doc.Summary
	sum.Format = "hwp"

	info, err := stream(streams, "DocInfo", compressed)
	if err != nil {
		return nil, err
	}
	recs, err := readRecords(info)
	if err != nil {
		return nil, fmt.Errorf("DocInfo: %w", err)
	}
	if err := readDocInfo(recs, streams, compressed, sum); err != nil {
		return nil, fmt.Errorf("DocInfo: %w", err)
	}

	sections := sectionNames(streams)
	if len(sections) == 0 {
		return nil, fmt.Errorf("%w: no BodyText/Section streams", ErrNotHWP)
	}
	sum.Sections = len(sections)
	w := &walker{doc: doc}
	for i, name := range sections {
		data, err := sectionStream(streams, name, compressed)
		if err != nil {
			return nil, err
		}
		recs, err := readRecords(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		w.section(i, buildTree(recs))
	}
	w.pairMemos()

	for _, b := range doc.Blocks {
		if b.Kind == model.KindPara {
			sum.Paragraphs++
		}
	}
	sum.OwnerMarks = model.FindOwnerMarks(doc.Blocks)
	return doc, nil
}

// stream returns the contents of the named stream, inflated when the
// document is compressed.
func stream(streams map[string][]byte, name string, compressed bool) ([]byte, error) {
	b, ok := streams[name]
	if !ok {
		return nil, fmt.Errorf("%w: missing stream %s", ErrNotHWP, name)
	}
	if !compressed {
		return b, nil
	}
	out, err := io.ReadAll(flate.NewReader(bytes.NewReader(b)))
	if err != nil {
		return nil, fmt.Errorf("%s: inflate: %w", name, err)
	}
	return out, nil
}

// sectionStream returns the records of the BodyText section name. A
// document with tracked changes also keeps each section, change history
// included, in ViewText/SectionN, while BodyText has it as shown. HWPX
// has the history, so the ViewText copy is read when it holds paragraph
// records. rhwp, which converts HWP to HWPX, reads it the same way.
func sectionStream(streams map[string][]byte, name string, compressed bool) ([]byte, error) {
	view := "ViewText/" + strings.TrimPrefix(name, "BodyText/")
	if _, ok := streams[view]; ok {
		if b, err := stream(streams, view, compressed); err == nil && len(b) >= 4 &&
			int(binary.LittleEndian.Uint32(b)&0x3ff) == tagParaHeader {
			return b, nil
		}
	}
	return stream(streams, name, compressed)
}

// sectionNames lists BodyText/SectionN streams in section order.
func sectionNames(streams map[string][]byte) []string {
	var names []string
	for name := range streams {
		if _, ok := sectionNum(name); ok {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, _ := sectionNum(names[i])
		b, _ := sectionNum(names[j])
		return a < b
	})
	return names
}

func sectionNum(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, "BodyText/Section")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

// record is one data record. The header is 32 bits: tag 10, level 10,
// size 12; a size of 0xFFF means a DWORD size follows (4.1, figure 46).
type record struct {
	tag   int
	level int
	data  []byte
}

func readRecords(b []byte) ([]record, error) {
	var recs []record
	off := 0
	for off < len(b) {
		if len(b)-off < 4 {
			return nil, fmt.Errorf("truncated record header at offset %d", off)
		}
		h := binary.LittleEndian.Uint32(b[off:])
		off += 4
		size := int(h >> 20)
		if size == 0xfff {
			if len(b)-off < 4 {
				return nil, fmt.Errorf("truncated record size at offset %d", off)
			}
			size = int(binary.LittleEndian.Uint32(b[off:]))
			off += 4
		}
		if size < 0 || size > len(b)-off {
			return nil, fmt.Errorf("record at offset %d: size %d runs past the stream", off, size)
		}
		recs = append(recs, record{tag: int(h & 0x3ff), level: int(h >> 10 & 0x3ff), data: b[off : off+size]})
		off += size
	}
	return recs, nil
}

// node is a record with the records nested under it by level.
type node struct {
	record
	kids []*node
}

func buildTree(recs []record) []*node {
	var roots, stack []*node
	for _, r := range recs {
		n := &node{record: r}
		for len(stack) > 0 && stack[len(stack)-1].level >= r.level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, n)
		} else {
			p := stack[len(stack)-1]
			p.kids = append(p.kids, n)
		}
		stack = append(stack, n)
	}
	return roots
}

func readDocInfo(recs []record, streams map[string][]byte, compressed bool, sum *model.Summary) error {
	trackChanges := 0
	for _, r := range recs {
		switch r.tag {
		case tagIDMappings:
			if len(r.data) >= 4*(idMapTrackChange+1) {
				trackChanges = int(int32(binary.LittleEndian.Uint32(r.data[4*idMapTrackChange:])))
			}
		case tagCharShape:
			sum.CharShapes++
		case tagParaShape:
			sum.ParaShapes++
		case tagStyle:
			st, err := parseStyle(r.data, len(sum.Styles))
			if err != nil {
				return err
			}
			sum.Styles = append(sum.Styles, st)
		case tagBinData:
			bi, err := parseBinData(r.data, len(sum.BinData)+1, streams, compressed)
			if err != nil {
				return err
			}
			sum.BinData = append(sum.BinData, bi)
		case tagTrackChange:
			sum.TrackChanges++
		}
	}
	if trackChanges > sum.TrackChanges {
		sum.TrackChanges = trackChanges
	}
	return nil
}

// parseStyle reads HWPTAG_STYLE (4.2.11, table 47): local name, English
// name, then a property byte whose bits 0-2 give the kind (table 48).
func parseStyle(b []byte, id int) (model.Style, error) {
	rd := &reader{b: b}
	name := rd.wstr()
	eng := rd.wstr()
	prop := rd.u8()
	if rd.err != nil {
		return model.Style{}, fmt.Errorf("style %d: %w", id, rd.err)
	}
	typ := "PARA"
	if prop&7 == 1 {
		typ = "CHAR"
	}
	return model.Style{ID: strconv.Itoa(id), Type: typ, Name: name, EngName: eng}, nil
}

// parseBinData reads HWPTAG_BIN_DATA (4.2.3, tables 17 and 18). Embedded
// items live in BinData/BINxxxx.ext with a hexadecimal storage id. The ID
// reported is the 1-based item index that pictures refer to.
func parseBinData(b []byte, idx int, streams map[string][]byte, compressed bool) (model.BinItem, error) {
	rd := &reader{b: b}
	prop := rd.u16()
	bi := model.BinItem{ID: strconv.Itoa(idx)}
	switch prop & 0xf {
	case 0: // LINK
		abs := rd.wstr()
		rel := rd.wstr()
		bi.Path = abs
		if bi.Path == "" {
			bi.Path = rel
		}
	case 1, 2: // EMBEDDING, STORAGE
		storage := rd.u16()
		ext := "ole"
		if prop&0xf == 1 {
			ext = rd.wstr()
		}
		if rd.err != nil {
			break
		}
		want := fmt.Sprintf("BinData/BIN%04X.%s", storage, ext)
		for name, data := range streams {
			if !strings.EqualFold(name, want) {
				continue
			}
			bi.Path = name
			bi.Size = int64(len(data))
			// Bits 4-5: 0 follows the document default, 0x10 always
			// compressed, 0x20 never compressed.
			if c := prop & 0x30; c == 0x10 || (c == 0 && compressed) {
				if n, err := io.Copy(io.Discard, flate.NewReader(bytes.NewReader(data))); err == nil {
					bi.Size = n
				}
			}
			break
		}
		if bi.Path == "" {
			bi.Path = want
		}
		bi.MediaType = mediaTypes[strings.ToLower(ext)]
	}
	if rd.err != nil {
		return bi, fmt.Errorf("bin data %d: %w", idx, rd.err)
	}
	return bi, nil
}

var mediaTypes = map[string]string{
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"gif":  "image/gif",
	"bmp":  "image/bmp",
	"tif":  "image/tiff",
	"tiff": "image/tiff",
	"wmf":  "image/x-wmf",
	"emf":  "image/x-emf",
	"svg":  "image/svg+xml",
}

// reader reads little-endian fields and remembers the first error.
type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || len(r.b)-r.off < n {
		r.err = io.ErrUnexpectedEOF
		return nil
	}
	b := r.b[r.off : r.off+n]
	r.off += n
	return b
}

func (r *reader) u8() byte {
	if b := r.take(1); b != nil {
		return b[0]
	}
	return 0
}

func (r *reader) u16() uint16 {
	if b := r.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}

// wstr reads a WORD length followed by that many WCHARs.
func (r *reader) wstr() string {
	n := int(r.u16())
	b := r.take(2 * n)
	if b == nil {
		return ""
	}
	return decodeUTF16(b)
}

func decodeUTF16(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// Control character classes in PARA_TEXT (3.2.3, table 6). A char
// control takes one WCHAR; inline and extended controls take eight, the
// code repeated at both ends. An extended control has a CTRL_HEADER
// record, in the same order as the controls appear in the text, and its
// control id is in the two WCHARs after the code.
const (
	ctrlChar = iota
	ctrlInline
	ctrlExtended
)

func ctrlClass(c uint16) int {
	switch c {
	case 4, 5, 6, 7, 8, 9, 19, 20:
		return ctrlInline
	case 1, 2, 3, 11, 12, 14, 15, 16, 17, 18, 21, 22, 23:
		return ctrlExtended
	}
	return ctrlChar // 0, 10, 13, 24-31
}

// walker turns section record trees into blocks.
type walker struct {
	doc         *model.Document
	memoAnchors []memoAnchor
	memoBodies  []memoBody
	outer       model.Change // change of the object whose paragraphs are being read
}

type memoAnchor struct {
	idx    int          // index in Summary.Memos
	num    int          // memo number from the field record, -1 if absent
	change model.Change // change of the field
}

type memoBody struct {
	num   int
	paras []*node
}

// block adds the block of one paragraph at loc. The cell blocks of its
// tables, named under inner, follow it, then a text-box block when it
// anchors text boxes.
func (w *walker) block(n *node, loc, inner string, kind model.Kind) {
	bi := len(w.doc.Blocks)
	w.doc.Blocks = append(w.doc.Blocks, model.Block{Loc: loc, Kind: kind})
	pr := w.para(n, inner)
	b := &w.doc.Blocks[bi]
	b.Text = pr.spans.Text(model.ViewAccepted)
	if pr.spans.Changed() {
		b.Spans = pr.spans
	}
	b.Break, b.Whole = pr.brk, pr.whole
	if pr.box.Len() > 0 {
		box := model.Block{Loc: loc + model.BoxSuffix, Kind: model.KindBox, Text: pr.box.Text(model.ViewAccepted)}
		if pr.box.Changed() {
			box.Spans = pr.box
		}
		w.doc.Blocks = append(w.doc.Blocks, box)
	}
}

func (w *walker) section(sec int, roots []*node) {
	p := 0
	for i := 0; i < len(roots); i++ {
		n := roots[i]
		switch n.tag {
		case tagParaHeader:
			loc := fmt.Sprintf("s%d/p%d", sec, p)
			p++
			w.block(n, loc, loc, model.KindPara)
			w.memoLists(n.kids)
		case tagMemoList:
			i = w.memoList(roots, i)
		}
	}
}

// memoLists collects memo bodies from a sibling list. Hwp stores them at
// the end of the last section (3.2.3), nested under the last paragraph.
func (w *walker) memoLists(nodes []*node) {
	for i := 0; i < len(nodes); i++ {
		if nodes[i].tag == tagMemoList {
			i = w.memoList(nodes, i)
		}
	}
}

// memoList reads the HWPTAG_MEMO_LIST at nodes[i] and the list header
// and paragraphs that follow it, and returns the index of the last one.
func (w *walker) memoList(nodes []*node, i int) int {
	num := -1
	if d := nodes[i].data; len(d) >= 4 {
		num = int(binary.LittleEndian.Uint32(d))
	}
	var paras []*node
	j := i + 1
	for ; j < len(nodes); j++ {
		switch nodes[j].tag {
		case tagListHeader:
			continue
		case tagParaHeader:
			paras = append(paras, nodes[j])
			continue
		}
		break
	}
	w.memoBodies = append(w.memoBodies, memoBody{num: num, paras: paras})
	return j - 1
}

// pairMemos fills memo text. A memo field's record ends with the memo
// number that its MEMO_LIST carries; without it memos pair in order.
func (w *walker) pairMemos() {
	memos := w.doc.Summary.Memos
	used := make([]bool, len(w.memoBodies))
	paired := make([]bool, len(w.memoAnchors))
	pair := func(i int, a memoAnchor, b memoBody) {
		sp := joinLines(b.paras, a.change)
		memos[a.idx].Text = sp.Text(model.ViewAccepted)
		paired[i] = sp.Len() > 0
	}
	for i, a := range w.memoAnchors {
		for j, b := range w.memoBodies {
			if !used[j] && a.num >= 0 && b.num == a.num {
				pair(i, a, b)
				used[j] = true
				break
			}
		}
	}
	next := 0
	for i, a := range w.memoAnchors {
		if paired[i] {
			continue
		}
		for next < len(used) && used[next] {
			next++
		}
		if next < len(used) {
			pair(i, a, w.memoBodies[next])
			used[next] = true
		}
	}
}

// paraText returns the PARA_TEXT code units of a PARA_HEADER node.
func paraText(n *node) []uint16 {
	for _, k := range n.kids {
		if k.tag == tagParaText {
			u := make([]uint16, len(k.data)/2)
			for i := range u {
				u[i] = binary.LittleEndian.Uint16(k.data[2*i:])
			}
			return u
		}
	}
	return nil
}

// trackRange is one tracked insertion or deletion of a paragraph: the
// UTF-16 positions start..end of its PARA_TEXT.
type trackRange struct {
	start, end uint32
	change     model.Change
}

// changes are the tracked insertions and deletions of one paragraph.
type changes struct {
	ranges []trackRange
	brk    model.Change // change that takes the paragraph break
	outer  model.Change // change of the object the paragraph is in
}

// paraChanges reads the insertion (kind 0x10) and deletion (kind 0x11)
// range tags of a PARA_HEADER node. A range tag (4.3.5) is start, end and
// a tag whose top byte is the kind. The break is taken the way the HWPX
// export of the same document marks it, so that a paragraph has the same
// changes read from either file: only when the range is the paragraph's
// one change range and ends at its character count, which counts the
// paragraph end character.
func paraChanges(n *node) changes {
	var cs changes
	for _, k := range n.kids {
		if k.tag != tagParaRangeTag {
			continue
		}
		for d := k.data; len(d) >= 12; d = d[12:] {
			start := binary.LittleEndian.Uint32(d)
			end := binary.LittleEndian.Uint32(d[4:])
			var c model.Change
			switch binary.LittleEndian.Uint32(d[8:]) >> 24 {
			case rangeInsert:
				c = model.Inserted
			case rangeDelete:
				c = model.Deleted
			default:
				continue
			}
			if start <= end {
				cs.ranges = append(cs.ranges, trackRange{start, end, c})
			}
		}
	}
	if len(cs.ranges) == 1 && len(n.data) >= 4 && cs.ranges[0].end == binary.LittleEndian.Uint32(n.data)&0x7fffffff {
		cs.brk = cs.ranges[0].change
	}
	return cs
}

// at returns the change of the code unit at position i.
func (cs changes) at(i int) model.Change {
	c := cs.outer
	for _, r := range cs.ranges {
		if uint32(i) >= r.start && uint32(i) < r.end {
			c |= r.change
		}
	}
	return c
}

// plainText is the text of one paragraph without its controls, and the
// change that takes its break. outer is the change of the object the
// paragraph is in, which covers all of it.
func plainText(n *node, outer model.Change) (model.Spans, model.Change) {
	cs := paraChanges(n)
	cs.outer = outer
	var t textBuilder
	u := paraText(n)
	for i := 0; i < len(u); {
		c := u[i]
		if c >= 32 {
			t.unit(c, cs.at(i))
			i++
			continue
		}
		if ctrlClass(c) != ctrlChar {
			if c == 9 {
				t.str("\t", cs.at(i))
			}
			i += 8
			continue
		}
		if c == 13 {
			break
		}
		t.char(c, cs.at(i))
		i++
	}
	return t.spans(), cs.brk | outer
}

// joinLines joins the text of a paragraph list in an object with change
// outer, one line per paragraph. The line break after a paragraph has the
// change that takes its break.
func joinLines(paras []*node, outer model.Change) model.Spans {
	var out model.Spans
	var brk model.Change
	for i, p := range paras {
		if i > 0 {
			out.Add("\n", brk)
		}
		var line model.Spans
		line, brk = plainText(p, outer)
		out.Append(line)
	}
	return out
}

// textBuilder collects UTF-16 code units and strings in order, split by
// change, and keeps the change that covers all of them.
type textBuilder struct {
	sp    model.Spans
	run   []uint16
	runC  model.Change
	cover model.Change
	any   bool
}

func (t *textBuilder) unit(c uint16, ch model.Change) {
	if len(t.run) > 0 && t.runC != ch {
		t.flush()
	}
	t.run = append(t.run, c)
	t.runC = ch
	t.content(ch)
}

func (t *textBuilder) flush() {
	if len(t.run) > 0 {
		t.sp.Add(string(utf16.Decode(t.run)), t.runC)
		t.run = t.run[:0]
	}
}

func (t *textBuilder) str(s string, ch model.Change) {
	t.flush()
	t.sp.Add(s, ch)
	t.content(ch)
}

// content notes a piece of paragraph content with change ch.
func (t *textBuilder) content(ch model.Change) {
	if !t.any {
		t.cover, t.any = ch, true
		return
	}
	t.cover &= ch
}

// covered returns the change that covers all content, or every change
// when there is none.
func (t *textBuilder) covered() model.Change {
	if !t.any {
		return model.Inserted | model.Deleted
	}
	return t.cover
}

// char writes a char-class control: line break, hyphen and the two kinds
// of fixed space; others write nothing.
func (t *textBuilder) char(c uint16, ch model.Change) {
	switch c {
	case 10:
		t.str("\n", ch)
	case 24:
		t.str("-", ch)
	case 30, 31:
		t.str(" ", ch)
	}
}

func (t *textBuilder) spans() model.Spans {
	t.flush()
	return t.sp
}

// paraResult is the text of one paragraph.
type paraResult struct {
	spans model.Spans
	box   model.Spans  // text of the text boxes anchored in it, one line per paragraph
	brk   model.Change // change that takes the paragraph break
	whole model.Change // change that covers the whole paragraph, break included
}

// para returns the text of one paragraph and, apart from it, the text of
// the text boxes anchored in it, one line per text-box paragraph. Tables
// in it are added to the document as cell blocks under loc/tN, pictures
// and memo anchors to the summary.
func (w *walker) para(n *node, loc string) paraResult {
	var ctrls []*node
	for _, k := range n.kids {
		if k.tag == tagCtrlHeader {
			ctrls = append(ctrls, k)
		}
	}
	cs := paraChanges(n)
	cs.outer = w.outer
	if len(cs.ranges) > 0 {
		w.doc.Tracked = true
	}
	var t textBuilder
	var box model.Spans
	u := paraText(n)
	tables, ci := 0, 0
	for i := 0; i < len(u); {
		c := u[i]
		if c >= 32 {
			t.unit(c, cs.at(i))
			i++
			continue
		}
		switch ctrlClass(c) {
		case ctrlChar:
			if c == 13 {
				i = len(u)
				continue
			}
			if c == 10 || c == 24 || c == 30 || c == 31 {
				t.char(c, cs.at(i))
			}
			i++
		case ctrlInline:
			if c == 9 {
				t.str("\t", cs.at(i))
			}
			i += 8
		case ctrlExtended:
			var id uint32
			if i+2 < len(u) {
				id = uint32(u[i+1]) | uint32(u[i+2])<<16
			}
			var ctrl *node
			if ci < len(ctrls) {
				ctrl = ctrls[ci]
			}
			ci++
			ch := cs.at(i)
			i += 8
			if c == 11 {
				t.content(ch)
			}
			if ctrl == nil {
				continue
			}
			switch {
			case c == 3 && id == fieldMemo:
				w.memoAnchor(loc, ctrl, ch)
			case c == 11 && ctrlHeaderID(ctrl) == ctrlTable:
				outer := w.outer
				w.outer = ch
				w.table(ctrl, fmt.Sprintf("%s/t%d", loc, tables))
				w.outer = outer
				tables++
			case c == 11 && ctrlHeaderID(ctrl) == ctrlShape:
				if b := w.shape(ctrl, loc, ch); b.Len() > 0 {
					if box.Len() > 0 {
						box.Add("\n", ch)
					}
					box.Append(b)
				}
			}
			// Headers, footers, footnotes, endnotes, hidden comments and
			// captions carry their own paragraph lists; they are not part
			// of the paragraph text.
		}
	}
	brk := cs.brk | w.outer
	return paraResult{spans: t.spans(), box: box, brk: brk, whole: t.covered() & brk}
}

func ctrlHeaderID(n *node) uint32 {
	if len(n.data) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(n.data)
}

// memoAnchor records a memo field. Its CTRL_HEADER is a field start
// (spec section 4.3.10 item 15, table 152); Hwp writes a memo number after the id.
// change is the change of the field, which covers the memo text.
func (w *walker) memoAnchor(loc string, ctrl *node, change model.Change) {
	num := -1
	rd := &reader{b: ctrl.data}
	rd.take(4 + 4 + 1)
	rd.wstr()
	rd.take(4)
	if b := rd.take(4); b != nil {
		num = int(binary.LittleEndian.Uint32(b))
	}
	sum := &w.doc.Summary
	w.memoAnchors = append(w.memoAnchors, memoAnchor{idx: len(sum.Memos), num: num, change: change})
	sum.Memos = append(sum.Memos, model.Memo{Loc: loc})
}

// table reads a 'tbl ' control. Its children are the HWPTAG_TABLE record
// (spec section 4.3.9 item 1, table 75: properties, RowCount, nCols), then for each cell a
// LIST_HEADER followed by the cell's paragraphs. A LIST_HEADER before the
// TABLE record is the caption and is skipped.
func (w *walker) table(ctrl *node, loc string) {
	sum := &w.doc.Summary
	ti := len(sum.Tables)
	sum.Tables = append(sum.Tables, model.Table{Loc: loc})
	seenTable := false
	kids := ctrl.kids
	for i := 0; i < len(kids); i++ {
		k := kids[i]
		switch {
		case k.tag == tagTable:
			seenTable = true
			if len(k.data) >= 8 {
				sum.Tables[ti].Rows = int(binary.LittleEndian.Uint16(k.data[4:]))
				sum.Tables[ti].Cols = int(binary.LittleEndian.Uint16(k.data[6:]))
			}
		case k.tag == tagListHeader && seenTable:
			j := i + 1
			for j < len(kids) && kids[j].tag == tagParaHeader {
				j++
			}
			w.cell(k, kids[i+1:j], loc)
			i = j - 1
		}
	}
}

// cell adds the blocks of one table cell: one block at the cell's
// location when it has one paragraph, one block per paragraph at loc/pN
// when it has more. The list header (table 65) is
// followed by the cell properties (table 80) whose first fields are the
// column and row address. The spec gives the list header as 6 bytes, but
// Hwp writes 8 (the paragraph count is 4 bytes), so the address starts
// at offset 8; merged cells keep the address of their top-left slot.
func (w *walker) cell(lh *node, paras []*node, tblLoc string) {
	row, col := 0, 0
	if len(lh.data) >= 12 {
		col = int(binary.LittleEndian.Uint16(lh.data[8:]))
		row = int(binary.LittleEndian.Uint16(lh.data[10:]))
	}
	loc := fmt.Sprintf("%s/r%d/c%d", tblLoc, row, col)
	for i, p := range paras {
		inner := fmt.Sprintf("%s/p%d", loc, i)
		if len(paras) == 1 {
			w.block(p, loc, inner, model.KindCell)
		} else {
			w.block(p, inner, inner, model.KindCell)
		}
	}
}

// shape reads a 'gso ' control. Every picture inside it (groups
// included) is recorded at loc, and the text of its text boxes is
// returned, nothing when they have none. change is the change of the
// control, which covers the text boxes.
// A LIST_HEADER directly under the control is the caption and is skipped.
func (w *walker) shape(ctrl *node, loc string, change model.Change) model.Spans {
	var paras []*node
	var walk func(nodes []*node)
	walk = func(nodes []*node) {
		for i := 0; i < len(nodes); i++ {
			k := nodes[i]
			switch k.tag {
			case tagShapePicture:
				w.doc.Summary.Pictures = append(w.doc.Summary.Pictures, model.Picture{Loc: loc, Ref: pictureRef(k.data)})
			case tagListHeader:
				j := i + 1
				for ; j < len(nodes) && nodes[j].tag == tagParaHeader; j++ {
					paras = append(paras, nodes[j])
				}
				i = j - 1
				continue
			}
			walk(k.kids)
		}
	}
	for _, k := range ctrl.kids {
		if k.tag == tagShapeComp {
			walk([]*node{k})
		}
	}
	for _, p := range paras {
		if sp, _ := plainText(p, change); sp.Len() > 0 {
			return joinLines(paras, change)
		}
	}
	return nil
}

// pictureRef returns the BinItem id of HWPTAG_SHAPE_COMPONENT_PICTURE
// (spec section 4.3.9 item 4, table 107): border color, thickness and properties (12),
// image rectangle x and y (32), crop rectangle (16), inner margins (8),
// then the picture info (table 32) whose last field is the BinItem id.
func pictureRef(b []byte) string {
	const off = 12 + 32 + 16 + 8 + 3
	if len(b) < off+2 {
		return ""
	}
	return strconv.Itoa(int(binary.LittleEndian.Uint16(b[off:])))
}
