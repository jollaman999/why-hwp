package svgtool

import (
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jollaman999/why-hwp/internal/sfnt"
)

// DefaultFamily is the font-family name given to the embedded font.
const DefaultFamily = "WhyHwpEmbed"

// EmbeddedStyleID is the id of the <style> element that holds the font. An
// existing element with this id is replaced, so embedding twice gives the
// same file.
const EmbeddedStyleID = "embedded-font"

// Weight is a CSS font weight the embedded font is declared for.
type Weight int

// The two weights an SVG figure is drawn with.
const (
	Regular Weight = 400
	Bold    Weight = 700
)

func (w Weight) String() string {
	if w == Bold {
		return "bold"
	}
	return "regular"
}

// isBold reports whether a font-weight value draws bold: bold, bolder or a
// number of 600 or more.
func isBold(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "bold", "bolder":
		return true
	}
	n, err := strconv.ParseFloat(v, 64)
	return err == nil && n >= 600
}

// weightOf returns the weight of n from its own or nearest ancestor's
// font-weight.
func weightOf(n *node) Weight {
	if v, ok := n.inherited("font-weight"); ok && isBold(v) {
		return Bold
	}
	return Regular
}

// Collect returns, per weight, the characters drawn by <text> elements and
// their descendants (tspan, textPath), entities decoded. A weight that has
// any text also gets a space. The runes are sorted.
func Collect(svg []byte) (map[Weight][]rune, error) {
	root, err := parseTree(svg)
	if err != nil {
		return nil, err
	}
	sets := map[Weight]map[rune]bool{}
	var rec func(n *node, inText bool)
	rec = func(n *node, inText bool) {
		switch n.name {
		case "text":
			inText = true
		case "style", "script":
			return
		}
		w := weightOf(n)
		for _, it := range n.items {
			if it.child != nil {
				rec(it.child, inText)
				continue
			}
			if !inText {
				continue
			}
			for _, r := range it.text {
				if r < 0x20 || r == 0x7F {
					continue
				}
				if sets[w] == nil {
					sets[w] = map[rune]bool{' ': true}
				}
				sets[w][r] = true
			}
		}
	}
	rec(root, false)
	out := map[Weight][]rune{}
	for w, set := range sets {
		rs := make([]rune, 0, len(set))
		for r := range set {
			rs = append(rs, r)
		}
		sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
		out[w] = rs
	}
	return out, nil
}

// Face is one embedded font file.
type Face struct {
	Weight Weight
	TTF    []byte
}

// SubsetFace writes the subset of f holding runes for weight w. The subset's
// name table gives family as its family and w as its weight, the same as the
// @font-face rule Embed writes, so renderers that ignore @font-face and pick
// fonts by their own names (resvg) find it too.
func SubsetFace(f *sfnt.Font, w Weight, family string, runes []rune) (Face, *sfnt.SubsetResult, error) {
	sub, err := f.SubsetAs(runes, sfnt.Naming{Family: family, Bold: w == Bold})
	if err != nil {
		return Face{}, nil, err
	}
	return Face{Weight: w, TTF: sub.Font}, sub, nil
}

