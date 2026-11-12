// Package apply edits an HWPX document from a JSON spec, keeping the
// original formatting and, by default, recording every edit as a tracked
// change that Hancom Office can accept or reject.
//
// Section XML is not re-serialized. Each edit becomes a byte-range patch
// on the original part, so paragraphs that are not edited, namespace
// prefixes and attribute order stay exactly as they were. Edited
// paragraphs lose their hp:linesegarray (cached line layout), which Hancom
// Office rebuilds; a stale one makes it stop laying out the document.
package apply

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jollaman999/why-hwp/internal/hwpx"
	"github.com/jollaman999/why-hwp/internal/model"
)

// Options controls how edits are recorded.
type Options struct {
	Track   bool      // record edits as tracked changes
	Author  string    // tracked-change author; required when Track is set
	Date    time.Time // tracked-change time
	BaseDir string    // base for relative image paths
}

// Result is the edited document and what was done to it.
type Result struct {
	Data     []byte
	Ops      int
	Inserted int // tracked insertions (or plain insertions when not tracking)
	Deleted  int // tracked deletions (or plain removals when not tracking)
	Images   int
	AuthorID string // tracked-change author id used, "" when not tracking
}

// Apply runs spec against the HWPX package src and returns the new package.
func Apply(src []byte, spec *Spec, opt Options) (*Result, error) {
	if opt.Track && strings.TrimSpace(opt.Author) == "" {
		return nil, fmt.Errorf("an author is required to record tracked changes")
	}
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		return nil, err
	}
	ed := &editor{opt: opt, files: map[string]*zip.File{}, parts: map[string][]byte{}}
	for _, f := range zr.File {
		ed.files[f.Name] = f
	}
	if ed.files["mimetype"] == nil {
		return nil, fmt.Errorf("not an HWPX package: no mimetype entry")
	}
	if err := ed.load(); err != nil {
		return nil, err
	}
	for i := range spec.Ops {
		if err := ed.run(&spec.Ops[i]); err != nil {
			return nil, fmt.Errorf("%s: %w", spec.Ops[i].label(i), err)
		}
	}
	for _, s := range ed.secs {
		out, err := ed.render(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.name, err)
		}
		if out != nil {
			ed.parts[s.name] = out
		}
	}
	if opt.Track && (ed.tcIns != 0 || ed.tcDel != 0) {
		if err := ed.updateHeader(); err != nil {
			return nil, fmt.Errorf("%s: %w", headerName, err)
		}
	}
	if len(ed.images) > 0 {
		if err := ed.updateManifest(); err != nil {
			return nil, fmt.Errorf("%s: %w", hpfName, err)
		}
	}
	data, err := ed.write(zr)
	if err != nil {
		return nil, err
	}
	// The result must still read as HWPX: this catches malformed XML.
	zr2, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("output check: %w", err)
	}
	if _, err := hwpx.Parse(zr2); err != nil {
		return nil, fmt.Errorf("output check: %w", err)
	}
	res := &Result{Data: data, Ops: len(spec.Ops), Inserted: ed.nIns, Deleted: ed.nDel, Images: len(ed.images)}
	if opt.Track {
		res.AuthorID = ed.authorID
	}
	return res, nil
}

const (
	headerName = "Contents/header.xml"
	hpfName    = "Contents/content.hpf"
)

type editor struct {
	opt   Options
	files map[string]*zip.File
	parts map[string][]byte // edited parts
	secs  []*section

	header   []byte
	nextMark int // next hp:insertBegin/deleteBegin Id
	nextTc   int // next hh:trackChange id
	tcIns    int // trackChange id for this run's insertions, 0 until used
	tcDel    int
	authorID string
	newAuth  bool

	images    []newImage
	nextImage int
	nextObj   int64
	nextZ     int

	nIns, nDel int
}

