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
		if !ok || se.Name.Local != "p" {
			continue
		}
		loc := fmt.Sprintf("s%d/p%d", sec, p)
		p++
		i := len(doc.Blocks)
		doc.Blocks = append(doc.Blocks, model.Block{Loc: loc, Kind: model.KindPara})
		text, err := w.para(loc)
		if err != nil {
			return err
		}
		doc.Blocks[i].Text = text
	}
}

// walker turns section XML into blocks. Each method is called right after
// the start tag of its element and returns after consuming the matching
// end tag.
type walker struct {
	dec   *xml.Decoder
	doc   *model.Document
	cells int // counter for unique placeholder locations
}

// para reads one hp:p and returns its own text. Tables found in it are
// added to the document as cell blocks under loc/tN.
func (w *walker) para(loc string) (string, error) {
	var sb strings.Builder
	tables := 0
	depth := 0
	afterBox := false
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return "", unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				var tb strings.Builder
				if err := w.text(&tb); err != nil {
					return "", err
				}
				if tb.Len() > 0 {
					if afterBox {
						sb.WriteByte('\n')
						afterBox = false
					}
					sb.WriteString(tb.String())
				}
				continue
			case "tbl":
				if err := w.table(fmt.Sprintf("%s/t%d", loc, tables), t); err != nil {
					return "", err
				}
				tables++
				continue
			case "fieldBegin":
				if attr(t, "type") == "MEMO" {
					text, err := w.flatten()
					if err != nil {
						return "", err
					}
					w.doc.Summary.Memos = append(w.doc.Summary.Memos, model.Memo{Loc: loc, Text: text})
					continue
				}
			case "pic":
				if err := w.pic(loc); err != nil {
					return "", err
				}
				continue
			case "drawText":
				text, err := w.flatten()
				if err != nil {
					return "", err
				}
				if text != "" {
					if sb.Len() > 0 {
						sb.WriteByte('\n')
					}
					sb.WriteString(text)
					afterBox = true
				}
				continue
			case "subList":
				// Header, footer, footnote, endnote, hidden comment and
				// caption bodies are not part of the paragraph text.
				if err := w.dec.Skip(); err != nil {
					return "", err
				}
				continue
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				return sb.String(), nil
			}
			depth--
		}
	}
}

// text appends the content of one hp:t.
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
			switch t.Name.Local {
			case "tab":
				sb.WriteByte('\t')
			case "lineBreak":
				sb.WriteByte('\n')
			case "nbSpace", "fwSpace":
				sb.WriteByte(' ')
			case "hyphen":
				sb.WriteByte('-')
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
func (w *walker) flatten() (string, error) {
	var lines []string
	var sb strings.Builder
	depth := 0
	inPara := 0
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return "", unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				if err := w.text(&sb); err != nil {
					return "", err
				}
				continue
			case "tbl":
				if err := w.dec.Skip(); err != nil {
					return "", err
				}
				continue
			case "p":
				inPara++
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				if sb.Len() > 0 {
					lines = append(lines, sb.String())
				}
				return strings.Join(lines, "\n"), nil
			}
			depth--
			if t.Name.Local == "p" && inPara > 0 {
				inPara--
				lines = append(lines, sb.String())
				sb.Reset()
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

// cell reads one hp:tc. The grid address (hp:cellAddr) comes after the
// cell's paragraphs, so nested content is first recorded under a
// placeholder location that is replaced once the address is known.
func (w *walker) cell(tblLoc string, row, col int) error {
	w.cells++
	ph := fmt.Sprintf("\x00c%d", w.cells)
	doc := w.doc
	bi := len(doc.Blocks)
	ti, mi, pi := len(doc.Summary.Tables), len(doc.Summary.Memos), len(doc.Summary.Pictures)
	doc.Blocks = append(doc.Blocks, model.Block{Kind: model.KindCell})

	var texts []string
	paras := 0
	depth := 0
	for {
		tok, err := w.dec.Token()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				text, err := w.para(fmt.Sprintf("%s/p%d", ph, paras))
				if err != nil {
					return err
				}
				paras++
				texts = append(texts, text)
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
			for i := bi + 1; i < len(doc.Blocks); i++ {
				fix(&doc.Blocks[i].Loc)
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
			doc.Blocks[bi].Loc = loc
			doc.Blocks[bi].Text = strings.Join(texts, "\n")
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
