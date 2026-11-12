package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// glyph is one character of a PDF as mutool reports it: its origin (the
// point on the baseline where the glyph starts, y growing downwards) and
// whether its font is a Type 3 font.
type glyph struct {
	Page  int
	X, Y  float64
	R     rune
	Font  string
	Type3 bool
}

// errNoMutool is returned when mutool is not installed.
var errNoMutool = errors.New("mutool not found")

// type3Line matches a Type 3 row of pdffonts: name, type, encoding, the
// three yes/no columns and the object ID.
var type3Line = regexp.MustCompile(`^(.*?)\s+Type 3\s+\S+\s+(?:yes|no)\s+(?:yes|no)\s+(?:yes|no)\s+\d+\s+\d+\s*$`)

// parseType3Names returns the names of the Type 3 fonts in pdffonts output.
func parseType3Names(r io.Reader) []string {
	var names []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		m := type3Line.FindStringSubmatch(sc.Text())
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		names = append(names, m[1])
	}
	return names
}

// readType3Names runs pdffonts on a PDF file.
func readType3Names(path string) ([]string, error) {
	c := exec.Command("pdffonts", path)
	var out, errb bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("pdffonts %s: %w: %s", path, err, strings.TrimSpace(errb.String()))
	}
	return parseType3Names(&out), nil
}

// readGlyphs runs mutool draw -F stext on a PDF file. Fonts named in type3
// are marked as Type 3.
func readGlyphs(path string, type3 []string) ([]glyph, error) {
	if _, err := exec.LookPath("mutool"); err != nil {
		return nil, errNoMutool
	}
	c := exec.Command("mutool", "draw", "-F", "stext", "-o", "-", path)
	var out, errb bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("mutool %s: %w: %s", path, err, lastLine(errb.String()))
	}
	return parseStext(&out, type3)
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// parseStext reads the structured text XML of mutool. mutool writes one
// element per line, and a character may be a code that XML does not allow,
// so the lines are read as they are and not with an XML parser.
func parseStext(r io.Reader, type3 []string) ([]glyph, error) {
	t3 := map[string]bool{}
	for _, n := range type3 {
		t3[n] = true
	}
	var out []glyph
	page := -1
	font := ""
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<26)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "<page "):
			page++
		case strings.HasPrefix(line, "<font "):
			font, _ = xmlAttr(line, "name")
		case strings.HasPrefix(line, "<char "):
			if page < 0 {
				return nil, fmt.Errorf("mutool output: character outside a page")
			}
			xs, okx := xmlAttr(line, "x")
			ys, oky := xmlAttr(line, "y")
			cs, okc := xmlAttr(line, "c")
			if !okx || !oky || !okc {
				return nil, fmt.Errorf("mutool output: <char> without x, y or c")
			}
			x, err1 := strconv.ParseFloat(xs, 64)
			y, err2 := strconv.ParseFloat(ys, 64)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("mutool output: bad character origin %q %q", xs, ys)
			}
			rs := []rune(cs)
			if len(rs) == 0 {
				continue
			}
			out = append(out, glyph{Page: page, X: x, Y: y, R: rs[0], Font: font, Type3: t3[font]})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("mutool output: %w", err)
	}
	if page < 0 {
		return nil, fmt.Errorf("mutool output: no page")
	}
	return out, nil
}

// xmlAttr returns the decoded value of attribute name in a one-line element.
func xmlAttr(line, name string) (string, bool) {
	i := strings.Index(line, " "+name+`="`)
	if i < 0 {
		return "", false
	}
	v := line[i+len(name)+3:]
	j := strings.IndexByte(v, '"')
	if j < 0 {
		return "", false
	}
	return unescapeXML(v[:j]), true
}

var xmlNamed = map[string]string{"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'"}

func unescapeXML(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	for len(s) > 0 {
		i := strings.IndexByte(s, '&')
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		s = s[i:]
		j := strings.IndexByte(s, ';')
		if j < 0 {
			b.WriteString(s)
			break
		}
		ent := s[1:j]
		switch {
		case strings.HasPrefix(ent, "#x") || strings.HasPrefix(ent, "#X"):
			if n, err := strconv.ParseUint(ent[2:], 16, 32); err == nil {
				b.WriteRune(rune(n))
			}
		case strings.HasPrefix(ent, "#"):
			if n, err := strconv.ParseUint(ent[1:], 10, 32); err == nil {
				b.WriteRune(rune(n))
			}
		default:
			if v, ok := xmlNamed[ent]; ok {
				b.WriteString(v)
			} else {
				b.WriteString(s[:j+1])
			}
		}
		s = s[j+1:]
	}
	return b.String()
}

// originTol is how far (in points) the origin of a word's first glyph may
// be from the left edge of the word box. The two tools round differently.
const originTol = 0.5

