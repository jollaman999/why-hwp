package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// word is one word of a PDF as pdftotext -bbox reports it. Page is 0-based
// and the box is in PDF points with y growing downwards.
type word struct {
	Text           string
	Page           int
	X0, Y0, X1, Y1 float64
}

func (w word) ymid() float64 { return (w.Y0 + w.Y1) / 2 }

type pageSize struct{ W, H float64 }

type pdfWords struct {
	Pages []pageSize
	Words []word
	// Base is parallel to Words and is set by attachBaselines, which is run
	// only for a document that has Type 3 fonts. It is nil otherwise.
	Base []wordBase
	// TwoUp tells that the pages are the halves of sheets printed two
	// pages to a sheet (see halves): pages 2n and 2n+1 are the left and
	// the right half of sheet n.
	TwoUp bool
}

// wordBase is the baseline of a word. OK is false when the word's first
// glyph was not found; Type3 tells that the word holds a glyph of a Type 3
// font.
type wordBase struct {
	Y     float64
	OK    bool
	Type3 bool
}

// base returns the wordBase of word i, the zero value when baselines were
// not attached.
func (d *pdfWords) base(i int) wordBase {
	if i < len(d.Base) {
		return d.Base[i]
	}
	return wordBase{}
}

// readPDFWords runs pdftotext -bbox on a PDF file.
func readPDFWords(path string) (*pdfWords, error) {
	c := exec.Command("pdftotext", "-bbox", path, "-")
	var out, errb bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("pdftotext %s: %w: %s", path, err, strings.TrimSpace(errb.String()))
	}
	return parseBBox(&out)
}

// parseBBox parses the XHTML that pdftotext -bbox writes.
func parseBBox(r io.Reader) (*pdfWords, error) {
	d := xml.NewDecoder(r)
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	p := &pdfWords{}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("pdftotext output: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "page":
			w, err1 := attrFloat(se, "width")
			h, err2 := attrFloat(se, "height")
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("pdftotext output: bad page size")
			}
			p.Pages = append(p.Pages, pageSize{w, h})
		case "word":
			if len(p.Pages) == 0 {
				return nil, fmt.Errorf("pdftotext output: word outside a page")
			}
			var w word
			var errs [4]error
			w.X0, errs[0] = attrFloat(se, "xMin")
			w.Y0, errs[1] = attrFloat(se, "yMin")
			w.X1, errs[2] = attrFloat(se, "xMax")
			w.Y1, errs[3] = attrFloat(se, "yMax")
			for _, e := range errs {
				if e != nil {
					return nil, fmt.Errorf("pdftotext output: %w", e)
				}
			}
			var text string
			if err := d.DecodeElement(&text, &se); err != nil {
				return nil, fmt.Errorf("pdftotext output: %w", err)
			}
			w.Text = text
			w.Page = len(p.Pages) - 1
			p.Words = append(p.Words, w)
		}
	}
	return p, nil
}

func attrFloat(se xml.StartElement, name string) (float64, error) {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return strconv.ParseFloat(a.Value, 64)
		}
	}
	return 0, fmt.Errorf("<%s> has no %s", se.Name.Local, name)
}
