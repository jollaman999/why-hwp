// Package docx reads Word (OOXML) documents into the model package's block
// list and structure summary.
//
// Only w:t and w:delText text is collected. Two things trip up naive extractors and are
// handled here: drawing anchors carry numbers in wp:posOffset that are not
// text, and mc:AlternateContent repeats the same content once per
// mc:Choice and again in mc:Fallback. The first mc:Choice is read and the
// rest are skipped.
//
// Tracked changes are read into the model's spans: text in w:ins and
// w:moveTo is inserted, text in w:del and w:moveFrom (w:delText) is
// deleted, and w:ins or w:del in a paragraph's w:pPr/w:rPr marks its
// paragraph mark. Block.Text is the accepted text, without deleted text.
package docx

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

const nsW = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// Read opens the docx file at name and parses it.
func Read(name string) (*model.Document, error) {
	zr, err := zip.OpenReader(name)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return Parse(&zr.Reader)
}

// Parse parses a docx package that is already open as a zip.
func Parse(zr *zip.Reader) (*model.Document, error) {
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	main := mainPart(files)
	f := files[main]
	if f == nil {
		return nil, fmt.Errorf("%s: missing", main)
	}

	doc := &model.Document{Format: "docx"}
	sum := &doc.Summary
	sum.Format = "docx"

	rels := readRels(files, path.Join(path.Dir(main), "_rels", path.Base(main)+".rels"))
	w := &walker{doc: doc, commentLoc: map[string]string{}}
	if err := w.readBody(f); err != nil {
		return nil, fmt.Errorf("%s: %w", main, err)
	}

	if f := files[path.Join(path.Dir(main), "styles.xml")]; f != nil {
		if err := readStyles(f, sum); err != nil {
			return nil, fmt.Errorf("styles.xml: %w", err)
		}
	}
	if f := files[path.Join(path.Dir(main), "comments.xml")]; f != nil {
		if err := w.readComments(f); err != nil {
			return nil, fmt.Errorf("comments.xml: %w", err)
		}
	}

	byTarget := map[string]string{}
	for id, target := range rels {
		byTarget[path.Clean(path.Join(path.Dir(main), target))] = id
	}
	var media []string
	for name := range files {
		if strings.HasPrefix(name, path.Join(path.Dir(main), "media")+"/") && !strings.HasSuffix(name, "/") {
			media = append(media, name)
		}
	}
	sort.Strings(media)
	for _, name := range media {
		sum.BinData = append(sum.BinData, model.BinItem{ID: byTarget[name], Path: name, Size: int64(files[name].UncompressedSize64)})
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

// mainPart finds the main document part through _rels/.rels.
func mainPart(files map[string]*zip.File) string {
	for _, target := range readRelsTyped(files, "_rels/.rels", "/officeDocument") {
		return strings.TrimPrefix(target, "/")
	}
	return "word/document.xml"
}

func readRels(files map[string]*zip.File, name string) map[string]string {
	return readRelsTyped(files, name, "")
}

// readRelsTyped returns relationship id -> target, keeping only types that
// end with typeSuffix when it is not empty.
func readRelsTyped(files map[string]*zip.File, name, typeSuffix string) map[string]string {
	out := map[string]string{}
	f := files[name]
	if f == nil {
		return out
	}
	rc, err := f.Open()
	if err != nil {
		return out
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Relationship" {
			continue
		}
		if attr(se, "TargetMode") == "External" {
			continue
		}
		if typeSuffix != "" && !strings.HasSuffix(attr(se, "Type"), typeSuffix) {
			continue
		}
		out[attr(se, "Id")] = attr(se, "Target")
	}
}

func readStyles(f *zip.File, sum *model.Summary) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	cur := -1
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "style":
				sum.Styles = append(sum.Styles, model.Style{ID: attr(t, "styleId"), Type: attr(t, "type")})
				cur = len(sum.Styles) - 1
			case "name":
				if cur >= 0 {
					sum.Styles[cur].Name = attr(t, "val")
				}
			}
		case xml.EndElement:
			if t.Name.Local == "style" {
				cur = -1
			}
		}
	}
}

type walker struct {
	dec        *xml.Decoder
	doc        *model.Document
	commentLoc map[string]string // comment id -> loc of its anchor
	boxEnded   bool              // a text-box paragraph was just appended

	ins, del int          // open w:ins (w:moveTo) and w:del (w:moveFrom) elements
	rPr      int          // open w:rPr elements
	mark     model.Change // change of the current paragraph's mark (w:pPr/w:rPr/w:ins or w:del)
	cover    model.Change // changes that cover all content seen in the current paragraph
}