func (ed *editor) read(name string) ([]byte, error) {
	f := ed.files[name]
	if f == nil {
		return nil, fmt.Errorf("%s: missing", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return b, nil
}

var (
	markIDRe = regexp.MustCompile(`:(?:insert|delete)(?:Begin|End)\s[^>]*?\bId="(\d+)"`)
	objIDRe  = regexp.MustCompile(`\s(?:id|instid)="(\d+)"`)
	zOrderRe = regexp.MustCompile(`\szOrder="(\d+)"`)
	imageNRe = regexp.MustCompile(`^(?:BinData/)?image(\d+)(?:\.[A-Za-z0-9]+)?$`)
)

// load reads the package parts and indexes the sections.
func (ed *editor) load() error {
	hpf, err := ed.read(hpfName)
	if err != nil {
		return err
	}
	names, ids, err := manifest(hpf)
	if err != nil {
		return fmt.Errorf("%s: %w", hpfName, err)
	}
	if len(names) == 0 {
		for name := range ed.files {
			if isSection(name) {
				names = append(names, name)
			}
		}
		sort.Slice(names, func(i, j int) bool { return sectionNum(names[i]) < sectionNum(names[j]) })
	}
	if len(names) == 0 {
		return fmt.Errorf("no sections in package")
	}
	for i, name := range names {
		raw, err := ed.read(name)
		if err != nil {
			return err
		}
		s, err := indexSection(i, name, raw)
		if err != nil {
			return err
		}
		ed.secs = append(ed.secs, s)
		for _, m := range markIDRe.FindAllSubmatch(raw, -1) {
			ed.nextMark = max(ed.nextMark, atoi(m[1]))
		}
		for _, m := range objIDRe.FindAllSubmatch(raw, -1) {
			if v, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil && v < 1<<31 {
				ed.nextObj = max(ed.nextObj, v)
			}
		}
		for _, m := range zOrderRe.FindAllSubmatch(raw, -1) {
			ed.nextZ = max(ed.nextZ, atoi(m[1]))
		}
	}
	ed.nextMark++
	for _, id := range ids {
		if m := imageNRe.FindStringSubmatch(id); m != nil {
			ed.nextImage = max(ed.nextImage, atoi([]byte(m[1])))
		}
	}
	for name := range ed.files {
		if m := imageNRe.FindStringSubmatch(name); m != nil && strings.HasPrefix(name, "BinData/") {
			ed.nextImage = max(ed.nextImage, atoi([]byte(m[1])))
		}
	}
	if ed.opt.Track {
		if ed.header, err = ed.read(headerName); err != nil {
			return err
		}
		if err := ed.readTrackInfo(); err != nil {
			return fmt.Errorf("%s: %w", headerName, err)
		}
	}
	return nil
}

// manifest returns the section parts in spine order and every manifest
// item id, the same way the hwpx reader picks sections.
func manifest(hpf []byte) ([]string, []string, error) {
	dec := xml.NewDecoder(bytes.NewReader(hpf))
	href := map[string]string{}
	var ids, spine []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "item":
			id := attr(se, "id")
			ids = append(ids, id)
			href[id] = attr(se, "href")
		case "itemref":
			spine = append(spine, attr(se, "idref"))
		}
	}
	var secs []string
	for _, id := range spine {
		if h, ok := href[id]; ok && isSection(h) {
			secs = append(secs, h)
		}
	}
	return secs, ids, nil
}

func isSection(name string) bool {
	return strings.HasPrefix(name, "Contents/section") && strings.HasSuffix(name, ".xml")
}

func sectionNum(name string) int {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(path.Base(name), "section"), ".xml"))
	if err != nil {
		return 1 << 30
	}
	return n
}

func atoi(b []byte) int {
	v, _ := strconv.Atoi(string(b))
	return v
}

var locRe = regexp.MustCompile(`^s(\d+)/p\d+(?:/t\d+/r\d+/c\d+(?:/p\d+)?)*$`)

// resolve finds the paragraph a loc names. A cell loc names the cell's
// first paragraph; cell is then the cell. The location of text-box text
// names no paragraph that can be edited.
func (ed *editor) resolve(loc string) (p *para, cell *container, err error) {
	if strings.HasSuffix(loc, model.BoxSuffix) {
		return nil, nil, fmt.Errorf("loc %q is text-box text, which apply cannot edit", loc)
	}
	m := locRe.FindStringSubmatch(loc)
	if m == nil {
		return nil, nil, fmt.Errorf("bad loc %q (want e.g. s0/p3 or s0/p3/t0/r1/c2)", loc)
	}
	si := atoi([]byte(m[1]))
	if si >= len(ed.secs) {
		return nil, nil, fmt.Errorf("loc %q: no section %d", loc, si)
	}
	s := ed.secs[si]
	if p := s.byLoc[loc]; p != nil {
		return p, nil, nil
	}
	if c := s.cells[loc]; c != nil {
		if len(c.paras) == 0 {
			return nil, nil, fmt.Errorf("loc %q: cell has no paragraph", loc)
		}
		return c.paras[0], c, nil
	}
	return nil, nil, fmt.Errorf("loc %q not found (see 'why-hwp text')", loc)
}

// paraEdit is what the ops did to one paragraph.
type paraEdit struct {
	ts      []*tcontent // parallel to para.ts
	owner   string      // op that replaced or deleted the whole text
	deleted bool        // removed without tracking
	dirty   bool        // content changed: drop the line layout cache
	runHead map[int][]string
	runTail map[int][]string
	before  []string // new paragraphs before this one
	after   []string // new paragraphs after this one
}