var familyName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]*$`)

// ValidFamily reports whether name can be used as the embedded family name.
func ValidFamily(name string) bool { return familyName.MatchString(name) }

// Embed returns the SVG with the faces in <style id="embedded-font"> at the
// start of <defs> (created when missing) and the family name put first in
// every font-family attribute and font-family declaration. A previous
// embedded style is removed first, so the result does not depend on whether
// the input was embedded before. Everything else is kept byte for byte.
func Embed(svg []byte, family string, faces []Face) ([]byte, error) {
	if !ValidFamily(family) {
		return nil, fmt.Errorf("invalid font family name %q (use letters, digits, space, _ and -)", family)
	}
	root, err := parseTree(svg)
	if err != nil {
		return nil, err
	}
	if root.selfClosing {
		return nil, errors.New("the <svg> element is empty")
	}
	var edits []edit
	var defs *node
	root.walk(func(n *node) bool {
		if n.name == "style" && n.attrs["id"] == EmbeddedStyleID {
			edits = append(edits, edit{n.tagStart, n.end, ""})
			return false
		}
		if n.name == "defs" && defs == nil && n.parent == root {
			defs = n
		}
		if tag, ok := rewriteTag(string(svg[n.tagStart:n.tagEnd]), family); ok {
			edits = append(edits, edit{n.tagStart, n.tagEnd, tag})
		}
		if n.name == "style" && !n.selfClosing {
			css := string(svg[n.tagEnd:n.contentEnd])
			if out, ok := prefixCSS(css, family, '\''); ok {
				edits = append(edits, edit{n.tagEnd, n.contentEnd, out})
			}
		}
		return true
	})
	if defs == nil {
		root.walk(func(n *node) bool {
			if n.name == "defs" && defs == nil {
				defs = n
			}
			return defs == nil
		})
	}

	style := styleElement(family, faces)
	switch {
	case defs == nil:
		edits = append(edits, edit{root.tagEnd, root.tagEnd, "<defs>" + style + "</defs>"})
	case defs.selfClosing:
		tag := string(svg[defs.tagStart:defs.tagEnd])
		if r, ok := rewriteTag(tag, family); ok {
			tag = r
		}
		tag = strings.TrimRight(tag, " \t\r\n")
		tag = strings.TrimRight(strings.TrimSuffix(tag, "/>"), " \t\r\n") + ">"
		edits = replaceEdit(edits, edit{defs.tagStart, defs.tagEnd, tag + style + "</defs>"})
	default:
		edits = append(edits, edit{defs.tagEnd, defs.tagEnd, style})
	}
	return applyEdits(svg, edits)
}

// styleElement writes the @font-face rules, regular first.
func styleElement(family string, faces []Face) string {
	sorted := append([]Face(nil), faces...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Weight < sorted[j].Weight })
	var b strings.Builder
	b.WriteString(`<style id="` + EmbeddedStyleID + `">`)
	for _, f := range sorted {
		fmt.Fprintf(&b, `@font-face{font-family:'%s';font-weight:%d;src:url("data:font/ttf;base64,%s") format('truetype')}`,
			family, int(f.Weight), BreakKeyLike(base64.StdEncoding.EncodeToString(f.TTF)))
	}
	b.WriteString(`</style>`)
	return b.String()
}

var keyLike = regexp.MustCompile(`(?i)AKIA[0-9A-Z]{16}`)

// BreakKeyLike splits every run of the base64 text that looks like a cloud
// access key (AKIA and 16 letters or digits, any case), which commit hooks
// take for a secret. A backslash and a line break inside a CSS string is a
// line continuation and not part of the value, so the font data does not
// change.
func BreakKeyLike(b64 string) string {
	var b strings.Builder
	rest := b64
	for {
		loc := keyLike.FindStringIndex(rest)
		if loc == nil {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:loc[0]+2])
		b.WriteString("\\\n")
		rest = rest[loc[0]+2:]
	}
}

type edit struct {
	start, end int
	text       string
}

func replaceEdit(edits []edit, e edit) []edit {
	for i := range edits {
		if edits[i].start == e.start && edits[i].end == e.end {
			edits[i] = e
			return edits
		}
	}
	return append(edits, e)
}

func applyEdits(src []byte, edits []edit) ([]byte, error) {
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start < edits[j].start
		}
		return edits[i].end < edits[j].end
	})
	var out []byte
	at := 0
	for _, e := range edits {
		if e.start < at {
			return nil, fmt.Errorf("internal error: overlapping edits at byte %d", e.start)
		}
		out = append(out, src[at:e.start]...)
		out = append(out, e.text...)
		at = e.end
	}
	return append(out, src[at:]...), nil
}

var (
	familyAttr = regexp.MustCompile(`(\sfont-family\s*=\s*)("[^"]*"|'[^']*')`)
	styleAttr  = regexp.MustCompile(`(\sstyle\s*=\s*)("[^"]*"|'[^']*')`)
	familyDecl = regexp.MustCompile(`(?i)(font-family\s*:\s*)([^;}]*)`)
	fontFace   = regexp.MustCompile(`(?is)@font-face\s*\{[^}]*\}`)
)

// rewriteTag puts the family first in the font-family attribute and in
// font-family declarations of the style attribute of one start tag.
func rewriteTag(tag, family string) (string, bool) {
	changed := false
	tag = familyAttr.ReplaceAllStringFunc(tag, func(m string) string {
		sub := familyAttr.FindStringSubmatch(m)
		q := sub[2][0]
		val := sub[2][1 : len(sub[2])-1]
		nv, ok := prefixFamily(val, family, other(q))
		if !ok {
			return m
		}
		changed = true
		return sub[1] + string(q) + nv + string(q)
	})
	tag = styleAttr.ReplaceAllStringFunc(tag, func(m string) string {
		sub := styleAttr.FindStringSubmatch(m)
		q := sub[2][0]
		val := sub[2][1 : len(sub[2])-1]
		nv, ok := prefixCSS(val, family, other(q))
		if !ok {
			return m
		}
		changed = true
		return sub[1] + string(q) + nv + string(q)
	})
	return tag, changed
}

func other(q byte) byte {
	if q == '"' {
		return '\''
	}
	return '"'
}

// prefixCSS puts the family first in every font-family declaration of css,
// leaving @font-face rules alone.
func prefixCSS(css, family string, quote byte) (string, bool) {
	skip := fontFace.FindAllStringIndex(css, -1)
	changed := false
	var b strings.Builder
	at := 0
	for _, m := range familyDecl.FindAllStringSubmatchIndex(css, -1) {
		inFace := false
		for _, s := range skip {
			if m[0] >= s[0] && m[0] < s[1] {
				inFace = true
			}
		}
		if inFace {
			continue
		}
		nv, ok := prefixFamily(css[m[4]:m[5]], family, quote)
		if !ok {
			continue
		}
		b.WriteString(css[at:m[4]])
		b.WriteString(nv)
		at = m[5]
		changed = true
	}
	if !changed {
		return css, false
	}
	b.WriteString(css[at:])
	return b.String(), true
}

// prefixFamily returns the family list with family in front, quoted with
// quote, or false when the list already names it.
func prefixFamily(list, family string, quote byte) (string, bool) {
	for _, f := range strings.Split(html.UnescapeString(list), ",") {
		f = strings.TrimSpace(f)
		f = strings.TrimSpace(strings.TrimSuffix(f, "!important"))
		f = strings.Trim(f, `"'`)
		if strings.EqualFold(f, family) {
			return list, false
		}
	}
	q := string(quote)
	rest := strings.TrimLeft(list, " \t\r\n")
	if strings.TrimSpace(rest) == "" {
		return q + family + q, true
	}
	return q + family + q + ", " + rest, true
}
