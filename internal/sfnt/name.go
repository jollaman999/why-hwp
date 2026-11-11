package sfnt

import (
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"unicode/utf16"
)

// Naming gives a subset its own family name and weight.
//
// Renderers that ignore @font-face, such as resvg, pick an embedded font by
// the family in its name table. resvg's font database (fontdb 0.23) reads
// the family from name ID 16 (typographic family) in every language, falls
// back to ID 1 when there is no ID 16, requires ID 6 (PostScript name), and
// takes weight and style from OS/2 usWeightClass and fsSelection.
type Naming struct {
	Family string
	Bold   bool
}

// renamedIDs are the name IDs that carry the font's identity: family,
// subfamily, unique id, full name, PostScript name, typographic family and
// subfamily, Macintosh full name, WWS family and subfamily, and variations
// PostScript prefix. Their records are dropped and IDs 1, 2, 3, 4 and 6 are
// written again; copyright, version, trademark, licence and every other
// record are kept as they are.
var renamedIDs = map[uint16]bool{1: true, 2: true, 3: true, 4: true, 6: true, 16: true, 17: true, 18: true, 21: true, 22: true, 25: true}

func (n Naming) subfamily(italic bool) string {
	switch {
	case n.Bold && italic:
		return "Bold Italic"
	case n.Bold:
		return "Bold"
	case italic:
		return "Italic"
	}
	return "Regular"
}

// applyNaming rewrites name, OS/2 and head in tables for n. The head table
// must be a copy the caller owns.
func applyNaming(tables map[string][]byte, n Naming) error {
	if n.Family == "" {
		return errors.New("naming: empty family")
	}
	italic := false
	if os2 := tables["OS/2"]; os2 != nil {
		if len(os2) < 64 {
			return errors.New("short OS/2 table")
		}
		os2 = append([]byte(nil), os2...)
		sel := u16(os2, 62)
		italic = sel&0x0001 != 0
		weight := uint16(400)
		sel &^= 0x0020 | 0x0040
		if n.Bold {
			weight = 700
			sel |= 0x0020
		} else if !italic {
			sel |= 0x0040
		}
		binary.BigEndian.PutUint16(os2[4:], weight)
		binary.BigEndian.PutUint16(os2[62:], sel)
		tables["OS/2"] = os2
	}
	head := tables["head"]
	style := u16(head, 44) &^ 0x0001
	if n.Bold {
		style |= 0x0001
	}
	binary.BigEndian.PutUint16(head[44:], style)

	sub := n.subfamily(italic)
	ps := strings.ReplaceAll(n.Family, " ", "") + "-" + strings.ReplaceAll(sub, " ", "")
	name, err := renameTable(tables["name"], map[uint16]string{
		1: n.Family,
		2: sub,
		3: ps,
		4: n.Family + " " + sub,
		6: ps,
	})
	if err != nil {
		return err
	}
	tables["name"] = name
	return nil
}

type nameRecord struct {
	pid, eid, lang, id uint16
	str                []byte
}

// renameTable returns a name table without the records of renamedIDs and
// with a Windows Unicode English (US) record for each entry of names. Format
// 1 language tags are kept. A missing table gives a table with only names.
func renameTable(t []byte, names map[uint16]string) ([]byte, error) {
	var recs []nameRecord
	var tags [][]byte
	format := uint16(0)
	if t != nil {
		if len(t) < 6 {
			return nil, errors.New("short name table")
		}
		format = u16(t, 0)
		count, strOff := int(u16(t, 2)), int(u16(t, 4))
		if 6+12*count > len(t) {
			return nil, errors.New("truncated name records")
		}
		str := func(length, off int) ([]byte, error) {
			if strOff+off+length > len(t) {
				return nil, errors.New("name string lies outside the table")
			}
			return t[strOff+off : strOff+off+length], nil
		}
		for i := 0; i < count; i++ {
			r := 6 + 12*i
			id := u16(t, r+6)
			if renamedIDs[id] {
				continue
			}
			s, err := str(int(u16(t, r+8)), int(u16(t, r+10)))
			if err != nil {
				return nil, err
			}
			recs = append(recs, nameRecord{u16(t, r), u16(t, r+2), u16(t, r+4), id, s})
		}
		if format == 1 {
			at := 6 + 12*count
			if at+2 > len(t) {
				return nil, errors.New("truncated name language tags")
			}
			n := int(u16(t, at))
			if at+2+4*n > len(t) {
				return nil, errors.New("truncated name language tags")
			}
			for i := 0; i < n; i++ {
				s, err := str(int(u16(t, at+2+4*i)), int(u16(t, at+4+4*i)))
				if err != nil {
					return nil, err
				}
				tags = append(tags, s)
			}
		}
	}
	for id, s := range names {
		u := utf16.Encode([]rune(s))
		b := make([]byte, 2*len(u))
		for i, c := range u {
			binary.BigEndian.PutUint16(b[2*i:], c)
		}
		recs = append(recs, nameRecord{3, 1, 0x0409, id, b})
	}
	sort.SliceStable(recs, func(i, j int) bool {
		a, b := recs[i], recs[j]
		if a.pid != b.pid {
			return a.pid < b.pid
		}
		if a.eid != b.eid {
			return a.eid < b.eid
		}
		if a.lang != b.lang {
			return a.lang < b.lang
		}
		return a.id < b.id
	})

	hdr := 6 + 12*len(recs)
	if format == 1 {
		hdr += 2 + 4*len(tags)
	}
	out := make([]byte, hdr)
	put := func(i int, v int) { binary.BigEndian.PutUint16(out[i:], uint16(v)) }
	put(0, int(format))
	put(2, len(recs))
	put(4, hdr)
	var storage []byte
	for i, r := range recs {
		at := 6 + 12*i
		put(at, int(r.pid))
		put(at+2, int(r.eid))
		put(at+4, int(r.lang))
		put(at+6, int(r.id))
		put(at+8, len(r.str))
		put(at+10, len(storage))
		storage = append(storage, r.str...)
	}
	if format == 1 {
		at := 6 + 12*len(recs)
		put(at, len(tags))
		for i, s := range tags {
			put(at+2+4*i, len(s))
			put(at+4+4*i, len(storage))
			storage = append(storage, s...)
		}
	}
	if hdr > 0xFFFF || len(storage) > 0xFFFF {
		return nil, errors.New("name table strings exceed 64 KB")
	}
	return append(out, storage...), nil
}
