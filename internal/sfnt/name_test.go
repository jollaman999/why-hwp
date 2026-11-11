package sfnt

import (
	"bytes"
	"testing"
)

// nameIDs returns every name ID in the name table with how many records it
// has.
func nameIDs(t *testing.T, f *Font) map[int]int {
	t.Helper()
	tb := f.Table("name")
	if len(tb) < 6 {
		t.Fatal("no name table")
	}
	ids := map[int]int{}
	for i := 0; i < int(u16(tb, 2)); i++ {
		ids[int(u16(tb, 6+12*i+6))]++
	}
	return ids
}

func TestSubsetAs(t *testing.T) {
	f, _ := readFixture(t)
	for _, c := range []struct {
		bold            bool
		sub             string
		weight          uint16
		boldBit, regBit bool
	}{
		{false, "Regular", 400, false, true},
		{true, "Bold", 700, true, false},
	} {
		res, err := f.SubsetAs([]rune("Hello"), Naming{Family: "Why Hwp", Bold: c.bold})
		if err != nil {
			t.Fatal(err)
		}
		again, err := f.SubsetAs([]rune("Hello"), Naming{Family: "Why Hwp", Bold: c.bold})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(res.Font, again.Font) {
			t.Errorf("bold %v: two runs differ", c.bold)
		}
		nf, err := Parse(res.Font)
		if err != nil {
			t.Fatalf("bold %v: subset does not parse: %v", c.bold, err)
		}
		ps := "WhyHwp-" + c.sub
		for id, want := range map[int]string{1: "Why Hwp", 2: c.sub, 3: ps, 4: "Why Hwp " + c.sub, 6: ps} {
			if got := nf.Name(id); got != want {
				t.Errorf("bold %v: name ID %d = %q, want %q", c.bold, id, got, want)
			}
		}
		for _, id := range []int{0, 5, 7, 13, 14} {
			if got, want := nf.Name(id), f.Name(id); got != want {
				t.Errorf("bold %v: name ID %d = %q, want it kept as %q", c.bold, id, got, want)
			}
		}
		if nf.Name(13) == "" {
			t.Error("licence (name ID 13) lost")
		}
		ids := nameIDs(t, nf)
		for _, id := range []int{1, 2, 3, 4, 6} {
			if ids[id] != 1 {
				t.Errorf("bold %v: %d records of name ID %d, want 1", c.bold, ids[id], id)
			}
		}
		for _, id := range []int{16, 17, 18, 21, 22, 25} {
			if ids[id] != 0 {
				t.Errorf("bold %v: name ID %d kept", c.bold, id)
			}
		}
		if before, after := nameIDs(t, f)[13], ids[13]; before != after {
			t.Errorf("bold %v: %d licence records, want %d", c.bold, after, before)
		}

		os2 := nf.Table("OS/2")
		if got := u16(os2, 4); got != c.weight {
			t.Errorf("bold %v: usWeightClass %d, want %d", c.bold, got, c.weight)
		}
		sel := u16(os2, 62)
		if sel&0x20 != 0 != c.boldBit || sel&0x40 != 0 != c.regBit {
			t.Errorf("bold %v: fsSelection %#x", c.bold, sel)
		}
		if got := u16(nf.Table("head"), 44)&1 != 0; got != c.bold {
			t.Errorf("bold %v: macStyle bold bit %v", c.bold, got)
		}
		if got := string(nf.Runes()); got != "Helo" {
			t.Errorf("bold %v: maps %q, want %q", c.bold, got, "Helo")
		}
	}

	plain, err := f.Subset([]rune("Hello"))
	if err != nil {
		t.Fatal(err)
	}
	pf, err := Parse(plain.Font)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pf.Name(1), f.Name(1); got != want {
		t.Errorf("Subset changed the family to %q, want %q", got, want)
	}
	if _, err := f.SubsetAs([]rune("A"), Naming{}); err == nil {
		t.Error("empty family accepted")
	}
}

func TestRenameTableFormat1(t *testing.T) {
	// Format 1 with one record of ID 1 (dropped), one of ID 13 using a
	// language tag (kept), and the tag "ko".
	in := []byte{
		0, 1, 0, 2, 0, 36, // format 1, 2 records, strings at 36
		0, 3, 0, 1, 0x04, 0x09, 0, 1, 0, 2, 0, 0, // ID 1 "A"
		0, 3, 0, 1, 0x80, 0x00, 0, 13, 0, 2, 0, 2, // ID 13 "L", language tag 0
		0, 1, 0, 4, 0, 4, // 1 tag: "ko" at 4
		0, 'A', 0, 'L', 0, 'k', 0, 'o',
	}
	out, err := renameTable(in, map[uint16]string{1: "N"})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0, 1, 0, 2, 0, 36,
		0, 3, 0, 1, 0x04, 0x09, 0, 1, 0, 2, 0, 0, // ID 1 "N"
		0, 3, 0, 1, 0x80, 0x00, 0, 13, 0, 2, 0, 2, // ID 13 "L"
		0, 1, 0, 4, 0, 4, // tag "ko"
		0, 'N', 0, 'L', 0, 'k', 0, 'o',
	}
	if !bytes.Equal(out, want) {
		t.Errorf("got  % x\nwant % x", out, want)
	}
}
