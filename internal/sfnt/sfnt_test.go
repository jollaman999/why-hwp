package sfnt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"testing"
)

func TestFixtureLicence(t *testing.T) {
	f, _ := readFixture(t)
	if lic := f.Name(13); !strings.Contains(lic, "SIL Open Font License") {
		t.Errorf("name ID 13 = %q, want the SIL Open Font License", lic)
	}
	if got, want := len(f.Runes()), len([]rune(fixtureText)); got != want {
		t.Errorf("fixture maps %d characters, want %d", got, want)
	}
}

func TestMetrics(t *testing.T) {
	f, _ := readFixture(t)
	if f.UnitsPerEm() != 1000 {
		t.Errorf("unitsPerEm = %d, want 1000", f.UnitsPerEm())
	}
	if f.Ascender() <= 0 || f.Descender() >= 0 {
		t.Errorf("ascender %d, descender %d: want positive and negative", f.Ascender(), f.Descender())
	}
	wW, ok := f.Advance('W')
	wI, _ := f.Advance('i')
	if !ok || wW <= wI {
		t.Errorf("advance W = %d, i = %d: want W wider", wW, wI)
	}
	if _, ok := f.Advance('가'); ok {
		t.Error("Advance reports a Hangul glyph the fixture does not have")
	}
	if _, _, _, _, ok := f.Bounds(' '); ok {
		t.Error("a space has outline bounds")
	}
	_, yMin, _, yMax, ok := f.Bounds('g')
	if !ok || yMin >= 0 || yMax <= 0 {
		t.Errorf("bounds of g: yMin %d yMax %d ok %v; want a descender", yMin, yMax, ok)
	}
}

// sameGlyph compares glyph og of the original with glyph ng of the subset:
// identical bytes for a simple glyph, and for a composite identical bytes
// apart from component ids, whose glyphs must match recursively.
func sameGlyph(t *testing.T, of *Font, og int, nf *Font, ng int) {
	t.Helper()
	a, b := of.GlyphData(og), nf.GlyphData(ng)
	if len(b) < len(a) || !bytes.Equal(b[len(a):], make([]byte, len(b)-len(a))) {
		t.Fatalf("glyph %d -> %d: %d bytes, want %d plus zero padding", og, ng, len(b), len(a))
	}
	b = b[:len(a)]
	if len(a) < compositeHeader || int16(u16(a, 0)) >= 0 {
		if !bytes.Equal(a, b) {
			t.Fatalf("glyph %d -> %d: outline bytes differ", og, ng)
		}
		return
	}
	var at []int
	if err := walkComponents(a, func(i int) { at = append(at, i) }); err != nil {
		t.Fatal(err)
	}
	a2, b2 := append([]byte(nil), a...), append([]byte(nil), b...)
	for _, i := range at {
		sameGlyph(t, of, int(u16(a, i)), nf, int(u16(b, i)))
		a2[i], a2[i+1], b2[i], b2[i+1] = 0, 0, 0, 0
	}
	if !bytes.Equal(a2, b2) {
		t.Fatalf("composite glyph %d -> %d: bytes other than component ids differ", og, ng)
	}
}

func TestSubsetKeepsOutlines(t *testing.T) {
	f, _ := readFixture(t)
	text := "Hello, World! éÅ\U00010B00"
	res, err := f.Subset([]rune(text + "가"))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Missing) != "가" {
		t.Errorf("missing = %q, want the Hangul syllable only", string(res.Missing))
	}
	sub, err := Parse(res.Font)
	if err != nil {
		t.Fatalf("subset does not parse: %v", err)
	}
	if sub.NumGlyphs() != res.Glyphs || res.Glyphs >= f.NumGlyphs() {
		t.Errorf("subset has %d glyphs (result says %d), source %d", sub.NumGlyphs(), res.Glyphs, f.NumGlyphs())
	}
	for _, r := range text {
		og, ok1 := f.GlyphIndex(r)
		ng, ok2 := sub.GlyphIndex(r)
		if ok1 != ok2 {
			t.Fatalf("%q: mapped in source %v, in subset %v", r, ok1, ok2)
		}
		if !ok1 {
			continue
		}
		sameGlyph(t, f, og, sub, ng)
		oa, _ := f.Advance(r)
		na, _ := sub.Advance(r)
		if oa != na || f.glyphLSB(og) != sub.glyphLSB(ng) {
			t.Errorf("%q: metrics %d/%d, want %d/%d", r, na, sub.glyphLSB(ng), oa, f.glyphLSB(og))
		}
	}
	sameGlyph(t, f, 0, sub, 0)
	if _, ok := sub.GlyphIndex('Z'); ok {
		t.Error("subset maps Z, which was not asked for")
	}
}

func TestSubsetClosesComposites(t *testing.T) {
	f, _ := readFixture(t)
	g, _ := f.GlyphIndex('é')
	comps, err := f.components(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) < 2 {
		t.Fatalf("é has %d components; the fixture should draw it as a composite", len(comps))
	}
	res, err := f.Subset([]rune{'é'})
	if err != nil {
		t.Fatal(err)
	}
	if want := 2 + len(comps); res.Glyphs != want {
		t.Errorf("subset of é has %d glyphs, want %d (.notdef, é and its components)", res.Glyphs, want)
	}
	sub, err := Parse(res.Font)
	if err != nil {
		t.Fatal(err)
	}
	ng, _ := sub.GlyphIndex('é')
	nc, err := sub.components(ng)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range nc {
		if c == 0 || c >= sub.NumGlyphs() || c == ng {
			t.Errorf("component id %d is not a separate glyph of the subset", c)
		}
	}
	sameGlyph(t, f, g, sub, ng)
}

