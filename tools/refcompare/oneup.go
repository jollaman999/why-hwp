package main

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
)

// A document whose print setting is two pages to a sheet has a second
// reference, <name>.oneup.pdf: the same document printed by Hancom Office
// one page to a sheet. Hancom Office's two-up print can leave out parts of
// a page (the right columns of a wide table on a page it shrank for memo
// balloons), so a candidate that draws the page in full cannot match it.
// The words of such a document are compared with the one-up reference, and
// the two-up reference tells only how many sheets there are and how large.

const (
	// settingsEntry is the entry of an HWPX file that holds the print
	// settings.
	settingsEntry = "settings.xml"
	// printTwoUp and printOneUp are the values of the PrintMethod print
	// setting for two pages to a sheet and for one.
	printTwoUp = "4"
	printOneUp = "0"
	// oneUpSuffix follows the document name in the name of the one-up
	// reference.
	oneUpSuffix = ".oneup.pdf"
)

// printMethodItem matches the PrintMethod item of settings.xml; the first
// group is its value.
var printMethodItem = regexp.MustCompile(`<config:config-item\b[^<>]*\bname="PrintMethod"[^<>]*>\s*(-?[0-9]+)\s*</config:config-item>`)

// printMethod returns the value of the PrintMethod print setting of an HWPX
// file, and "" when the file has no settings.xml or no such setting in it.
func printMethod(hwpx string) (string, error) {
	zr, err := zip.OpenReader(hwpx)
	if err != nil {
		return "", fmt.Errorf("%s: %w", hwpx, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != settingsEntry {
			continue
		}
		b, err := readEntry(f)
		if err != nil {
			return "", fmt.Errorf("%s: %s: %w", hwpx, settingsEntry, err)
		}
		m := printMethodItem.FindAllSubmatch(b, -1)
		if len(m) == 0 {
			return "", nil
		}
		if len(m) > 1 {
			return "", fmt.Errorf("%s: %s has %d PrintMethod items", hwpx, settingsEntry, len(m))
		}
		return string(m[0][1]), nil
	}
	return "", nil
}

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// writeOneUp copies the HWPX file src to dst with the PrintMethod print
// setting changed from two pages to a sheet to one. Nothing else changes:
// every other entry is copied as it is stored, in the same order, and
// settings.xml keeps its place and its compression method. Hancom Office
// wants the mimetype entry first and not compressed.
func writeOneUp(src, dst string) (err error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	defer zr.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	zw := zip.NewWriter(out)
	changed := false
	for _, f := range zr.File {
		if f.Name != settingsEntry {
			if err := zw.Copy(f); err != nil {
				return fmt.Errorf("%s: %s: %w", src, f.Name, err)
			}
			continue
		}
		b, err := readEntry(f)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", src, f.Name, err)
		}
		m := printMethodItem.FindAllSubmatchIndex(b, -1)
		if len(m) != 1 || string(b[m[0][2]:m[0][3]]) != printTwoUp {
			return fmt.Errorf("%s: %s: want one PrintMethod item with the value %s", src, f.Name, printTwoUp)
		}
		edited := append(append(append([]byte(nil), b[:m[0][2]]...), printOneUp...), b[m[0][3]:]...)
		var packed bytes.Buffer
		switch f.Method {
		case zip.Store:
			packed.Write(edited)
		case zip.Deflate:
			fw, err := flate.NewWriter(&packed, flate.DefaultCompression)
			if err != nil {
				return err
			}
			if _, err := fw.Write(edited); err != nil {
				return err
			}
			if err := fw.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: %s: compression method %d is not supported", src, f.Name, f.Method)
		}
		// The sizes and the checksum go into the entry's own header,
		// as Hancom Office writes them, and not behind its data.
		h := f.FileHeader
		h.Flags &^= 0x8
		h.CRC32 = crc32.ChecksumIEEE(edited)
		h.CompressedSize64 = uint64(packed.Len())
		h.UncompressedSize64 = uint64(len(edited))
		w, err := zw.CreateRaw(&h)
		if err != nil {
			return err
		}
		if _, err := w.Write(packed.Bytes()); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return fmt.Errorf("%s: no %s", src, settingsEntry)
	}
	return zw.Close()
}

// oneUpRef returns the one-up reference of a document, or "" when there is
// none. A one-up reference is used only when manifest.json of refDir
// records it for the same input as the reference PDF: one left behind by a
// document that has since changed its print setting is from another input.
func oneUpRef(refDir, name string) (string, error) {
	p := filepath.Join(refDir, name+oneUpSuffix)
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	man, err := loadManifest(refDir)
	if err != nil {
		return "", err
	}
	if sum := man.Outputs[name+oneUpSuffix]; sum == "" || sum != man.Outputs[name+".pdf"] {
		return "", nil
	}
	return p, nil
}