func (ed *editor) edit(p *para) (*paraEdit, error) {
	if p.edit != nil {
		return p.edit, nil
	}
	e := &paraEdit{runHead: map[int][]string{}, runTail: map[int][]string{}}
	inDel := false
	for _, ts := range p.ts {
		atoms, err := parseT(string(p.sec.raw[ts.tagEnd:ts.contentEnd]))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.loc, err)
		}
		for i := range atoms {
			a := &atoms[i]
			if a.kind == atomMarker {
				switch {
				case strings.Contains(a.raw, ":deleteBegin"):
					inDel = true
				case strings.Contains(a.raw, ":deleteEnd"):
					inDel = false
				}
			}
			a.del = inDel
		}
		e.ts = append(e.ts, &tcontent{atoms: atoms})
	}
	p.edit = e
	return e, nil
}

// cref points at one atom of a paragraph.
type cref struct{ t, a int }

// visible returns the paragraph's text that is not already deleted, with
// the atom each character comes from.
func (e *paraEdit) visible() ([]rune, []cref) {
	var text []rune
	var refs []cref
	for ti, tc := range e.ts {
		for ai, a := range tc.atoms {
			if a.kind == atomChar && !a.del && !a.mark && !a.drop {
				text = append(text, a.ch)
				refs = append(refs, cref{ti, ai})
			}
		}
	}
	return text, refs
}

// remove deletes the atoms: tracked, they are wrapped in deletion
// markers when the content is written; untracked, they are dropped.
func (ed *editor) remove(e *paraEdit, refs []cref) {
	for _, r := range refs {
		a := &e.ts[r.t].atoms[r.a]
		if ed.opt.Track {
			a.mark = true
		} else {
			a.drop = true
		}
		e.ts[r.t].changed = true
	}
	if !ed.opt.Track && len(refs) > 0 {
		ed.nDel++
	}
	e.dirty = true
}

// insertText returns text as hp:t content, wrapped in insertion markers
// when tracking. paraEnd marks the paragraph break as inserted too.
func (ed *editor) insertText(pfx, text string, paraEnd bool) string {
	return ed.insertRaw(pfx, textXML(pfx, text), paraEnd)
}

func (ed *editor) insertRaw(pfx, content string, paraEnd bool) string {
	if !ed.opt.Track {
		ed.nIns++
		return content
	}
	id := ed.mark()
	tc := ed.tc(&ed.tcIns)
	ed.nIns++
	return fmt.Sprintf(`<%s:insertBegin Id="%d" TcId="%d"/>%s<%s:insertEnd Id="%d" TcId="%d" paraend="%s"/>`,
		pfx, id, tc, content, pfx, id, tc, newBreak(paraEnd))
}

func (ed *editor) deleteBegin(pfx string, id int) string {
	return fmt.Sprintf(`<%s:deleteBegin Id="%d" TcId="%d"/>`, pfx, id, ed.tc(&ed.tcDel))
}

func (ed *editor) deleteEnd(pfx string, id int, paraEnd bool) string {
	return fmt.Sprintf(`<%s:deleteEnd Id="%d" TcId="%d" paraend="%d"/>`, pfx, id, ed.tc(&ed.tcDel), b2i(paraEnd))
}

func (ed *editor) mark() int {
	id := ed.nextMark
	ed.nextMark++
	return id
}

func (ed *editor) tc(slot *int) int {
	if *slot == 0 {
		*slot = ed.nextTc
		ed.nextTc++
	}
	return *slot
}

// breakToken stands for the paraend value of a new paragraph until render
// knows whether that paragraph ends up last in its list.
const breakToken = "\x00break\x00"

