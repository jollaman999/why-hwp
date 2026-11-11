// Package sfnt reads TrueType (glyf) fonts, measures text with them and
// writes subsets that hold only the glyphs a document needs.
//
// Only single TrueType fonts are accepted. CFF outlines, variable fonts,
// font collections and fonts whose embedding licence is restricted are
// rejected with an error that says why.
package sfnt

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"unicode/utf16"
)

// Errors returned by Parse for fonts this package does not handle.
var (
	ErrCFF        = errors.New("CFF outlines are not supported; use a TrueType font such as Noto Sans KR static TTF")
	ErrVariable   = errors.New("variable fonts (fvar table) are not supported; use a static TrueType font such as Noto Sans KR static TTF")
	ErrCollection = errors.New("font collections (.ttc) are not supported; use a single TrueType font (.ttf)")
	ErrRestricted = errors.New("the font's embedding licence is restricted (OS/2 fsType bit 0x0002); it may not be embedded")
)

// Font is a parsed TrueType font. The font data must not be changed while
// the Font is in use.
type Font struct {
	data   []byte
	tables map[string][]byte

	numGlyphs  int
	unitsPerEm int
	ascender   int
	descender  int
	fsType     uint16

	nHMetrics int
	hmtx      []byte
	loca      []uint32 // numGlyphs+1 offsets into glyf
	glyf      []byte

	cmap cmapTable
}

// Parse reads a TrueType font.
func Parse(data []byte) (*Font, error) {
	if len(data) < 12 {
		return nil, errors.New("not a font file: too short")
	}
	switch tag := string(data[:4]); tag {
	case "\x00\x01\x00\x00", "true":
	case "ttcf":
		return nil, ErrCollection
	case "OTTO":
		return nil, ErrCFF
	case "wOFF", "wOF2":
		return nil, errors.New("WOFF fonts are not supported; use a TrueType font (.ttf)")
	default:
		return nil, fmt.Errorf("not a TrueType font (sfnt version %q)", tag)
	}
	n := int(u16(data, 4))
	if 12+16*n > len(data) {
		return nil, errors.New("truncated table directory")
	}
	f := &Font{data: data, tables: map[string][]byte{}}
	for i := 0; i < n; i++ {
		rec := data[12+16*i:]
		tag := string(rec[:4])
		off, length := int(u32(rec, 8)), int(u32(rec, 12))
		if off < 0 || length < 0 || off > len(data) || length > len(data)-off {
			return nil, fmt.Errorf("table %q lies outside the file", tag)
		}
		f.tables[tag] = data[off : off+length]
	}
	if f.tables["CFF "] != nil || f.tables["CFF2"] != nil {
		return nil, ErrCFF
	}
	if f.tables["fvar"] != nil {
		return nil, ErrVariable
	}
	for _, t := range []string{"head", "hhea", "maxp", "hmtx", "loca", "glyf", "cmap"} {
		if f.tables[t] == nil {
			return nil, fmt.Errorf("missing %q table", t)
		}
	}
	if os2 := f.tables["OS/2"]; len(os2) >= 10 {
		f.fsType = u16(os2, 8)
		if f.fsType&0x0002 != 0 {
			return nil, ErrRestricted
		}
	}

	head := f.tables["head"]
	if len(head) < 54 {
		return nil, errors.New("short head table")
	}
	f.unitsPerEm = int(u16(head, 18))
	if f.unitsPerEm == 0 {
		return nil, errors.New("head.unitsPerEm is zero")
	}
	longLoca := int16(u16(head, 50)) == 1

	maxp := f.tables["maxp"]
	if len(maxp) < 6 {
		return nil, errors.New("short maxp table")
	}
	f.numGlyphs = int(u16(maxp, 4))
	if f.numGlyphs == 0 {
		return nil, errors.New("font has no glyphs")
	}

	hhea := f.tables["hhea"]
	if len(hhea) < 36 {
		return nil, errors.New("short hhea table")
	}
	f.ascender = int(int16(u16(hhea, 4)))
	f.descender = int(int16(u16(hhea, 6)))
	f.nHMetrics = int(u16(hhea, 34))
	f.hmtx = f.tables["hmtx"]
	if f.nHMetrics == 0 || f.nHMetrics > f.numGlyphs || len(f.hmtx) < 4*f.nHMetrics+2*(f.numGlyphs-f.nHMetrics) {
		return nil, errors.New("hmtx table does not match hhea.numberOfHMetrics")
	}

	f.glyf = f.tables["glyf"]
	loca := f.tables["loca"]
	f.loca = make([]uint32, f.numGlyphs+1)
	for i := range f.loca {
		if longLoca {
			if len(loca) < 4*(i+1) {
				return nil, errors.New("short loca table")
			}
			f.loca[i] = u32(loca, 4*i)
		} else {
			if len(loca) < 2*(i+1) {
				return nil, errors.New("short loca table")
			}
			f.loca[i] = 2 * uint32(u16(loca, 2*i))
		}
	}
	for i := 0; i < f.numGlyphs; i++ {
		if f.loca[i] > f.loca[i+1] || int(f.loca[i+1]) > len(f.glyf) {
			return nil, fmt.Errorf("loca entry for glyph %d is out of range", i)
		}
	}

	c, err := parseCmap(f.tables["cmap"])
	if err != nil {
		return nil, err
	}
	f.cmap = c
	return f, nil
}

