package model

import (
	"fmt"
	"strings"
)

// View selects how tracked changes are shown.
type View string

const (
	// ViewAccepted shows the document with every change accepted:
	// inserted text is kept and deleted text is left out. Apart from line
	// breaks and spacing this is what Hancom Office writes when it saves
	// such a document as text.
	ViewAccepted View = "accepted"
	// ViewOriginal shows the document with every change rejected:
	// deleted text is kept and inserted text is left out.
	ViewOriginal View = "original"
	// ViewMarkup shows both, inserted text as [+text+] and deleted text
	// as [-text-]. A changed paragraph break is shown as "\n" inside the
	// marks.
	ViewMarkup View = "markup"
)

// Views lists every view, default first.
var Views = []View{ViewAccepted, ViewOriginal, ViewMarkup}

// ParseView checks a view name.
func ParseView(s string) (View, error) {
	for _, v := range Views {
		if string(v) == s {
			return v, nil
		}
	}
	return "", fmt.Errorf("unknown view %q (want accepted, original or markup)", s)
}

// Change tells whether text is part of a tracked insertion, a tracked
// deletion, or both (inserted by one change and deleted by a later one).
type Change uint8

const (
	Inserted Change = 1 << iota
	Deleted
)

// hides reports whether v leaves out text with change c.
func (v View) hides(c Change) bool {
	switch v {
	case ViewAccepted:
		return c&Deleted != 0
	case ViewOriginal:
		return c&Inserted != 0
	}
	return false
}

// Span is a run of text with one change.
type Span struct {
	Text   string
	Change Change
}

// Spans is text split by change.
type Spans []Span

// Add appends text with change c, joining it to the last span when that
// has the same change.
func (s *Spans) Add(text string, c Change) {
	if text == "" {
		return
	}
	if n := len(*s); n > 0 && (*s)[n-1].Change == c {
		(*s)[n-1].Text += text
		return
	}
	*s = append(*s, Span{text, c})
}

// Append adds every span of t.
func (s *Spans) Append(t Spans) {
	for _, sp := range t {
		s.Add(sp.Text, sp.Change)
	}
}

// Len is the length of all spans in bytes.
func (s Spans) Len() int {
	n := 0
	for _, sp := range s {
		n += len(sp.Text)
	}
	return n
}

// Changed reports whether any span is part of a tracked change.
func (s Spans) Changed() bool {
	for _, sp := range s {
		if sp.Change != 0 {
			return true
		}
	}
	return false
}

// Text returns the spans as shown in view v.
func (s Spans) Text(v View) string {
	var b strings.Builder
	for _, sp := range s {
		switch {
		case v == ViewMarkup:
			b.WriteString(markup(sp))
		case !v.hides(sp.Change):
			b.WriteString(sp.Text)
		}
	}
	return b.String()
}

func markup(sp Span) string {
	switch sp.Change {
	case Inserted:
		return "[+" + sp.Text + "+]"
	case Deleted:
		return "[-" + sp.Text + "-]"
	case Inserted | Deleted:
		return "[-[+" + sp.Text + "+]-]"
	}
	return sp.Text
}

// spans returns b's text split by change.
func (b Block) spans() Spans {
	if b.Spans != nil {
		return b.Spans
	}
	if b.Text == "" {
		return nil
	}
	return Spans{{b.Text, 0}}
}

// InView returns a copy of d showing view v. For a reader without views it
// returns d unchanged.
//
// In the markup view every block stays. In the accepted and original
// views a paragraph whose break the view leaves out is joined to the next
// paragraph of the same section, as the editor would show it. The joined
// text keeps the location of the first of those paragraphs that the view
// still has, so a paragraph that only exists in the other view (all of it,
// break included, inside an insertion or deletion) gives up its block and
// no location is reused for a different paragraph.
func (d *Document) InView(v View) *Document {
	if !d.Tracked {
		return d
	}
	out := *d
	out.Summary.View = v
	out.Blocks = make([]Block, 0, len(d.Blocks))
	bs := d.Blocks
	for i := 0; i < len(bs); i++ {
		b := bs[i]
		if b.Kind != KindPara || v == ViewMarkup || !v.hides(b.Break) {
			out.Blocks = append(out.Blocks, b.render(v))
			continue
		}
		members := []int{i}
		for j := i; v.hides(bs[j].Break); {
			k := nextPara(bs, j)
			if k < 0 {
				break
			}
			members = append(members, k)
			j = k
		}
		var text strings.Builder
		keep := members[len(members)-1]
		for _, m := range members {
			text.WriteString(bs[m].spans().Text(v))
		}
		for _, m := range members {
			if !v.hides(bs[m].Whole) {
				keep = m
				break
			}
		}
		last := members[len(members)-1]
		for k := i; k <= last; k++ {
			switch {
			case k == keep:
				out.Blocks = append(out.Blocks, Block{Loc: bs[k].Loc, Kind: KindPara, Text: text.String()})
			case bs[k].Kind != KindPara:
				out.Blocks = append(out.Blocks, bs[k].render(v))
			}
		}
		i = last
	}
	out.Summary.OwnerMarks = FindOwnerMarks(out.Blocks)
	return &out
}

// render returns b alone in view v.
func (b Block) render(v View) Block {
	sp := b.spans()
	if v == ViewMarkup && b.Kind == KindPara && b.Break != 0 {
		sp = append(Spans(nil), sp...)
		sp.Add("\n", b.Break)
	}
	return Block{Loc: b.Loc, Kind: b.Kind, Text: sp.Text(v)}
}

// nextPara returns the index of the paragraph after bs[i] in the same
// section, or -1.
func nextPara(bs []Block, i int) int {
	sec := section(bs[i].Loc)
	for k := i + 1; k < len(bs); k++ {
		if bs[k].Kind != KindPara {
			continue
		}
		if section(bs[k].Loc) != sec {
			return -1
		}
		return k
	}
	return -1
}

func section(loc string) string {
	if i := strings.IndexByte(loc, '/'); i >= 0 {
		return loc[:i]
	}
	return loc
}