func newBreak(paraEnd bool) string {
	if paraEnd {
		return breakToken
	}
	return "0"
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// placeText puts XML for new text into the paragraph: after the last atom
// of the first hp:t that has text, or into the last hp:t, or into a new
// hp:t at the end of the last run.
func (ed *editor) placeText(p *para, e *paraEdit, x string) error {
	ti := -1
	for i, tc := range e.ts {
		for _, a := range tc.atoms {
			if a.kind == atomChar {
				ti = i
				break
			}
		}
		if ti >= 0 {
			break
		}
	}
	if ti < 0 && len(e.ts) > 0 {
		ti = len(e.ts) - 1
	}
	e.dirty = true
	if ti >= 0 {
		tc := e.ts[ti]
		tc.changed = true
		if n := len(tc.atoms); n > 0 {
			tc.atoms[n-1].after = append(tc.atoms[n-1].after, x)
		} else {
			tc.head = append(tc.head, x)
		}
		return nil
	}
	if len(p.runs) == 0 {
		return fmt.Errorf("%s: paragraph has no run to hold text", p.loc)
	}
	r := len(p.runs) - 1
	e.runTail[r] = append(e.runTail[r], fmt.Sprintf("<%s:t>%s</%s:t>", p.sec.pfx, x, p.sec.pfx))
	return nil
}

// placeEnd puts XML at the very end of the paragraph's text: after the
// last atom of its last hp:t, or in a new hp:t at the end of the last run.
func (ed *editor) placeEnd(p *para, e *paraEdit, x string) error {
	e.dirty = true
	if n := len(e.ts); n > 0 {
		tc := e.ts[n-1]
		tc.changed = true
		if k := len(tc.atoms); k > 0 {
			tc.atoms[k-1].after = append(tc.atoms[k-1].after, x)
		} else {
			tc.head = append(tc.head, x)
		}
		return nil
	}
	if len(p.runs) == 0 {
		return fmt.Errorf("%s: paragraph has no run", p.loc)
	}
	r := len(p.runs) - 1
	e.runTail[r] = append(e.runTail[r], fmt.Sprintf("<%s:t>%s</%s:t>", p.sec.pfx, x, p.sec.pfx))
	return nil
}

func isLast(p *para) bool {
	return p.cont.paras[len(p.cont.paras)-1] == p
}

func prevSibling(p *para) *para {
	for i, q := range p.cont.paras {
		if q == p && i > 0 {
			return p.cont.paras[i-1]
		}
	}
	return nil
}

func (ed *editor) run(o *Op) error {
	switch o.Op {
	case "set_text":
		p, cell, err := ed.resolve(o.Loc)
		if err != nil {
			return err
		}
		if cell != nil && len(cell.paras) > 1 {
			return fmt.Errorf("cell %s has %d paragraphs; address one as %s/pN", o.Loc, len(cell.paras), o.Loc)
		}
		e, err := ed.claim(p, "set_text")
		if err != nil {
			return err
		}
		_, refs := e.visible()
		ed.remove(e, refs)
		if *o.Text != "" {
			return ed.placeText(p, e, ed.insertText(p.sec.pfx, *o.Text, false))
		}
		return nil

	case "replace":
		p, cell, err := ed.resolve(o.Loc)
		if err != nil {
			return err
		}
		e, err := ed.edit(p)
		if err != nil {
			return err
		}
		if e.owner != "" {
			return fmt.Errorf("%s: already changed by %s", p.loc, e.owner)
		}
		text, refs := e.visible()
		find := []rune(o.Find)
		var hits []int
		for i := 0; i+len(find) <= len(text); {
			if string(text[i:i+len(find)]) == o.Find {
				hits = append(hits, i)
				i += len(find)
				continue
			}
			i++
		}
		switch {
		case len(hits) == 0 && cell != nil && len(cell.paras) > 1:
			return fmt.Errorf("%s: %q not found in %q; cell %s has %d paragraphs and its loc names the first, address the others as %s/pN",
				p.loc, o.Find, string(text), o.Loc, len(cell.paras), o.Loc)
		case len(hits) == 0:
			return fmt.Errorf("%s: %q not found in %q", p.loc, o.Find, string(text))
		case len(hits) > 1 && !o.All:
			return fmt.Errorf("%s: %q occurs %d times; set \"all\": true or use a longer find", p.loc, o.Find, len(hits))
		}
		for _, h := range hits {
			span := refs[h : h+len(find)]
			ed.remove(e, span)
			if *o.With != "" {
				last := span[len(span)-1]
				a := &e.ts[last.t].atoms[last.a]
				a.after = append(a.after, ed.insertText(p.sec.pfx, *o.With, false))
			}
		}
		return nil

	case "insert_after", "insert_before":
		p, _, err := ed.resolve(o.Loc)
		if err != nil {
			return err
		}
		if o.Op == "insert_before" && p.hasSecPr {
			return fmt.Errorf("%s holds the section settings; insert after it instead", p.loc)
		}
		e, err := ed.edit(p)
		if err != nil {
			return err
		}
		for _, np := range o.Paras {
			shape := p
			if np.Like != "" {
				if shape, _, err = ed.resolve(np.Like); err != nil {
					return fmt.Errorf("like: %w", err)
				}
			}
			x := ed.newPara(p.sec, shape, np.Text)
			if o.Op == "insert_after" {
				e.after = append(e.after, x)
			} else {
				e.before = append(e.before, x)
			}
		}
		return nil

	case "delete":
		p, _, err := ed.resolve(o.Loc)
		if err != nil {
			return err
		}
		switch {
		case p.hasSecPr:
			return fmt.Errorf("%s holds the section settings and cannot be deleted", p.loc)
		case p.hasTable:
			return fmt.Errorf("%s holds a table; deleting tables is not supported", p.loc)
		}
		e, err := ed.claim(p, "delete")
		if err != nil {
			return err
		}
		if !ed.opt.Track {
			if p.hasMemo {
				return fmt.Errorf("%s holds a memo; untracked delete would drop it", p.loc)
			}
			if len(p.cont.paras) == 1 {
				return fmt.Errorf("%s is the only paragraph of its %s; use set_text", p.loc, contName(p.cont))
			}
			e.deleted = true
			ed.nDel++
			return nil
		}
		if len(p.runs) == 0 {
			return fmt.Errorf("%s: paragraph has no run", p.loc)
		}
		// One deletion range from the start of the first run to the end of
		// the last, so pictures and fields in the paragraph go with it.
		// The paragraph break goes with it, except for the last paragraph of
		// a list: its break stays and the break before it is deleted instead,
		// as Hancom Office does.
		pfx := p.sec.pfx
		id := ed.mark()
		last := isLast(p)
		if prev := prevSibling(p); last && prev != nil {
			pe, err := ed.edit(prev)
			if err != nil {
				return err
			}
			if pe.owner != "delete" {
				bid := ed.mark()
				if err := ed.placeEnd(prev, pe, ed.deleteBegin(pfx, bid)+ed.deleteEnd(pfx, bid, true)); err != nil {
					return err
				}
			}
		}
		e.runHead[0] = append(e.runHead[0], fmt.Sprintf("<%s:t>%s</%s:t>", pfx, ed.deleteBegin(pfx, id), pfx))
		r := len(p.runs) - 1
		e.runTail[r] = append(e.runTail[r], fmt.Sprintf("<%s:t>%s</%s:t>", pfx, ed.deleteEnd(pfx, id, !last), pfx))
		e.dirty = true
		ed.nDel++
		return nil

	case "image":
		loc := o.Loc
		if o.After != "" {
			loc = o.After
		}
		p, _, err := ed.resolve(loc)
		if err != nil {
			return err
		}
		e, err := ed.edit(p)
		if err != nil {
			return err
		}
		pic, err := ed.addImage(p.sec, o.Src, o.WidthMM)
		if err != nil {
			return err
		}
		pfx := p.sec.pfx
		if o.After != "" {
			shape := p
			if o.Like != "" {
				if shape, _, err = ed.resolve(o.Like); err != nil {
					return fmt.Errorf("like: %w", err)
				}
			}
			e.after = append(e.after, ed.picPara(p.sec, shape, pic))
			return nil
		}
		if e.owner == "delete" {
			return fmt.Errorf("%s: already deleted", p.loc)
		}
		if len(p.runs) == 0 {
			return fmt.Errorf("%s: paragraph has no run", p.loc)
		}
		r := len(p.runs) - 1
		e.runTail[r] = append(e.runTail[r], ed.picContent(pfx, pic, false))
		e.dirty = true
		return nil
	}
	return fmt.Errorf("unknown op")
}

func contName(c *container) string {
	if c.loc == "" {
		return "section"
	}
	return "cell"
}

// claim marks p as wholly changed by op; set_text and delete do not mix
// with other text edits of the same paragraph.
func (ed *editor) claim(p *para, op string) (*paraEdit, error) {
	e, err := ed.edit(p)
	if err != nil {
		return nil, err
	}
	if e.owner != "" {
		return nil, fmt.Errorf("%s: already changed by %s", p.loc, e.owner)
	}
	for _, tc := range e.ts {
		if tc.changed {
			return nil, fmt.Errorf("%s: already changed by replace", p.loc)
		}
	}
	e.owner = op
	return e, nil
}

// shapes returns the paragraph shape, style and character shape to copy
// from p: the character shape of the first run that has text, else of the
// first run.
func shapes(p *para) (paraPr, style, charPr string) {
	paraPr, style, charPr = p.paraPr, p.style, "0"
	if paraPr == "" {
		paraPr = "0"
	}
	if style == "" {
		style = "0"
	}
	if len(p.runs) > 0 {
		charPr = p.runs[0].charPr
	}
	for _, ts := range p.ts {
		if ts.run >= 0 && ts.contentEnd > ts.tagEnd {
			charPr = p.runs[ts.run].charPr
			break
		}
	}
	if charPr == "" {
		charPr = "0"
	}
	return
}

func (ed *editor) newPara(s *section, shape *para, text string) string {
	pp, st, cp := shapes(shape)
	pfx := s.pfx
	var body string
	switch {
	case text == "" && !ed.opt.Track:
		body = fmt.Sprintf(`<%s:run charPrIDRef="%s"/>`, pfx, cp)
		ed.nIns++
	default:
		body = fmt.Sprintf(`<%s:run charPrIDRef="%s"><%s:t>%s</%s:t></%s:run>`, pfx, cp, pfx, ed.insertText(pfx, text, true), pfx, pfx)
	}
	return paraOpen(pfx, pp, st) + body + "</" + pfx + ":p>"
}

func (ed *editor) picPara(s *section, shape *para, pic string) string {
	pp, st, cp := shapes(shape)
	pfx := s.pfx
	return paraOpen(pfx, pp, st) + fmt.Sprintf(`<%s:run charPrIDRef="%s">`, pfx, cp) + ed.picContent(pfx, pic, true) + "</" + pfx + ":run></" + pfx + ":p>"
}

// picContent is run content holding pic, with insertion markers around it
// when tracking.
func (ed *editor) picContent(pfx, pic string, paraEnd bool) string {
	if !ed.opt.Track {
		ed.nIns++
		return pic + "<" + pfx + ":t/>"
	}
	id := ed.mark()
	tc := ed.tc(&ed.tcIns)
	ed.nIns++
	return fmt.Sprintf(`<%s:t><%s:insertBegin Id="%d" TcId="%d"/></%s:t>%s<%s:t><%s:insertEnd Id="%d" TcId="%d" paraend="%s"/></%s:t>`,
		pfx, pfx, id, tc, pfx, pic, pfx, pfx, id, tc, newBreak(paraEnd), pfx)
}

func paraOpen(pfx, paraPr, style string) string {
	return fmt.Sprintf(`<%s:p id="0" paraPrIDRef="%s" styleIDRef="%s" pageBreak="0" columnBreak="0" merged="0">`, pfx, escAttr(paraPr), escAttr(style))
}

// patch replaces raw[pos:end] with data.
type patch struct {
	pos, end int
	order    int // for insertions at the same position
	seq      int
	data     string
}

const (
	orderAfter  = 0 // paragraphs added after the paragraph that ends here
	orderBefore = 1 // paragraphs added before the paragraph that starts here
	orderInside = 2
)

// render applies the section's edits and returns the new bytes, or nil
// when nothing in the section changed.
func (ed *editor) render(s *section) ([]byte, error) {
	var ps []patch
	add := func(pos, end, order int, data string) {
		ps = append(ps, patch{pos: pos, end: end, order: order, seq: len(ps), data: data})
	}
	for _, p := range s.paras {
		e := p.edit
		if e == nil {
			continue
		}
		if len(e.before) > 0 {
			add(p.start, p.start, orderBefore, strings.ReplaceAll(strings.Join(e.before, ""), breakToken, "1"))
		}
		if n := len(e.after); n > 0 {
			after := strings.ReplaceAll(strings.Join(e.after[:n-1], ""), breakToken, "1")
			lastBreak := "1"
			if ed.opt.Track && isLast(p) && !e.deleted {
				// The list's final break cannot be inserted: mark the break
				// of p as inserted instead and leave the last new one as is.
				lastBreak = "0"
				id := ed.mark()
				tc := ed.tc(&ed.tcIns)
				x := fmt.Sprintf(`<%s:insertBegin Id="%d" TcId="%d"/><%s:insertEnd Id="%d" TcId="%d" paraend="1"/>`, s.pfx, id, tc, s.pfx, id, tc)
				if err := ed.placeEnd(p, e, x); err != nil {
					return nil, err
				}
			}
			after += strings.ReplaceAll(e.after[n-1], breakToken, lastBreak)
			add(p.end, p.end, orderAfter, after)
		}
		if e.deleted {
			add(p.start, p.end, orderInside, "")
			continue
		}
		if e.dirty && p.ls[0] >= 0 {
			add(p.ls[0], p.ls[1], orderInside, "")
		}
		for i, tc := range e.ts {
			if !tc.changed {
				continue
			}
			ts := p.ts[i]
			content := ed.renderT(s.pfx, tc)
			if ts.self {
				open, close := expand(string(s.raw[ts.start:ts.end]))
				add(ts.start, ts.end, orderInside, open+content+close)
			} else {
				add(ts.tagEnd, ts.contentEnd, orderInside, content)
			}
		}
		for ri, r := range p.runs {
			head, tail := strings.Join(e.runHead[ri], ""), strings.Join(e.runTail[ri], "")
			if head == "" && tail == "" {
				continue
			}
			if r.self {
				open, close := expand(string(s.raw[r.start:r.end]))
				add(r.start, r.end, orderInside, open+head+tail+close)
				continue
			}
			if head != "" {
				add(r.tagEnd, r.tagEnd, orderInside, head)
			}
			if tail != "" {
				add(r.closeStart, r.closeStart, orderInside, tail)
			}
		}
	}
	if len(ps) == 0 {
		return nil, nil
	}
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		if (a.end == a.pos) != (b.end == b.pos) {
			return a.end == a.pos
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return a.seq < b.seq
	})
	var out bytes.Buffer
	at := 0
	for _, pt := range ps {
		if pt.pos < at {
			return nil, fmt.Errorf("internal error: overlapping edits at byte %d", pt.pos)
		}
		out.Write(s.raw[at:pt.pos])
		out.WriteString(pt.data)
		at = pt.end
	}
	out.Write(s.raw[at:])
	return out.Bytes(), nil
}

