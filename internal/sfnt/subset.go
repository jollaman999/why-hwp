package sfnt

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"unicode"
)

// Composite glyph flags.
const (
	argsAreWords    = 0x0001
	haveScale       = 0x0008
	moreComponents  = 0x0020
	haveXYScale     = 0x0040
	haveTwoByTwo    = 0x0080
	compositeHeader = 10
)

// SubsetResult describes a subset.
type SubsetResult struct {
	Font    []byte // the subset font file
	Glyphs  int    // glyphs in the subset, .notdef included
	Missing []rune // requested characters the font does not have
	// Empty are requested characters the font maps to a glyph without an
	// outline, other than white space: they come out blank.
	Empty []rune
}

// Subset writes a font holding .notdef, the glyphs of runes and every glyph
// those glyphs use as components. Glyphs keep their order and are numbered
// from 0 again; outlines are copied byte for byte (only component glyph ids
// in composite glyphs are renumbered).
//
// The subset keeps head, hhea, maxp, OS/2, name, cmap, hmtx, glyf, loca,
// post (as version 3.0, without glyph names), cvt, fpgm, prep and gasp.
// Layout tables (GSUB, GPOS, kern), device metrics (hdmx, LTSH, VDMX),
// signatures (DSIG) and vertical metrics are dropped: they refer to glyph ids
// that no longer exist.
func (f *Font) Subset(runes []rune) (*SubsetResult, error) {
	return f.subset(runes, nil)
}

// SubsetAs is Subset with the family name and weight of n written into the
// subset: name IDs 1, 2, 3, 4 and 6 are replaced, the other identity names
// (16, 17, 18, 21, 22, 25) are dropped, and OS/2 usWeightClass, the OS/2
// fsSelection bold and regular bits and the head macStyle bold bit are set
// for the weight. Copyright, trademark and licence names (0, 7, 13, 14) and
// every other name are kept.
func (f *Font) SubsetAs(runes []rune, n Naming) (*SubsetResult, error) {
	return f.subset(runes, &n)
}

func (f *Font) subset(runes []rune, naming *Naming) (*SubsetResult, error) {
	res := &SubsetResult{}
	cmap := map[rune]int{} // rune -> old glyph id
	used := map[int]bool{0: true}
	for _, r := range runes {
		if _, dup := cmap[r]; dup {
			continue
		}
		g, ok := f.GlyphIndex(r)
		if !ok {
			if !containsRune(res.Missing, r) {
				res.Missing = append(res.Missing, r)
			}
			continue
		}
		cmap[r] = g
		used[g] = true
		if len(f.GlyphData(g)) == 0 && !unicode.IsSpace(r) {
			res.Empty = append(res.Empty, r)
		}
	}
	// Close over composite components.
	queue := make([]int, 0, len(used))
	for g := range used {
		queue = append(queue, g)
	}
	for len(queue) > 0 {
		g := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		comps, err := f.components(g)
		if err != nil {
			return nil, err
		}
		for _, c := range comps {
			if !used[c] {
				used[c] = true
				queue = append(queue, c)
			}
		}
	}
	old := make([]int, 0, len(used))
	for g := range used {
		old = append(old, g)
	}
	sort.Ints(old)
	newID := make(map[int]int, len(old))
	for i, g := range old {
		newID[g] = i
	}
	res.Glyphs = len(old)

	// glyf and loca (long offsets).
	var glyf []byte
	loca := make([]byte, 4*(len(old)+1))
	for i, g := range old {
		binary.BigEndian.PutUint32(loca[4*i:], uint32(len(glyf)))
		b := append([]byte(nil), f.GlyphData(g)...)
		if len(b) >= compositeHeader && int16(u16(b, 0)) < 0 {
			if err := renumberComponents(b, newID); err != nil {
				return nil, fmt.Errorf("glyph %d: %w", g, err)
			}
		}
		glyf = append(glyf, b...)
		for len(glyf)%4 != 0 {
			glyf = append(glyf, 0)
		}
	}
	binary.BigEndian.PutUint32(loca[4*len(old):], uint32(len(glyf)))

	hmtx := make([]byte, 4*len(old))
	for i, g := range old {
		binary.BigEndian.PutUint16(hmtx[4*i:], uint16(f.glyphAdvance(g)))
		binary.BigEndian.PutUint16(hmtx[4*i+2:], uint16(f.glyphLSB(g)))
	}

	newMap := make(map[rune]int, len(cmap))
	for r, g := range cmap {
		newMap[r] = newID[g]
	}

	out := map[string][]byte{
		"glyf": glyf,
		"loca": loca,
		"hmtx": hmtx,
		"cmap": buildCmap(newMap),
	}
	head := append([]byte(nil), f.tables["head"]...)
	binary.BigEndian.PutUint32(head[8:], 0) // checksumAdjustment, set below
	binary.BigEndian.PutUint16(head[50:], 1)
	out["head"] = head

	hhea := append([]byte(nil), f.tables["hhea"]...)
	binary.BigEndian.PutUint16(hhea[34:], uint16(len(old)))
	out["hhea"] = hhea

	maxp := append([]byte(nil), f.tables["maxp"]...)
	binary.BigEndian.PutUint16(maxp[4:], uint16(len(old)))
	out["maxp"] = maxp

	if post := f.tables["post"]; post != nil {
		if len(post) < 32 {
			return nil, errors.New("short post table")
		}
		p := append([]byte(nil), post[:32]...)
		binary.BigEndian.PutUint32(p[0:], 0x00030000)
		out["post"] = p
	}
	for _, t := range []string{"OS/2", "name", "cvt ", "fpgm", "prep", "gasp"} {
		if b := f.tables[t]; b != nil {
			out[t] = b
		}
	}
	if naming != nil {
		if err := applyNaming(out, *naming); err != nil {
			return nil, err
		}
	}
	res.Font = writeFont(out)
	return res, nil
}

