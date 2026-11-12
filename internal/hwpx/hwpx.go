// Package hwpx reads HWPX (OWPML) documents into the model package's
// block list and structure summary.
//
// An HWPX file is a zip. Contents/content.hpf lists the parts, the spine
// gives the section order, Contents/header.xml holds the shape, style and
// change-tracking tables, and Contents/sectionN.xml holds the body. Section
// XML is parsed as a token stream so large documents are not loaded into a
// tree.
package hwpx

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jollaman999/why-hwp/internal/model"
)

// Read opens the HWPX file at name and parses it.
func Read(name string) (*model.Document, error) {
	zr, err := zip.OpenReader(name)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return Parse(&zr.Reader)
}

// Parse parses an HWPX package that is already open as a zip.
func Parse(zr *zip.Reader) (*model.Document, error) {
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}

	pkg, err := readPackage(files)
	if err != nil {
		return nil, err
	}

	doc := &model.Document{Format: "hwpx"}
	sum := &doc.Summary
	sum.Format = "hwpx"
	sum.Sections = len(pkg.sections)
	sum.BinData = pkg.binData

	if f := files["Contents/header.xml"]; f != nil {
		if err := readHeader(f, sum); err != nil {
			return nil, fmt.Errorf("Contents/header.xml: %w", err)
		}
	}

	for i, name := range pkg.sections {
		f := files[name]
		if f == nil {
			return nil, fmt.Errorf("%s: listed in content.hpf but missing", name)
		}
		if err := readSection(f, i, doc); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	for _, b := range doc.Blocks {
		if b.Kind == model.KindPara {
			sum.Paragraphs++
		}
	}
	sum.OwnerMarks = model.FindOwnerMarks(doc.Blocks)
	doc.Tracked = true
	return doc, nil
}

type pkgInfo struct {
	sections []string
	binData  []model.BinItem
}

// readPackage takes the section order and binary items from content.hpf.
// Without a manifest it falls back to the Contents/sectionN.xml entries.
func readPackage(files map[string]*zip.File) (*pkgInfo, error) {
	var pkg pkgInfo
	type item struct{ id, href, media string }
	var items []item
	var spine []string

	if f := files["Contents/content.hpf"]; f != nil {
		dec, closer, err := openXML(f)
		if err != nil {
			return nil, err
		}
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				closer.Close()
				return nil, fmt.Errorf("Contents/content.hpf: %w", err)
			}
			se, ok := tok.(xml.StartElement)
			if !ok {
				continue
			}
			switch se.Name.Local {
			case "item":
				items = append(items, item{attr(se, "id"), attr(se, "href"), attr(se, "media-type")})
			case "itemref":
				spine = append(spine, attr(se, "idref"))
			}
		}
		closer.Close()
	}

	byID := map[string]item{}
	for _, it := range items {
		byID[it.id] = it
	}
	for _, id := range spine {
		if it, ok := byID[id]; ok && isSection(it.href) {
			pkg.sections = append(pkg.sections, it.href)
		}
	}
	if len(pkg.sections) == 0 {
		for name := range files {
			if isSection(name) {
				pkg.sections = append(pkg.sections, name)
			}
		}
		sort.Slice(pkg.sections, func(i, j int) bool {
			return sectionNum(pkg.sections[i]) < sectionNum(pkg.sections[j])
		})
	}

	seen := map[string]bool{}
	for _, it := range items {
		if !strings.HasPrefix(it.href, "BinData/") {
			continue
		}
		bi := model.BinItem{ID: it.id, Path: it.href, MediaType: it.media}
		if f := files[it.href]; f != nil {
			bi.Size = int64(f.UncompressedSize64)
		}
		pkg.binData = append(pkg.binData, bi)
		seen[it.href] = true
	}
	var orphans []string
	for name := range files {
		if strings.HasPrefix(name, "BinData/") && !seen[name] && !strings.HasSuffix(name, "/") {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	for _, name := range orphans {
		pkg.binData = append(pkg.binData, model.BinItem{Path: name, Size: int64(files[name].UncompressedSize64)})
	}
	return &pkg, nil
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

func readHeader(f *zip.File, sum *model.Summary) error {
	dec, closer, err := openXML(f)
	if err != nil {
		return err
	}
	defer closer.Close()
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "paraPr":
			sum.ParaShapes++
		case "charPr":
			sum.CharShapes++
		case "trackChange":
			sum.TrackChanges++
		case "style":
			sum.Styles = append(sum.Styles, model.Style{
				ID:      attr(se, "id"),
				Type:    attr(se, "type"),
				Name:    attr(se, "name"),
				EngName: attr(se, "engName"),
			})
		}
	}
}