// next returns the next token, counts tracked insertions and deletions and
// follows which of them the position is in.
func (w *walker) next() (xml.Token, error) {
	tok, err := w.dec.Token()
	if err != nil {
		return nil, err
	}
	w.track(tok)
	if se, ok := tok.(xml.StartElement); ok && se.Name.Space == nsW {
		switch se.Name.Local {
		case "ins", "del":
			w.doc.Summary.TrackChanges++
		case "sectPr":
			w.doc.Summary.Sections++
		}
	}
	return tok, nil
}

// track updates the open change ranges for tok. A w:ins or w:del inside
// w:rPr (only paragraph mark properties have them) marks the paragraph
// mark instead.
func (w *walker) track(tok xml.Token) {
	var name xml.Name
	start := false
	switch t := tok.(type) {
	case xml.StartElement:
		name, start = t.Name, true
	case xml.EndElement:
		name = t.Name
	default:
		return
	}
	if name.Space != nsW {
		return
	}
	step := -1
	if start {
		step = 1
	}
	var open *int
	var c model.Change
	switch name.Local {
	case "rPr":
		w.rPr += step
		return
	case "ins", "moveTo":
		open, c = &w.ins, model.Inserted
	case "del", "moveFrom":
		open, c = &w.del, model.Deleted
	default:
		return
	}
	if w.rPr > 0 {
		if start {
			w.mark |= c
		}
		return
	}
	*open = max(*open+step, 0)
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

// piece notes one piece of paragraph content and returns its change.
func (w *walker) piece() model.Change {
	c := w.change()
	w.cover &= c
	return c
}

// paraText is the text of one w:p.
type paraText struct {
	spans model.Spans
	brk   model.Change // change of the paragraph mark
	whole model.Change // change that covers the whole paragraph, mark included
}

// paragraph reads one w:p.
func (w *walker) paragraph(loc string) (paraText, error) {
	saveMark, saveCover := w.mark, w.cover
	w.mark, w.cover = 0, model.Inserted|model.Deleted
	defer func() { w.mark, w.cover = saveMark, saveCover }()
	var sp model.Spans
	w.boxEnded = false
	if err := w.content(&sp, loc); err != nil {
		return paraText{}, err
	}
	return paraText{spans: sp, brk: w.mark, whole: w.cover & w.mark}, nil
}

func (w *walker) readBody(f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	w.dec = xml.NewDecoder(rc)
	p := 0
	for {
		tok, err := w.next()
		if err == io.EOF {
			if w.doc.Summary.Sections == 0 {
				w.doc.Summary.Sections = 1
			}
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Space != nsW {
			continue
		}
		switch se.Name.Local {
		case "p":
			loc := fmt.Sprintf("s0/p%d", p)
			p++
			pt, err := w.paragraph(loc)
			if err != nil {
				return err
			}
			b := model.Block{Loc: loc, Kind: model.KindPara, Text: pt.spans.Text(model.ViewAccepted), Break: pt.brk, Whole: pt.whole}
			if pt.spans.Changed() {
				b.Spans = pt.spans
			}
			w.doc.Blocks = append(w.doc.Blocks, b)
		case "tbl":
			loc := fmt.Sprintf("s0/p%d/t0", p)
			p++
			if err := w.table(loc); err != nil {
				return err
			}
		}
	}
}

// content appends the text inside the current element (a paragraph, a
// mc:Choice, a text box) to sp and returns after its end tag. Paragraphs
// nested in it (text boxes) are appended as new lines.
func (w *walker) content(sp *model.Spans, loc string) error {
	depth := 0
	for {
		tok, err := w.next()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == nsW {
				switch t.Name.Local {
				case "t", "delText":
					var tb strings.Builder
					if err := w.text(&tb); err != nil {
						return err
					}
					if tb.Len() > 0 {
						c := w.piece()
						if w.boxEnded {
							sp.Add("\n", c)
							w.boxEnded = false
						}
						sp.Add(tb.String(), c)
					}
					continue
				case "tabs":
					// Tab stop definitions in w:pPr, not tab characters.
					if err := w.dec.Skip(); err != nil {
						return err
					}
					continue
				case "tab":
					sp.Add("\t", w.piece())
				case "br", "cr":
					sp.Add("\n", w.piece())
				case "noBreakHyphen":
					sp.Add("-", w.piece())
				case "drawing", "pict", "object":
					w.piece()
				case "commentReference":
					w.commentLoc[attr(t, "id")] = loc
				case "p":
					c := w.change()
					var inner model.Spans
					w.boxEnded = false
					mark := w.mark
					err := w.content(&inner, loc)
					w.mark = mark
					if err != nil {
						return err
					}
					if inner.Len() > 0 {
						if sp.Len() > 0 {
							sp.Add("\n", c)
						}
						sp.Append(inner)
						w.boxEnded = true
					}
					continue
				case "tbl":
					if err := w.dec.Skip(); err != nil {
						return err
					}
					continue
				}
			}
			switch t.Name.Local {
			case "AlternateContent":
				if err := w.alternate(sp, loc); err != nil {
					return err
				}
				continue
			case "blip":
				w.doc.Summary.Pictures = append(w.doc.Summary.Pictures, model.Picture{Loc: loc, Ref: attr(t, "embed")})
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

// alternate reads the first mc:Choice of an mc:AlternateContent and skips
// the other choices and the fallback, which repeat the same content.
func (w *walker) alternate(sp *model.Spans, loc string) error {
	done := false
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "Choice" && !done {
				done = true
				if err := w.content(sp, loc); err != nil {
					return err
				}
				continue
			}
			if err := w.dec.Skip(); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}

func (w *walker) text(sb *strings.Builder) error {
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			sb.Write(t)
		case xml.StartElement:
			if err := w.dec.Skip(); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}

func (w *walker) table(loc string) error {
	ti := len(w.doc.Summary.Tables)
	w.doc.Summary.Tables = append(w.doc.Summary.Tables, model.Table{Loc: loc})
	row, col := -1, 0
	gridCols, maxCol := 0, 0
	depth := 0
	for {
		tok, err := w.next()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == nsW {
				switch t.Name.Local {
				case "gridCol":
					gridCols++
				case "tr":
					row++
					col = 0
				case "gridBefore":
					if v, err := strconv.Atoi(attr(t, "val")); err == nil {
						col += v
					}
				case "tc":
					span, err := w.cell(loc, row, col)
					if err != nil {
						return err
					}
					col += span
					maxCol = max(maxCol, col)
					continue
				}
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				w.doc.Summary.Tables[ti].Rows = row + 1
				w.doc.Summary.Tables[ti].Cols = maxCol
				if gridCols > 0 {
					w.doc.Summary.Tables[ti].Cols = gridCols
				}
				return nil
			}
			depth--
		}
	}
}

// cell reads one w:tc and returns how many grid columns it spans.
func (w *walker) cell(tblLoc string, row, col int) (int, error) {
	loc := fmt.Sprintf("%s/r%d/c%d", tblLoc, row, col)
	bi := len(w.doc.Blocks)
	w.doc.Blocks = append(w.doc.Blocks, model.Block{Loc: loc, Kind: model.KindCell})
	span := 1
	var texts []paraText
	p := 0
	depth := 0
	for {
		tok, err := w.next()
		if err != nil {
			return 0, unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == nsW {
				switch t.Name.Local {
				case "gridSpan":
					if v, err := strconv.Atoi(attr(t, "val")); err == nil && v > 0 {
						span = v
					}
				case "p":
					pt, err := w.paragraph(fmt.Sprintf("%s/p%d", loc, p))
					if err != nil {
						return 0, err
					}
					p++
					texts = append(texts, pt)
					continue
				case "tbl":
					if err := w.table(fmt.Sprintf("%s/p%d/t0", loc, p)); err != nil {
						return 0, err
					}
					p++
					continue
				}
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				var sp model.Spans
				for i, pt := range texts {
					if i > 0 {
						sp.Add("\n", texts[i-1].brk)
					}
					sp.Append(pt.spans)
				}
				w.doc.Blocks[bi].Text = sp.Text(model.ViewAccepted)
				if sp.Changed() {
					w.doc.Blocks[bi].Spans = sp
				}
				return span, nil
			}
			depth--
		}
	}
}

func (w *walker) readComments(f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	cw := &walker{dec: dec, doc: &model.Document{}, commentLoc: map[string]string{}}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Space != nsW || se.Name.Local != "comment" {
			continue
		}
		var sp model.Spans
		if err := cw.content(&sp, ""); err != nil {
			return err
		}
		w.doc.Summary.Memos = append(w.doc.Summary.Memos, model.Memo{Loc: w.commentLoc[attr(se, "id")], Text: sp.Text(model.ViewAccepted)})
	}
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