// expand turns a self-closing tag "<x a=.../>" into "<x a=...>" and "</x>".
func expand(tag string) (string, string) {
	open := strings.TrimRight(strings.TrimSuffix(tag, "/>"), " \t\r\n")
	name := open[1:]
	if k := strings.IndexAny(name, " \t\r\n"); k >= 0 {
		name = name[:k]
	}
	return open + ">", "</" + name + ">"
}

// renderT writes the content of one hp:t with its edits. Consecutive
// removed characters share one deletion range.
func (ed *editor) renderT(pfx string, tc *tcontent) string {
	var b strings.Builder
	for _, x := range tc.head {
		b.WriteString(x)
	}
	open := 0
	for i, a := range tc.atoms {
		switch {
		case a.mark:
			if open == 0 {
				open = ed.mark()
				ed.nDel++
				b.WriteString(ed.deleteBegin(pfx, open))
			}
			b.WriteString(a.raw)
			if i == len(tc.atoms)-1 || !tc.atoms[i+1].mark || len(a.after) > 0 {
				b.WriteString(ed.deleteEnd(pfx, open, false))
				open = 0
			}
		case a.drop:
		default:
			b.WriteString(a.raw)
		}
		for _, x := range a.after {
			b.WriteString(x)
		}
	}
	return b.String()
}