func readSection(f *zip.File, sec int, doc *model.Document) error {
	dec, closer, err := openXML(f)
	if err != nil {
		return err
	}
	defer closer.Close()
	w := &walker{dec: dec, doc: doc}
	p := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if w.marker(se) {
			if err := dec.Skip(); err != nil {
				return err
			}
			continue
		}
		if se.Name.Local != "p" {
			continue
		}
		loc := fmt.Sprintf("s%d/p%d", sec, p)
		p++
		if err := w.block(loc, model.KindPara); err != nil {
			return err
		}
	}
}

// block reads one hp:p into a block of its own at loc. The cell blocks of
// its tables follow it, then a text-box block when it anchors text boxes.
func (w *walker) block(loc string, kind model.Kind) error {
	doc := w.doc
	i := len(doc.Blocks)
	doc.Blocks = append(doc.Blocks, model.Block{Loc: loc, Kind: kind})
	pt, err := w.para(loc)
	if err != nil {
		return err
	}
	b := &doc.Blocks[i]
	b.Text = pt.spans.Text(model.ViewAccepted)
	if pt.spans.Changed() {
		b.Spans = pt.spans
	}
	b.Break, b.Whole = pt.brk, pt.whole
	if pt.box.Len() > 0 {
		box := model.Block{Loc: loc + model.BoxSuffix, Kind: model.KindBox, Text: pt.box.Text(model.ViewAccepted)}
		if pt.box.Changed() {
			box.Spans = pt.box
		}
		doc.Blocks = append(doc.Blocks, box)
	}
	return nil
}

// walker turns section XML into blocks. Each method is called right after
// the start tag of its element and returns after consuming the matching
// end tag.
//
// Tracked changes are marked by empty hp:insertBegin / hp:insertEnd and
// hp:deleteBegin / hp:deleteEnd elements, which may span hp:t, run and
// paragraph boundaries. An end marker with paraend="1" takes the break of
// the paragraph it is in into the change.
type walker struct {
	dec   *xml.Decoder
	doc   *model.Document
	cells int // counter for unique placeholder locations

	ins, del int          // open insertion and deletion ranges
	brk      model.Change // changes that took the current paragraph's break
	cover    model.Change // changes that cover all content seen in the current paragraph
}

// paraText is the text of one hp:p.
type paraText struct {
	spans model.Spans
	box   model.Spans  // text of the text boxes anchored in it, one line per paragraph
	brk   model.Change // change that covers the paragraph break
	whole model.Change // change that covers the whole paragraph, break included
}

// change returns the change in effect at the current position.
func (w *walker) change() model.Change {
	var c model.Change
	if w.ins > 0 {
		c |= model.Inserted
	}
	if w.del > 0 {
		c |= model.Deleted
	}
	return c
}

// marker reports whether se is a change-tracking marker and, if so,
// opens or closes its range.
func (w *walker) marker(se xml.StartElement) bool {
	var open *int
	var c model.Change
	switch se.Name.Local {
	case "insertBegin":
		w.ins++
		return true
	case "deleteBegin":
		w.del++
		return true
	case "insertEnd":
		open, c = &w.ins, model.Inserted
	case "deleteEnd":
		open, c = &w.del, model.Deleted
	default:
		return false
	}
	if *open > 0 {
		*open--
	}
	if attr(se, "paraend") == "1" {
		w.brk |= c
	}
	return true
}