// defaultBaseFrac is where the baseline sits in a word box, as a fraction
// of the box height from the top, for a font that has no word to learn it
// from. pdftotext builds the box from the font's ascent and descent, and
// 0.95 above and 0.35 below the baseline are its values for a font that
// states none.
const defaultBaseFrac = 0.95 / (0.95 + 0.35)

// attachBaselines gives every word the baseline of its first glyph and
// marks the words that hold a Type 3 glyph. It returns the number of words
// with a baseline.
//
// The first glyph of a word is a glyph with the word's first character
// whose origin is at the left edge of the word box and inside the box
// vertically. The box of a Type 3 word is tall enough to hold the same
// character at the same x on the line above. pdftotext puts the baseline
// at the same fraction of the box height for every word of a font, so the
// fraction is learned for each font from the words with one such glyph and
// the other words take the glyph nearest to it.
func attachBaselines(d *pdfWords, glyphs []glyph) int {
	byPage := map[int][]int{}
	for i, g := range glyphs {
		byPage[g.Page] = append(byPage[g.Page], i)
	}
	for _, idx := range byPage {
		sort.SliceStable(idx, func(a, b int) bool { return glyphs[idx[a]].X < glyphs[idx[b]].X })
	}
	frac := func(w word, g glyph) float64 {
		if w.Y1 <= w.Y0 {
			return defaultBaseFrac
		}
		return (g.Y - w.Y0) / (w.Y1 - w.Y0)
	}
	cands := make([][]int, len(d.Words))
	samples := map[string][]float64{}
	for wi, w := range d.Words {
		rs := []rune(w.Text)
		if len(rs) == 0 {
			continue
		}
		idx := byPage[w.Page]
		lo := sort.Search(len(idx), func(k int) bool { return glyphs[idx[k]].X >= w.X0-originTol })
		for k := lo; k < len(idx) && glyphs[idx[k]].X <= w.X0+originTol; k++ {
			g := glyphs[idx[k]]
			if g.R == rs[0] && g.Y >= w.Y0-originTol && g.Y <= w.Y1+originTol {
				cands[wi] = append(cands[wi], idx[k])
			}
		}
		if len(cands[wi]) == 1 {
			g := glyphs[cands[wi][0]]
			samples[g.Font] = append(samples[g.Font], frac(w, g))
		}
	}
	learned := map[string]float64{}
	for font, v := range samples {
		sort.Float64s(v)
		learned[font] = v[len(v)/2]
	}
	d.Base = make([]wordBase, len(d.Words))
	n := 0
	for wi, w := range d.Words {
		best, bestD := -1, 0.0
		for _, gi := range cands[wi] {
			g := glyphs[gi]
			want, ok := learned[g.Font]
			if !ok {
				want = defaultBaseFrac
			}
			if dist := math.Abs(frac(w, g) - want); best < 0 || dist < bestD {
				best, bestD = gi, dist
			}
		}
		if best < 0 {
			continue
		}
		wb := wordBase{Y: glyphs[best].Y, OK: true}
		n++
		// Every glyph of the word sits on the same baseline between the
		// word's left and right edge.
		idx := byPage[w.Page]
		lo := sort.Search(len(idx), func(k int) bool { return glyphs[idx[k]].X >= w.X0-originTol })
		for k := lo; k < len(idx) && glyphs[idx[k]].X < w.X1; k++ {
			g := glyphs[idx[k]]
			if g.Type3 && math.Abs(g.Y-wb.Y) < 0.05 {
				wb.Type3 = true
				break
			}
		}
		d.Base[wi] = wb
	}
	return n
}

// loadBaselines attaches baselines to both documents when one of the two
// PDFs has a Type 3 font. It returns why that could not be done, or "".
// Documents without Type 3 fonts are left as they are and mutool is not
// run for them.
func loadBaselines(refPath, candPath string, ref, cand *pdfWords) string {
	refT3, err := readType3Names(refPath)
	if err != nil {
		return err.Error()
	}
	candT3, err := readType3Names(candPath)
	if err != nil {
		return err.Error()
	}
	if len(refT3) == 0 && len(candT3) == 0 {
		return ""
	}
	for _, side := range []struct {
		path string
		d    *pdfWords
		t3   []string
	}{{refPath, ref, refT3}, {candPath, cand, candT3}} {
		if side.d.Base != nil {
			continue
		}
		glyphs, err := readGlyphs(side.path, side.t3)
		if err != nil {
			return err.Error()
		}
		if len(side.t3) > 0 && !anyType3(glyphs) {
			// pdffonts and mutool name the font differently, so its
			// words cannot be told apart.
			return fmt.Sprintf("mutool shows no text in the Type 3 fonts %s of %s", strings.Join(side.t3, ", "), side.path)
		}
		attachBaselines(side.d, glyphs)
	}
	return ""
}

func anyType3(glyphs []glyph) bool {
	for _, g := range glyphs {
		if g.Type3 {
			return true
		}
	}
	return false
}