// oneUp tells how a document was compared with its one-up reference, and
// how the candidate's sheets compare with those of the two-up reference.
type oneUp struct {
	// Halved tells that the candidate prints two pages to a sheet (see
	// sideBySide) and that the halves of its sheets were compared with
	// the pages of the one-up reference. Without it the candidate was
	// compared page by page.
	Halved bool
	// RefSheets and CandSheets count the sheets of the two-up reference
	// and of the candidate.
	RefSheets, CandSheets int
	// SizeDiff is the largest difference, in points, between the width
	// or the height of a candidate sheet and of the two-up reference
	// sheet of the same number. SizeSheet is that sheet (1-based), and
	// RefSize and CandSize are its two sizes.
	SizeDiff          float64
	SizeSheet         int
	RefSize, CandSize pageSize
	// Trimmed tells that the right half of the candidate's last sheet
	// has no word and was not taken as a page, because the reference
	// has one page less than the candidate has halves. HalfErr tells
	// why that half is not known to be empty; it is "" when it is.
	Trimmed bool
	HalfErr string
}

// sameSheets reports whether the candidate has the sheets of the two-up
// reference: as many, each of the same size to within tol points, and no
// half that is left out with something drawn on it.
func (u *oneUp) sameSheets(tol float64) bool {
	return u.RefSheets == u.CandSheets && u.SizeDiff <= tol && u.HalfErr == ""
}

// compareOneUp compares cand with ref, the one-up reference of a document
// whose two-up reference is sheets. A candidate that prints two pages to a
// sheet (see sideBySide) is cut into halves (see halves), and half n is
// compared with page n of ref as comparePDF compares two pages: with a fit
// of its own, which takes up what the candidate shrank the page by to put
// two on a sheet and what Hancom Office shrank the reference page by for
// memo balloons. Any other candidate is compared with ref page by page.
// The result has OneUp set; its pages are those of ref.
func compareOneUp(sheets, ref, cand *pdfWords, o cmpOptions) pdfResult {
	u := &oneUp{RefSheets: len(sheets.Pages), CandSheets: len(cand.Pages)}
	for i := 0; i < min(len(sheets.Pages), len(cand.Pages)); i++ {
		rs, cs := sheets.Pages[i], cand.Pages[i]
		if d := math.Max(math.Abs(rs.W-cs.W), math.Abs(rs.H-cs.H)); d > u.SizeDiff {
			u.SizeDiff, u.SizeSheet, u.RefSize, u.CandSize = d, i+1, rs, cs
		}
	}
	c := cand
	if sideBySide(cand) {
		c = halves(cand)
		u.Halved = true
		// A document with an odd number of pages leaves the right
		// half of its last sheet empty.
		if n := len(c.Pages); n == len(ref.Pages)+1 && !hasWords(c, n-1) {
			c.Pages = c.Pages[:n-1]
			u.Trimmed = true
		}
	}
	r := comparePDF(ref, c, o)
	r.OneUp = u
	return r
}

// hasWords reports whether page (0-based) of d has a word.
func hasWords(d *pdfWords, page int) bool {
	for _, w := range d.Words {
		if w.Page == page {
			return true
		}
	}
	return false
}

// sheetOf returns where page i of a comparison is in the reference PDF and
// in the candidate PDF: the page of the PDF (0-based) and the half of it
// (0 the whole page, 1 the left half, 2 the right half; see halfImage).
func (r pdfResult) sheetOf(i int) (refSheet, refSide, candSheet, candSide int) {
	switch {
	case r.TwoUp:
		return i / 2, 1 + i%2, i / 2, 1 + i%2
	case r.OneUp != nil && r.OneUp.Halved:
		return i, 0, i / 2, 1 + i%2
	}
	return i, 0, i, 0
}

// checkTrimmedHalf draws the last sheet of the candidate when its right
// half was left out as empty (see oneUp.Trimmed) and sets HalfErr when the
// half has ink or could not be drawn.
func checkTrimmedHalf(candPath string, r *pdfResult) {
	u := r.OneUp
	if u == nil || !u.Trimmed {
		return
	}
	g, err := renderPage(candPath, u.CandSheets-1)
	if err != nil {
		u.HalfErr = "it could not be drawn: " + err.Error()
		return
	}
	if hasInk(halfImage(g, 2)) {
		u.HalfErr = "something is drawn on it"
	}
}

// hasInk reports whether g has an outline (see outline, with
// inkPartnerStep).
func hasInk(g *pageImage) bool {
	for _, on := range outline(g, inkPartnerStep) {
		if on {
			return true
		}
	}
	return false
}