// content notes one piece of paragraph content (a character or an
// object) and returns its change.
func (w *walker) content() model.Change {
	c := w.change()
	w.cover &= c
	return c
}

// para reads one hp:p and returns its own text and, apart from it, the
// text of its text boxes. Tables found in it are added to the document as
// cell blocks under loc/tN.
func (w *walker) para(loc string) (paraText, error) {
	saveBrk, saveCover := w.brk, w.cover
	w.brk, w.cover = 0, model.Inserted|model.Deleted
	defer func() { w.brk, w.cover = saveBrk, saveCover }()

	var sp, box model.Spans
	tables := 0
	depth := 0
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return paraText{}, unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if w.marker(t) {
				if err := w.dec.Skip(); err != nil {
					return paraText{}, err
				}
				continue
			}
			if depth == 1 && isObject(t.Name.Local) {
				w.content()
			}
			switch t.Name.Local {
			case "t":
				if err := w.text(&sp); err != nil {
					return paraText{}, err
				}
				continue
			case "tbl":
				if err := w.table(fmt.Sprintf("%s/t%d", loc, tables), t); err != nil {
					return paraText{}, err
				}
				tables++
				continue
			case "fieldBegin":
				if attr(t, "type") == "MEMO" {
					cover := w.cover
					text, err := w.flatten()
					w.cover = cover
					if err != nil {
						return paraText{}, err
					}
					w.doc.Summary.Memos = append(w.doc.Summary.Memos, model.Memo{Loc: loc, Text: text.Text(model.ViewAccepted)})
					continue
				}
			case "pic":
				if err := w.pic(loc); err != nil {
					return paraText{}, err
				}
				continue
			case "drawText":
				c := w.change()
				text, err := w.flatten()
				if err != nil {
					return paraText{}, err
				}
				if text.Len() > 0 {
					if box.Len() > 0 {
						box.Add("\n", c)
					}
					box.Append(text)
				}
				continue
			case "subList":
				// Header, footer, footnote, endnote, hidden comment and
				// caption bodies are not part of the paragraph text.
				if err := w.dec.Skip(); err != nil {
					return paraText{}, err
				}
				continue
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				brk := w.brk | w.change()
				return paraText{spans: sp, box: box, brk: brk, whole: w.cover & brk}, nil
			}
			depth--
		}
	}
}

// isObject reports whether a run child is content (text, a table, a
// picture, a shape) rather than a control or settings element.
func isObject(local string) bool {
	switch local {
	case "t", "ctrl", "secPr":
		return false
	}
	return true
}

// text appends the content of one hp:t.
func (w *walker) text(sp *model.Spans) error {
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			if len(t) > 0 {
				sp.Add(string(t), w.content())
			}
		case xml.StartElement:
			if !w.marker(t) {
				switch t.Name.Local {
				case "tab":
					sp.Add("\t", w.content())
				case "lineBreak":
					sp.Add("\n", w.content())
				case "nbSpace", "fwSpace":
					sp.Add(" ", w.content())
				case "hyphen":
					sp.Add("-", w.content())
				}
			}
			if err := w.dec.Skip(); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}

// flatten returns the text of every hp:t inside the current element, one
// line per paragraph, ignoring tables and nested lists.
func (w *walker) flatten() (model.Spans, error) {
	saveBrk := w.brk
	defer func() { w.brk = saveBrk }()
	var out, line model.Spans
	lines := 0
	addLine := func(brk model.Change) {
		if lines > 0 {
			out.Add("\n", brk)
		}
		out.Append(line)
		line = nil
		lines++
	}
	var brk model.Change
	depth := 0
	inPara := 0
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return nil, unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if w.marker(t) {
				if err := w.dec.Skip(); err != nil {
					return nil, err
				}
				continue
			}
			switch t.Name.Local {
			case "t":
				if err := w.text(&line); err != nil {
					return nil, err
				}
				continue
			case "tbl":
				if err := w.dec.Skip(); err != nil {
					return nil, err
				}
				continue
			case "p":
				inPara++
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				if line.Len() > 0 {
					addLine(brk)
				}
				return out, nil
			}
			depth--
			if t.Name.Local == "p" && inPara > 0 {
				inPara--
				addLine(brk)
				brk = w.brk | w.change()
				w.brk = 0
			}
		}
	}
}