// NumGlyphs is the number of glyphs in the font.
func (f *Font) NumGlyphs() int { return f.numGlyphs }

// UnitsPerEm is the design grid size; advances and bounds are in these units.
func (f *Font) UnitsPerEm() int { return f.unitsPerEm }

// Ascender is hhea.ascender in font units (positive, above the baseline).
func (f *Font) Ascender() int { return f.ascender }

// Descender is hhea.descender in font units (negative, below the baseline).
func (f *Font) Descender() int { return f.descender }

// FSType is the OS/2 embedding permission field (0 when there is no OS/2).
func (f *Font) FSType() uint16 { return f.fsType }

// GlyphIndex returns the glyph for r, or 0 and false when the font has none.
func (f *Font) GlyphIndex(r rune) (int, bool) {
	g := f.cmap.lookup(r)
	if g <= 0 || g >= f.numGlyphs {
		return 0, false
	}
	return g, true
}

// Advance returns the advance width of r in font units. A character the
// font does not have gets the advance of .notdef and ok false.
func (f *Font) Advance(r rune) (adv int, ok bool) {
	g, ok := f.GlyphIndex(r)
	return f.glyphAdvance(g), ok
}

func (f *Font) glyphAdvance(g int) int {
	if g >= f.nHMetrics {
		g = f.nHMetrics - 1
	}
	return int(u16(f.hmtx, 4*g))
}

func (f *Font) glyphLSB(g int) int16 {
	if g < f.nHMetrics {
		return int16(u16(f.hmtx, 4*g+2))
	}
	return int16(u16(f.hmtx, 4*f.nHMetrics+2*(g-f.nHMetrics)))
}

// Bounds returns the outline bounding box of r's glyph in font units, y up.
// ok is false for an empty glyph (a space) or a character the font lacks.
func (f *Font) Bounds(r rune) (xMin, yMin, xMax, yMax int, ok bool) {
	g, ok := f.GlyphIndex(r)
	if !ok {
		return 0, 0, 0, 0, false
	}
	b := f.GlyphData(g)
	if len(b) < 10 {
		return 0, 0, 0, 0, false
	}
	return int(int16(u16(b, 2))), int(int16(u16(b, 4))), int(int16(u16(b, 6))), int(int16(u16(b, 8))), true
}

// GlyphData returns the glyf bytes of glyph g as stored in the font.
func (f *Font) GlyphData(g int) []byte {
	if g < 0 || g >= f.numGlyphs {
		return nil
	}
	return f.glyf[f.loca[g]:f.loca[g+1]]
}

// Table returns the raw bytes of a table, or nil.
func (f *Font) Table(tag string) []byte { return f.tables[tag] }

// Name returns a string from the name table (Windows Unicode records first,
// then Macintosh Roman), or "" when there is none. nameID 1 is the family,
// 13 the licence description and 14 the licence URL.
func (f *Font) Name(nameID int) string {
	t := f.tables["name"]
	if len(t) < 6 {
		return ""
	}
	count, strOff := int(u16(t, 2)), int(u16(t, 4))
	var mac string
	for i := 0; i < count; i++ {
		r := 6 + 12*i
		if r+12 > len(t) {
			break
		}
		pid, eid, id := u16(t, r), u16(t, r+2), u16(t, r+6)
		length, off := int(u16(t, r+8)), int(u16(t, r+10))
		if int(id) != nameID || strOff+off+length > len(t) {
			continue
		}
		s := t[strOff+off : strOff+off+length]
		switch {
		case (pid == 3 && (eid == 1 || eid == 10)) || pid == 0:
			u := make([]uint16, len(s)/2)
			for j := range u {
				u[j] = u16(s, 2*j)
			}
			return string(utf16.Decode(u))
		case pid == 1 && eid == 0 && mac == "":
			mac = string(s)
		}
	}
	return mac
}

// Runes returns every character the font maps, sorted.
func (f *Font) Runes() []rune {
	var out []rune
	f.cmap.each(func(r rune, g int) {
		if g > 0 && g < f.numGlyphs {
			out = append(out, r)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func u16(b []byte, i int) uint16 { return binary.BigEndian.Uint16(b[i:]) }
func u32(b []byte, i int) uint32 { return binary.BigEndian.Uint32(b[i:]) }