// readTrackInfo finds the next free trackChange id and the author id,
// reusing an author entry with the same name.
func (ed *editor) readTrackInfo() error {
	pfx, err := rootPrefix(ed.header, nsHead)
	if err != nil {
		return err
	}
	tcRe := regexp.MustCompile(`<` + pfx + `:trackChange\s[^>]*>`)
	maxTc := 0
	for _, t := range tcRe.FindAll(ed.header, -1) {
		maxTc = max(maxTc, atoi([]byte(tagAttr(string(t), "id"))))
	}
	ed.nextTc = maxTc + 1
	auRe := regexp.MustCompile(`<` + pfx + `:trackChangeAuthor\s[^>]*>`)
	maxAu := 0
	for _, t := range auRe.FindAll(ed.header, -1) {
		id := tagAttr(string(t), "id")
		maxAu = max(maxAu, atoi([]byte(id)))
		if unescAttr(tagAttr(string(t), "name")) == ed.opt.Author && ed.authorID == "" {
			ed.authorID = id
		}
	}
	if ed.authorID == "" {
		ed.authorID = strconv.Itoa(maxAu + 1)
		ed.newAuth = true
	}
	return nil
}

var attrRe = regexp.MustCompile(`\s([A-Za-z_:][-A-Za-z0-9_:.]*)\s*=\s*"([^"]*)"`)