func (w *walker) pic(loc string) error {
	ref := ""
	depth := 0
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "img" && ref == "" {
				ref = attr(t, "binaryItemIDRef")
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				w.doc.Summary.Pictures = append(w.doc.Summary.Pictures, model.Picture{Loc: loc, Ref: ref})
				return nil
			}
			depth--
		}
	}
}

func (w *walker) table(loc string, start xml.StartElement) error {
	rows, _ := strconv.Atoi(attr(start, "rowCnt"))
	cols, _ := strconv.Atoi(attr(start, "colCnt"))
	w.doc.Summary.Tables = append(w.doc.Summary.Tables, model.Table{Loc: loc, Rows: rows, Cols: cols})
	row, col := -1, 0
	depth := 0
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tr":
				row++
				col = 0
			case "tc":
				if err := w.cell(loc, row, col); err != nil {
					return err
				}
				col++
				continue
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				return nil
			}
			depth--
		}
	}
}

// cell reads one hp:tc. A cell with one paragraph is one block at the
// cell's location; a cell with more is one block per paragraph at
// loc/pN. The grid address (hp:cellAddr) comes after the cell's
// paragraphs, so everything is first recorded under a placeholder
// location that is replaced once the address is known.
func (w *walker) cell(tblLoc string, row, col int) error {
	w.cells++
	ph := fmt.Sprintf("\x00c%d", w.cells)
	doc := w.doc
	bi := len(doc.Blocks)
	ti, mi, pi := len(doc.Summary.Tables), len(doc.Summary.Memos), len(doc.Summary.Pictures)

	paras := 0
	depth := 0
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if w.marker(t) {
				if err := w.dec.Skip(); err != nil {
					return err
				}
				continue
			}
			switch t.Name.Local {
			case "p":
				if err := w.block(fmt.Sprintf("%s/p%d", ph, paras), model.KindCell); err != nil {
					return err
				}
				paras++
				continue
			case "cellAddr":
				if v, err := strconv.Atoi(attr(t, "rowAddr")); err == nil {
					row = v
				}
				if v, err := strconv.Atoi(attr(t, "colAddr")); err == nil {
					col = v
				}
			}
			depth++
		case xml.EndElement:
			if depth > 0 {
				depth--
				continue
			}
			loc := fmt.Sprintf("%s/r%d/c%d", tblLoc, row, col)
			fix := func(s *string) {
				if strings.HasPrefix(*s, ph) {
					*s = loc + strings.TrimPrefix(*s, ph)
				}
			}
			only := ph + "/p0"
			for i := bi; i < len(doc.Blocks); i++ {
				b := &doc.Blocks[i]
				if paras == 1 && (b.Loc == only || b.Loc == only+model.BoxSuffix) {
					b.Loc = ph + strings.TrimPrefix(b.Loc, only)
				}
				fix(&b.Loc)
			}
			for i := ti; i < len(doc.Summary.Tables); i++ {
				fix(&doc.Summary.Tables[i].Loc)
			}
			for i := mi; i < len(doc.Summary.Memos); i++ {
				fix(&doc.Summary.Memos[i].Loc)
			}
			for i := pi; i < len(doc.Summary.Pictures); i++ {
				fix(&doc.Summary.Pictures[i].Loc)
			}
			return nil
		}
	}
}

func openXML(f *zip.File) (*xml.Decoder, io.Closer, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, nil, err
	}
	return xml.NewDecoder(rc), rc, nil
}

func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func unexpected(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}