func containsRune(rs []rune, r rune) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}

// components returns the glyph ids a composite glyph refers to.
func (f *Font) components(g int) ([]int, error) {
	b := f.GlyphData(g)
	if len(b) < compositeHeader || int16(u16(b, 0)) >= 0 {
		return nil, nil
	}
	var out []int
	err := walkComponents(b, func(at int) {
		out = append(out, int(u16(b, at)))
	})
	if err != nil {
		return nil, fmt.Errorf("glyph %d: %w", g, err)
	}
	for _, c := range out {
		if c >= f.numGlyphs {
			return nil, fmt.Errorf("glyph %d refers to glyph %d of %d", g, c, f.numGlyphs)
		}
	}
	return out, nil
}

// walkComponents calls fn with the offset of each component's glyph id.
func walkComponents(b []byte, fn func(at int)) error {
	p := compositeHeader
	for {
		if p+4 > len(b) {
			return errors.New("truncated composite glyph")
		}
		flags := u16(b, p)
		fn(p + 2)
		p += 4
		if flags&argsAreWords != 0 {
			p += 4
		} else {
			p += 2
		}
		switch {
		case flags&haveScale != 0:
			p += 2
		case flags&haveXYScale != 0:
			p += 4
		case flags&haveTwoByTwo != 0:
			p += 8
		}
		if flags&moreComponents == 0 {
			return nil
		}
	}
}

func renumberComponents(b []byte, newID map[int]int) error {
	var err error
	werr := walkComponents(b, func(at int) {
		n, ok := newID[int(u16(b, at))]
		if !ok {
			err = fmt.Errorf("component glyph %d is not in the subset", u16(b, at))
			return
		}
		binary.BigEndian.PutUint16(b[at:], uint16(n))
	})
	if werr != nil {
		return werr
	}
	return err
}

// buildCmap writes a cmap with a format 4 subtable for the BMP and, when a
// character lies outside the BMP, a format 12 subtable for all of them.
func buildCmap(m map[rune]int) []byte {
	runes := make([]rune, 0, len(m))
	wide := false
	for r := range m {
		runes = append(runes, r)
		if r > 0xFFFF {
			wide = true
		}
	}
	sort.Slice(runes, func(i, j int) bool { return runes[i] < runes[j] })

	f4 := buildFormat4(runes, m)
	type rec struct {
		pid, eid uint16
		sub      int // 0 format 4, 1 format 12
	}
	recs := []rec{{0, 3, 0}, {3, 1, 0}}
	subs := [][]byte{f4}
	if wide {
		subs = append(subs, buildFormat12(runes, m))
		recs = []rec{{0, 3, 0}, {0, 4, 1}, {3, 1, 0}, {3, 10, 1}}
	}
	hdr := 4 + 8*len(recs)
	offs := []int{hdr}
	if len(subs) == 2 {
		offs = append(offs, hdr+len(f4))
	}
	out := make([]byte, hdr)
	binary.BigEndian.PutUint16(out[2:], uint16(len(recs)))
	for i, r := range recs {
		binary.BigEndian.PutUint16(out[4+8*i:], r.pid)
		binary.BigEndian.PutUint16(out[4+8*i+2:], r.eid)
		binary.BigEndian.PutUint32(out[4+8*i+4:], uint32(offs[r.sub]))
	}
	for _, s := range subs {
		out = append(out, s...)
	}
	return out
}