func tagAttr(tag, name string) string {
	for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
		if m[1] == name {
			return m[2]
		}
	}
	return ""
}

func unescAttr(s string) string {
	var v struct {
		A string `xml:"a,attr"`
	}
	if err := xml.Unmarshal([]byte(`<x a="`+s+`"/>`), &v); err != nil {
		return s
	}
	return v.A
}

// updateHeader registers this run's tracked-change entries and author in
// header.xml and updates the itemCnt of both lists.
func (ed *editor) updateHeader() error {
	h := string(ed.header)
	pfx, err := rootPrefix(ed.header, nsHead)
	if err != nil {
		return err
	}
	date := ed.opt.Date.UTC().Format("2006-01-02T15:04:05Z")
	var entries []string
	for _, x := range []struct {
		typ string
		id  int
	}{{"Insert", ed.tcIns}, {"Delete", ed.tcDel}} {
		if x.id != 0 {
			entries = append(entries, fmt.Sprintf(`<%s:trackChange type="%s" date="%s" authorID="%s" hide="0" id="%d"/>`, pfx, x.typ, date, ed.authorID, x.id))
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return atoi([]byte(tagAttr(entries[i], "id"))) < atoi([]byte(tagAttr(entries[j], "id")))
	})
	if h, err = addListItems(h, pfx, "trackChanges", "trackChange", entries, pfx+":trackChangeAuthors"); err != nil {
		return err
	}
	if ed.newAuth {
		au := fmt.Sprintf(`<%s:trackChangeAuthor name="%s" mark="1" id="%s"/>`, pfx, escAttr(ed.opt.Author), ed.authorID)
		if h, err = addListItems(h, pfx, "trackChangeAuthors", "trackChangeAuthor", []string{au}, ""); err != nil {
			return err
		}
	}
	ed.parts[headerName] = []byte(h)
	return nil
}

