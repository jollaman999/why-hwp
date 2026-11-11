package apply

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// section is one Contents/sectionN.xml with byte offsets of every
// addressable paragraph. Offsets index into raw, the original bytes;
// edits are collected as patches and applied in one pass at the end, so
// everything outside the patched ranges stays byte for byte.
type section struct {
	idx   int
	name  string
	raw   []byte
	pfx   string // prefix bound to the paragraph namespace, e.g. "hp"
	core  string // prefix bound to the core namespace, e.g. "hc"
	paras []*para
	byLoc map[string]*para
	cells map[string]*container
}

// container is the list of paragraphs that are siblings: a section body
// or the paragraph list of one table cell.
type container struct {
	loc   string // cell loc, "" for a section body
	paras []*para
}

// para is one hp:p with the parts of it that apply edits. Only the
// paragraph's own runs and text nodes are listed: text inside tables,
// memos, text boxes and other sub-lists belongs to other paragraphs or is
// not addressable.
type para struct {
	sec    *section
	loc    string
	cont   *container
	start  int // '<' of the start tag
	tagEnd int // just after the start tag
	end    int // just after the end tag
	paraPr string
	style  string
	ls     [2]int // own hp:linesegarray, {-1, -1} when absent
	runs   []run
	ts     []tslot

	hasSecPr, hasTable, hasMemo, hasPic bool

	edit *paraEdit
}

type run struct {
	start, tagEnd, closeStart, end int
	self                           bool
	charPr                         string
}

type tslot struct {
	start, tagEnd, contentEnd, end int
	self                           bool
	run                            int
}

func indexSection(idx int, name string, raw []byte) (*section, error) {
	s := &section{idx: idx, name: name, raw: raw, byLoc: map[string]*para{}, cells: map[string]*container{}}
	var err error
	if s.pfx, err = rootPrefix(raw, nsParagraph); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if s.core, err = rootPrefix(raw, nsCore); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	ix := &indexer{dec: xml.NewDecoder(bytes.NewReader(raw)), raw: raw, sec: s}
	body := &container{}
	n := 0
	for {
		before := ix.off()
		tok, err := ix.dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "p" {
			continue
		}
		if err := ix.para(fmt.Sprintf("s%d/p%d", idx, n), before, se, body); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		n++
	}
	for _, p := range s.paras {
		s.byLoc[p.loc] = p
	}
	return s, nil
}

// indexer walks section XML the same way the hwpx reader does, so the
// locations it assigns are the ones 'why-hwp text' prints.
type indexer struct {
	dec   *xml.Decoder
	raw   []byte
	sec   *section
	cells int
}

func (ix *indexer) off() int { return int(ix.dec.InputOffset()) }

// selfClosing reports whether the start tag that ends at off is "<.../>".
func (ix *indexer) selfClosing(off int) bool {
	return off >= 2 && ix.raw[off-2] == '/' && ix.raw[off-1] == '>'
}