func TestSubsetTablesAndChecksums(t *testing.T) {
	f, _ := readFixture(t)
	res, err := f.Subset([]rune("Ab\U00010B00"))
	if err != nil {
		t.Fatal(err)
	}
	font := res.Font
	if got := Checksum(font); got != 0xB1B0AFBA {
		t.Errorf("whole-font checksum = %#x, want 0xB1B0AFBA", got)
	}
	n := int(u16(font, 4))
	var tags []string
	for i := 0; i < n; i++ {
		rec := font[12+16*i:]
		tag := string(rec[:4])
		tags = append(tags, tag)
		off, length := u32(rec, 8), u32(rec, 12)
		if off%4 != 0 {
			t.Errorf("%s at offset %d is not 4-byte aligned", tag, off)
		}
		table := append([]byte(nil), font[off:off+length]...)
		if tag == "head" {
			binary.BigEndian.PutUint32(table[8:], 0)
		}
		if got, want := u32(rec, 4), Checksum(table); got != want {
			t.Errorf("%s checksum = %#x, want %#x", tag, got, want)
		}
	}
	if !sort.StringsAreSorted(tags) {
		t.Errorf("table directory not sorted: %v", tags)
	}
	allowed := map[string]bool{}
	for _, tag := range strings.Split("head hhea maxp OS/2 name cmap hmtx glyf loca post cvt  fpgm prep gasp", " ") {
		allowed[tag] = true
	}
	allowed["cvt "] = true
	for _, tag := range tags {
		if !allowed[tag] {
			t.Errorf("subset keeps table %q", tag)
		}
	}
	sub, err := Parse(font)
	if err != nil {
		t.Fatal(err)
	}
	if v := u32(sub.Table("post"), 0); v != 0x00030000 {
		t.Errorf("post version %#x, want 3.0", v)
	}
	if v := u16(sub.Table("head"), 50); v != 1 {
		t.Errorf("indexToLocFormat %d, want 1", v)
	}
	if v := int(u16(sub.Table("hhea"), 34)); v != sub.NumGlyphs() {
		t.Errorf("numberOfHMetrics %d, want %d", v, sub.NumGlyphs())
	}
	if sub.cmap.format != 12 {
		t.Errorf("cmap uses format %d, want 12 for a character outside the BMP", sub.cmap.format)
	}
	if _, ok := sub.GlyphIndex('\U00010B00'); !ok {
		t.Error("U+10B00 is not mapped in the subset")
	}
	// The BMP-only subset has only format 4.
	res2, err := f.Subset([]rune("Ab"))
	if err != nil {
		t.Fatal(err)
	}
	sub2, err := Parse(res2.Font)
	if err != nil {
		t.Fatal(err)
	}
	if sub2.cmap.format != 4 {
		t.Errorf("BMP-only subset uses cmap format %d, want 4", sub2.cmap.format)
	}
	for _, r := range "Ab" {
		if _, ok := sub2.GlyphIndex(r); !ok {
			t.Errorf("%q is not mapped in the format 4 subset", r)
		}
	}
	// Subsetting is deterministic.
	again, _ := f.Subset([]rune("Ab\U00010B00"))
	if !bytes.Equal(again.Font, font) {
		t.Error("two subsets of the same text differ")
	}
}

// patched returns a copy of the fixture with the table tag renamed.
func patched(t *testing.T, data []byte, from, to string) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	for i := 0; i < int(u16(out, 4)); i++ {
		rec := out[12+16*i:]
		if string(rec[:4]) == from {
			copy(rec, to)
			return out
		}
	}
	t.Fatalf("fixture has no %q table", from)
	return nil
}

func TestRejects(t *testing.T) {
	_, data := readFixture(t)
	otto := append([]byte(nil), data...)
	copy(otto, "OTTO")
	ttc := append([]byte(nil), data...)
	copy(ttc, "ttcf")
	restricted := append([]byte(nil), data...)
	for i := 0; i < int(u16(restricted, 4)); i++ {
		rec := restricted[12+16*i:]
		if string(rec[:4]) == "OS/2" {
			binary.BigEndian.PutUint16(restricted[u32(rec, 8)+8:], 0x0002)
		}
	}
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"OTTO", otto, ErrCFF},
		{"CFF table", patched(t, data, "post", "CFF "), ErrCFF},
		{"fvar", patched(t, data, "post", "fvar"), ErrVariable},
		{"ttc", ttc, ErrCollection},
		{"fsType", restricted, ErrRestricted},
	}
	for _, c := range cases {
		_, err := Parse(c.data)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if !strings.Contains(ErrCFF.Error(), "Noto Sans KR static TTF") {
		t.Errorf("CFF error %q does not suggest a TrueType font", ErrCFF)
	}
	if _, err := Parse([]byte("not a font at all")); err == nil {
		t.Error("garbage parsed")
	}
}