// addListItems appends items to the <pfx:list itemCnt="N"> element and
// sets itemCnt to the number of <pfx:item> children. A missing list is
// created before the element named by beforeTag if present, else at the
// end of refList.
func addListItems(h, pfx, list, item string, items []string, beforeTag string) (string, error) {
	open := regexp.MustCompile(`<` + pfx + `:` + list + `(\s[^>]*)?(/?)>`)
	loc := open.FindStringSubmatchIndex(h)
	itemRe := regexp.MustCompile(`<` + pfx + `:` + item + `\s`)
	closeTag := "</" + pfx + ":" + list + ">"
	if loc == nil {
		x := fmt.Sprintf(`<%s:%s itemCnt="%d">%s%s`, pfx, list, len(items), strings.Join(items, ""), closeTag)
		at := -1
		if beforeTag != "" {
			at = strings.Index(h, "<"+beforeTag)
		}
		if at < 0 {
			at = strings.Index(h, "</"+pfx+":refList>")
		}
		if at < 0 {
			return "", fmt.Errorf("no %s:refList", pfx)
		}
		return h[:at] + x + h[at:], nil
	}
	if loc[5] > loc[4] { // self-closing list
		x := fmt.Sprintf(`<%s:%s itemCnt="%d">%s%s`, pfx, list, len(items), strings.Join(items, ""), closeTag)
		return h[:loc[0]] + x + h[loc[1]:], nil
	}
	end := strings.Index(h[loc[1]:], closeTag)
	if end < 0 {
		return "", fmt.Errorf("unterminated %s:%s", pfx, list)
	}
	end += loc[1]
	body := h[loc[1]:end] + strings.Join(items, "")
	n := len(itemRe.FindAllStringIndex(body, -1))
	tag := h[loc[0]:loc[1]]
	cnt := regexp.MustCompile(`\sitemCnt="\d*"`)
	if cnt.MatchString(tag) {
		tag = cnt.ReplaceAllString(tag, fmt.Sprintf(` itemCnt="%d"`, n))
	} else {
		tag = strings.TrimSuffix(tag, ">") + fmt.Sprintf(` itemCnt="%d">`, n)
	}
	return h[:loc[0]] + tag + body + h[end:], nil
}

// updateManifest lists the new images in content.hpf.
func (ed *editor) updateManifest() error {
	hpf, err := ed.read(hpfName)
	if err != nil {
		return err
	}
	pfx, err := rootPrefix(hpf, nsOPF)
	if err != nil {
		return err
	}
	closeTag := "</" + pfx + ":manifest>"
	at := bytes.Index(hpf, []byte(closeTag))
	if at < 0 {
		return fmt.Errorf("no %s", closeTag)
	}
	var items strings.Builder
	for _, im := range ed.images {
		fmt.Fprintf(&items, `<%s:item id="%s" href="%s" media-type="image/png" isEmbeded="1"/>`, pfx, im.id, im.path)
	}
	out := append([]byte{}, hpf[:at]...)
	out = append(out, items.String()...)
	out = append(out, hpf[at:]...)
	ed.parts[hpfName] = out
	return nil
}

// write builds the new zip: mimetype first and stored, the other entries
// in their original order with their original compression method, and
// the new images at the end, stored. Unchanged entries are copied without
// recompressing.
func (ed *editor) write(zr *zip.Reader) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(f *zip.File, data []byte, method uint16) error {
		fh := f.FileHeader
		fh.Method = method
		// Keep the original MS-DOS time as is; a non-zero Modified would
		// rewrite it and add an extended timestamp field.
		fh.Extra = nil
		fh.Modified = time.Time{}
		fh.CRC32, fh.CompressedSize64, fh.UncompressedSize64 = 0, 0, 0
		fh.CompressedSize, fh.UncompressedSize = 0, 0
		w, err := zw.CreateHeader(&fh)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	copyRaw := func(f *zip.File) error {
		r, err := f.OpenRaw()
		if err != nil {
			return err
		}
		fh := f.FileHeader
		w, err := zw.CreateRaw(&fh)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, r)
		return err
	}
	order := []*zip.File{ed.files["mimetype"]}
	for _, f := range zr.File {
		if f.Name != "mimetype" {
			order = append(order, f)
		}
	}
	for _, f := range order {
		var err error
		switch data, edited := ed.parts[f.Name]; {
		case f.Name == "mimetype" && f.Method != zip.Store:
			var b []byte
			if b, err = ed.read(f.Name); err == nil {
				err = put(f, b, zip.Store)
			}
		case edited:
			err = put(f, data, f.Method)
		default:
			err = copyRaw(f)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	for _, im := range ed.images {
		mt := ed.files["mimetype"]
		fh := &zip.FileHeader{Name: im.path, Method: zip.Store, ModifiedTime: mt.ModifiedTime, ModifiedDate: mt.ModifiedDate}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(im.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