func (ix *indexer) para(loc string, start int, se xml.StartElement, cont *container) error {
	p := &para{
		sec: ix.sec, loc: loc, cont: cont, start: start, tagEnd: ix.off(),
		paraPr: attr(se, "paraPrIDRef"), style: attr(se, "styleIDRef"), ls: [2]int{-1, -1},
	}
	cont.paras = append(cont.paras, p)
	ix.sec.paras = append(ix.sec.paras, p)
	tables := 0
	depth := 0
	cur := -1
	for {
		before := ix.off()
		tok, err := ix.dec.Token()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			skip := false
			switch t.Name.Local {
			case "run":
				if depth == 0 {
					r := run{start: before, tagEnd: ix.off(), charPr: attr(t, "charPrIDRef")}
					if ix.selfClosing(r.tagEnd) {
						r.self = true
						if err := ix.dec.Skip(); err != nil {
							return err
						}
						r.closeStart, r.end = r.tagEnd, r.tagEnd
						p.runs = append(p.runs, r)
						continue
					}
					p.runs = append(p.runs, r)
					cur = len(p.runs) - 1
				}
			case "t":
				ts := tslot{start: before, tagEnd: ix.off(), run: cur}
				ts.self = ix.selfClosing(ts.tagEnd)
				if err := ix.dec.Skip(); err != nil {
					return err
				}
				ts.end = ix.off()
				ts.contentEnd = ts.tagEnd
				if !ts.self {
					ts.contentEnd = ts.tagEnd + bytes.LastIndex(ix.raw[ts.tagEnd:ts.end], []byte("</"))
				}
				p.ts = append(p.ts, ts)
				continue
			case "tbl":
				p.hasTable = true
				if err := ix.table(fmt.Sprintf("%s/t%d", loc, tables)); err != nil {
					return err
				}
				tables++
				continue
			case "fieldBegin":
				if attr(t, "type") == "MEMO" {
					p.hasMemo = true
					skip = true
				}
			case "pic":
				p.hasPic = true
				skip = true
			case "secPr":
				p.hasSecPr = true
				skip = true
			case "drawText", "subList":
				skip = true
			case "linesegarray":
				if depth == 0 {
					if err := ix.dec.Skip(); err != nil {
						return err
					}
					p.ls = [2]int{before, ix.off()}
					continue
				}
			}
			if skip {
				if err := ix.dec.Skip(); err != nil {
					return err
				}
				continue
			}
			depth++
		case xml.EndElement:
			if depth == 0 {
				p.end = ix.off()
				return nil
			}
			depth--
			if depth == 0 && t.Name.Local == "run" && cur >= 0 {
				p.runs[cur].closeStart = before
				p.runs[cur].end = ix.off()
				cur = -1
			}
		}
	}
}

func (ix *indexer) table(loc string) error {
	row, col := -1, 0
	depth := 0
	for {
		tok, err := ix.dec.Token()
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
				if err := ix.cell(loc, row, col); err != nil {
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

// cell indexes one hp:tc. As in the reader, the grid address comes after
// the paragraphs, so they are first named under a placeholder prefix.
func (ix *indexer) cell(tblLoc string, row, col int) error {
	ix.cells++
	ph := fmt.Sprintf("\x00c%d", ix.cells)
	first := len(ix.sec.paras)
	cont := &container{loc: ph}
	paras := 0
	depth := 0
	for {
		before := ix.off()
		tok, err := ix.dec.Token()
		if err != nil {
			return unexpected(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				if err := ix.para(fmt.Sprintf("%s/p%d", ph, paras), before, t, cont); err != nil {
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
			for _, p := range ix.sec.paras[first:] {
				fix(&p.loc)
				if p.cont.loc != "" {
					fix(&p.cont.loc)
				}
			}
			cont.loc = loc
			ix.sec.cells[loc] = cont
			// Nested cells were registered under placeholder names.
			for k, c := range ix.sec.cells {
				if strings.HasPrefix(k, ph) {
					fix(&c.loc)
					delete(ix.sec.cells, k)
					ix.sec.cells[c.loc] = c
				}
			}
			return nil
		}
	}
}

const (
	nsParagraph = "http://www.hancom.co.kr/hwpml/2011/paragraph"
	nsCore      = "http://www.hancom.co.kr/hwpml/2011/core"
	nsHead      = "http://www.hancom.co.kr/hwpml/2011/head"
	nsOPF       = "http://www.idpf.org/2007/opf/"
)

// rootPrefix returns the prefix the document element binds to uri, so new
// XML uses the same prefixes as the rest of the part.
func rootPrefix(raw []byte, uri string) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.RawToken()
		if err != nil {
			return "", fmt.Errorf("no root element: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		for _, a := range se.Attr {
			if a.Name.Space == "xmlns" && a.Value == uri {
				return a.Name.Local, nil
			}
		}
		return "", fmt.Errorf("root element does not declare namespace %s", uri)
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