// buildFormat4 maps each run of consecutive characters with consecutive
// glyph ids to one segment with an idDelta.
func buildFormat4(runes []rune, m map[rune]int) []byte {
	type seg struct{ start, end, delta int }
	var segs []seg
	for _, r := range runes {
		if r > 0xFFFE {
			continue
		}
		g := m[r]
		if n := len(segs); n > 0 && segs[n-1].end == int(r)-1 && segs[n-1].start+segs[n-1].delta+(int(r)-segs[n-1].start) == g {
			segs[n-1].end = int(r)
			continue
		}
		segs = append(segs, seg{int(r), int(r), g - int(r)})
	}
	segs = append(segs, seg{0xFFFF, 0xFFFF, 1})
	n := len(segs)
	searchRange, entrySelector := 2, 0
	for searchRange*2 <= 2*n {
		searchRange *= 2
		entrySelector++
	}
	length := 16 + 8*n
	b := make([]byte, length)
	put := func(i int, v int) { binary.BigEndian.PutUint16(b[i:], uint16(v)) }
	put(0, 4)
	put(2, length)
	put(6, 2*n)
	put(8, searchRange)
	put(10, entrySelector)
	put(12, 2*n-searchRange)
	for i, s := range segs {
		put(14+2*i, s.end)
		put(16+2*n+2*i, s.start)
		put(16+4*n+2*i, s.delta&0xFFFF)
		put(16+6*n+2*i, 0)
	}
	return b
}

func buildFormat12(runes []rune, m map[rune]int) []byte {
	type group struct{ start, end, gid int }
	var gs []group
	for _, r := range runes {
		g := m[r]
		if n := len(gs); n > 0 && gs[n-1].end == int(r)-1 && gs[n-1].gid+int(r)-gs[n-1].start == g {
			gs[n-1].end = int(r)
			continue
		}
		gs = append(gs, group{int(r), int(r), g})
	}
	b := make([]byte, 16+12*len(gs))
	binary.BigEndian.PutUint16(b[0:], 12)
	binary.BigEndian.PutUint32(b[4:], uint32(len(b)))
	binary.BigEndian.PutUint32(b[12:], uint32(len(gs)))
	for i, g := range gs {
		binary.BigEndian.PutUint32(b[16+12*i:], uint32(g.start))
		binary.BigEndian.PutUint32(b[16+12*i+4:], uint32(g.end))
		binary.BigEndian.PutUint32(b[16+12*i+8:], uint32(g.gid))
	}
	return b
}

// writeFont lays the tables out in tag order, each padded to four bytes,
// with table checksums and head.checksumAdjustment filled in.
func writeFont(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for t := range tables {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	n := len(tags)
	searchRange, entrySelector := 1, 0
	for searchRange*2 <= n {
		searchRange *= 2
		entrySelector++
	}
	searchRange *= 16
	out := make([]byte, 12+16*n)
	binary.BigEndian.PutUint32(out[0:], 0x00010000)
	binary.BigEndian.PutUint16(out[4:], uint16(n))
	binary.BigEndian.PutUint16(out[6:], uint16(searchRange))
	binary.BigEndian.PutUint16(out[8:], uint16(entrySelector))
	binary.BigEndian.PutUint16(out[10:], uint16(16*n-searchRange))
	headAt := -1
	for i, t := range tags {
		b := tables[t]
		rec := out[12+16*i:]
		copy(rec, t)
		binary.BigEndian.PutUint32(rec[4:], Checksum(b))
		binary.BigEndian.PutUint32(rec[8:], uint32(len(out)))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(b)))
		if t == "head" {
			headAt = len(out)
		}
		out = append(out, b...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
	}
	if headAt >= 0 {
		binary.BigEndian.PutUint32(out[headAt+8:], 0xB1B0AFBA-Checksum(out))
	}
	return out
}

// Checksum is the sfnt table checksum: the sum of big-endian uint32 words,
// the last one padded with zeros.
func Checksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i < len(b); i += 4 {
		var w [4]byte
		copy(w[:], b[i:])
		sum += binary.BigEndian.Uint32(w[:])
	}
	return sum
}
