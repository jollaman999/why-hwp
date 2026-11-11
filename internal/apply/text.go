package apply

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// atom is one piece of the content of an hp:t: a character (plain,
// entity or a character element such as hp:tab), a change-tracking
// marker, or another element that is kept as it is.
type atom struct {
	raw  string
	kind atomKind
	ch   rune
	del  bool // already inside a tracked deletion in the input

	mark  bool     // newly deleted (tracked)
	drop  bool     // removed (not tracked)
	after []string // XML written right after this atom
}

type atomKind int

const (
	atomChar atomKind = iota
	atomMarker
	atomOther
)

// tcontent is the parsed content of one hp:t of a paragraph.
type tcontent struct {
	atoms   []atom
	head    []string // XML written before the first atom
	changed bool
}

// parseT splits the raw content of an hp:t into atoms.
func parseT(raw string) ([]atom, error) {
	var out []atom
	for i := 0; i < len(raw); {
		switch raw[i] {
		case '<':
			n, local, err := elementLen(raw[i:])
			if err != nil {
				return nil, err
			}
			a := atom{raw: raw[i : i+n], kind: atomOther}
			switch local {
			case "insertBegin", "insertEnd", "deleteBegin", "deleteEnd":
				a.kind = atomMarker
			case "tab":
				a.kind, a.ch = atomChar, '\t'
			case "lineBreak":
				a.kind, a.ch = atomChar, '\n'
			case "nbSpace", "fwSpace":
				a.kind, a.ch = atomChar, ' '
			case "hyphen":
				a.kind, a.ch = atomChar, '-'
			}
			out = append(out, a)
			i += n
		case '&':
			j := strings.IndexByte(raw[i:], ';')
			if j < 0 {
				return nil, fmt.Errorf("bad entity in text")
			}
			r, ok := entity(raw[i+1 : i+j])
			if !ok {
				return nil, fmt.Errorf("unknown entity %q in text", raw[i:i+j+1])
			}
			out = append(out, atom{raw: raw[i : i+j+1], kind: atomChar, ch: r})
			i += j + 1
		default:
			r, n := utf8.DecodeRuneInString(raw[i:])
			out = append(out, atom{raw: raw[i : i+n], kind: atomChar, ch: r})
			i += n
		}
	}
	return out, nil
}

// elementLen returns the byte length of the element (or comment) that
// starts s, and its local name.
func elementLen(s string) (int, string, error) {
	if strings.HasPrefix(s, "<!--") {
		j := strings.Index(s, "-->")
		if j < 0 {
			return 0, "", fmt.Errorf("unterminated comment in text")
		}
		return j + 3, "", nil
	}
	if strings.HasPrefix(s, "<!") || strings.HasPrefix(s, "<?") || strings.HasPrefix(s, "</") {
		return 0, "", fmt.Errorf("unexpected markup %.20q in text", s)
	}
	end := tagEnd(s)
	if end < 0 {
		return 0, "", fmt.Errorf("unterminated tag in text")
	}
	name := s[1:]
	if k := strings.IndexAny(name, " \t\r\n/>"); k >= 0 {
		name = name[:k]
	}
	local := name
	if k := strings.IndexByte(local, ':'); k >= 0 {
		local = local[k+1:]
	}
	if s[end-2] == '/' {
		return end, local, nil
	}
	// Find the matching end tag, counting nested elements of the same name.
	depth := 1
	i := end
	for depth > 0 {
		k := strings.IndexByte(s[i:], '<')
		if k < 0 {
			return 0, "", fmt.Errorf("unterminated %s in text", name)
		}
		i += k
		e := tagEnd(s[i:])
		if e < 0 {
			return 0, "", fmt.Errorf("unterminated tag in text")
		}
		tag := s[i : i+e]
		switch {
		case tag == "</"+name+">" || strings.HasPrefix(tag, "</"+name+" "):
			depth--
		case strings.HasPrefix(tag, "<"+name+" ") || strings.HasPrefix(tag, "<"+name+">"):
			if tag[len(tag)-2] != '/' {
				depth++
			}
		}
		i += e
	}
	return i, local, nil
}

// tagEnd returns the length of the tag that starts s, skipping '>' inside
// quoted attribute values, or -1.
func tagEnd(s string) int {
	var quote byte
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i + 1
		}
	}
	return -1
}

func entity(name string) (rune, bool) {
	switch name {
	case "amp":
		return '&', true
	case "lt":
		return '<', true
	case "gt":
		return '>', true
	case "quot":
		return '"', true
	case "apos":
		return '\'', true
	}
	if strings.HasPrefix(name, "#x") || strings.HasPrefix(name, "#X") {
		v, err := strconv.ParseUint(name[2:], 16, 32)
		return rune(v), err == nil
	}
	if strings.HasPrefix(name, "#") {
		v, err := strconv.ParseUint(name[1:], 10, 32)
		return rune(v), err == nil
	}
	return 0, false
}

// textXML turns plain text into hp:t content: markup characters escaped,
// "\n" as a line break and "\t" as a tab element.
func textXML(pfx, s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\n':
			b.WriteString("<" + pfx + ":lineBreak/>")
		case '\t':
			b.WriteString("<" + pfx + ":tab/>")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func escText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func escAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
