package sfnt

import (
	"errors"
	"fmt"
)

// cmapTable is one Unicode subtable of the cmap table, format 4 or 12.
type cmapTable struct {
	format int
	data   []byte // the subtable
}

// parseCmap picks the widest Unicode subtable: format 12 (full Unicode)
// before format 4 (BMP).
func parseCmap(t []byte) (cmapTable, error) {
	if len(t) < 4 {
		return cmapTable{}, errors.New("short cmap table")
	}
	n := int(u16(t, 2))
	best, bestRank := cmapTable{}, 0
	for i := 0; i < n; i++ {
		r := 4 + 8*i
		if r+8 > len(t) {
			return cmapTable{}, errors.New("short cmap table")
		}
		pid, eid, off := u16(t, r), u16(t, r+2), int(u32(t, r+4))
		unicode := pid == 0 || (pid == 3 && (eid == 1 || eid == 10))
		if !unicode || off+4 > len(t) {
			continue
		}
		format := int(u16(t, off))
		var length int
		rank := 0
		switch format {
		case 4:
			length, rank = int(u16(t, off+2)), 1
		case 12:
			if off+8 > len(t) {
				continue
			}
			length, rank = int(u32(t, off+4)), 2
		default:
			continue
		}
		if length > len(t)-off {
			// Some fonts store a wrong format 4 length; keep what is there.
			length = len(t) - off
		}
		if rank > bestRank {
			best, bestRank = cmapTable{format: format, data: t[off : off+length]}, rank
		}
	}
	if bestRank == 0 {
		return cmapTable{}, errors.New("no Unicode cmap subtable (format 4 or 12)")
	}
	if err := best.check(); err != nil {
		return cmapTable{}, err
	}
	return best, nil
}

func (c cmapTable) check() error {
	d := c.data
	switch c.format {
	case 4:
		if len(d) < 14 {
			return errors.New("short cmap format 4 subtable")
		}
		segX2 := int(u16(d, 6))
		if 16+4*segX2 > len(d) {
			return fmt.Errorf("cmap format 4 has %d segments but only %d bytes", segX2/2, len(d))
		}
	case 12:
		if len(d) < 16 || 16+12*int(u32(d, 12)) > len(d) {
			return errors.New("short cmap format 12 subtable")
		}
	}
	return nil
}

func (c cmapTable) lookup(r rune) int {
	d := c.data
	switch c.format {
	case 4:
		if r < 0 || r > 0xFFFF {
			return 0
		}
		segX2 := int(u16(d, 6))
		ends, starts := 14, 16+segX2
		deltas, ranges := starts+segX2, starts+2*segX2
		lo, hi := 0, segX2/2
		for lo < hi {
			m := (lo + hi) / 2
			if rune(u16(d, ends+2*m)) < r {
				lo = m + 1
			} else {
				hi = m
			}
		}
		if lo == segX2/2 {
			return 0
		}
		return c.format4Glyph(lo, r, starts, deltas, ranges)
	case 12:
		n := int(u32(d, 12))
		lo, hi := 0, n
		for lo < hi {
			m := (lo + hi) / 2
			g := 16 + 12*m
			if rune(u32(d, g+4)) < r {
				lo = m + 1
			} else {
				hi = m
			}
		}
		if lo == n {
			return 0
		}
		g := 16 + 12*lo
		start := rune(u32(d, g))
		if r < start {
			return 0
		}
		return int(u32(d, g+8)) + int(r-start)
	}
	return 0
}

func (c cmapTable) format4Glyph(seg int, r rune, starts, deltas, ranges int) int {
	d := c.data
	start := rune(u16(d, starts+2*seg))
	if r < start {
		return 0
	}
	delta := int(u16(d, deltas+2*seg))
	ro := int(u16(d, ranges+2*seg))
	if ro == 0 {
		return (int(r) + delta) & 0xFFFF
	}
	at := ranges + 2*seg + ro + 2*int(r-start)
	if at+2 > len(d) {
		return 0
	}
	g := int(u16(d, at))
	if g == 0 {
		return 0
	}
	return (g + delta) & 0xFFFF
}

// each calls fn for every mapped character.
func (c cmapTable) each(fn func(r rune, g int)) {
	d := c.data
	switch c.format {
	case 4:
		segX2 := int(u16(d, 6))
		ends, starts := 14, 16+segX2
		deltas, ranges := starts+segX2, starts+2*segX2
		for s := 0; s < segX2/2; s++ {
			start, end := rune(u16(d, starts+2*s)), rune(u16(d, ends+2*s))
			for r := start; r <= end && r != 0xFFFF; r++ {
				if g := c.format4Glyph(s, r, starts, deltas, ranges); g != 0 {
					fn(r, g)
				}
			}
		}
	case 12:
		n := int(u32(d, 12))
		for i := 0; i < n; i++ {
			g := 16 + 12*i
			start, end, gid := rune(u32(d, g)), rune(u32(d, g+4)), int(u32(d, g+8))
			if end < start || end > 0x10FFFF {
				continue
			}
			for r := start; r <= end; r++ {
				fn(r, gid+int(r-start))
			}
		}
	}
}
