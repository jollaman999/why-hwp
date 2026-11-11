package svgtool

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jollaman999/why-hwp/internal/sfnt"
)

// Severities of a finding.
const (
	Violation = "violation"
	Warning   = "warning"
)

// Finding is one layout problem.
type Finding struct {
	Severity string `json:"severity"`
	Kind     string `json:"kind"` // overflow, overlap, loose-end, crosses-box, star
	Loc      string `json:"loc"`
	Text     string `json:"text,omitempty"`
	Message  string `json:"message"`
}

// CheckReport is the result of Check.
type CheckReport struct {
	Violations int       `json:"violations"`
	Warnings   int       `json:"warnings"`
	Texts      int       `json:"texts"`
	Rects      int       `json:"rects"`
	Arrows     int       `json:"arrows"`
	Findings   []Finding `json:"findings"`
	Notes      []string  `json:"notes"`
}

// CheckOptions configures Check.
type CheckOptions struct {
	// Tol is how far in px an arrow end may be from a box edge.
	Tol float64
	// Fonts measure the text; a weight the SVG uses must be present.
	Fonts map[Weight]*sfnt.Font
}

// NeededWeights returns the weights the SVG draws text with.
func NeededWeights(svg []byte) ([]Weight, error) {
	sets, err := Collect(svg)
	if err != nil {
		return nil, err
	}
	var out []Weight
	for _, w := range []Weight{Regular, Bold} {
		if len(sets[w]) > 0 {
			out = append(out, w)
		}
	}
	return out, nil
}

type box struct{ x0, y0, x1, y1 float64 }

func (b box) w() float64 { return b.x1 - b.x0 }
func (b box) h() float64 { return b.y1 - b.y0 }

func (b box) contains(x, y, pad float64) bool {
	return x >= b.x0-pad && x <= b.x1+pad && y >= b.y0-pad && y <= b.y1+pad
}

type rectInfo struct {
	n     *node
	b     box
	inner box // inside the stroke
}

type textLine struct {
	n      *node
	ax, ay float64 // anchor point
	ink    box
	text   string
}

type point struct{ x, y float64 }

type arrow struct {
	n    *node
	pts  []point // vertices
	segs [][2]point
}

type checker struct {
	opt    CheckOptions
	rep    *CheckReport
	rects  []rectInfo
	lines  []textLine
	arrows []arrow
	notes  map[string]bool
}

// Check measures the text with the fonts and checks the layout. Only
// translate transforms are followed; elements with any other transform are
// skipped and listed in the notes.
func Check(svg []byte, opt CheckOptions) (*CheckReport, error) {
	root, err := parseTree(svg)
	if err != nil {
		return nil, err
	}
	c := &checker{opt: opt, rep: &CheckReport{Findings: []Finding{}, Notes: []string{}}, notes: map[string]bool{}}
	c.collect(root, 0, 0)
	c.rep.Texts, c.rep.Rects, c.rep.Arrows = len(c.lines), len(c.rects), len(c.arrows)
	c.checkText()
	c.checkArrows()
	for _, f := range c.rep.Findings {
		if f.Severity == Violation {
			c.rep.Violations++
		} else {
			c.rep.Warnings++
		}
	}
	return c.rep, nil
}

func (c *checker) note(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	if !c.notes[s] {
		c.notes[s] = true
		c.rep.Notes = append(c.rep.Notes, s)
	}
}

func (c *checker) add(sev, kind string, n *node, text, format string, args ...any) {
	c.rep.Findings = append(c.rep.Findings, Finding{Severity: sev, Kind: kind, Loc: n.loc(), Text: text, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) collect(n *node, dx, dy float64) {
	if n.parent != nil && hidden(n) {
		return
	}
	if t, ok := n.attrs["transform"]; ok {
		tx, ty, ok := parseTranslate(t)
		if !ok {
			c.note("%s: transform %q is not a translate; skipped with its content", n.loc(), t)
			return
		}
		dx, dy = dx+tx, dy+ty
	}
	switch n.name {
	case "svg":
		if n.parent != nil {
			c.note("%s: nested <svg> skipped", n.loc())
			return
		}
	case "rect":
		c.addRect(n, dx, dy)
	case "text":
		c.addText(n, dx, dy)
		return
	case "line", "polyline", "path":
		if hasMarker(n) {
			c.addArrow(n, dx, dy)
		}
	}
	for _, ch := range n.children {
		c.collect(ch, dx, dy)
	}
}

func hasMarker(n *node) bool {
	for _, p := range []string{"marker-end", "marker-start", "marker"} {
		if v, ok := n.inherited(p); ok && v != "" && v != "none" {
			return true
		}
	}
	return false
}

var numRe = regexp.MustCompile(`[-+]?(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?`)

var pathTok = regexp.MustCompile(`[A-Za-z]|[-+]?(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?`)

// parseTranslate accepts one or more translate() functions.
func parseTranslate(s string) (float64, float64, bool) {
	s = strings.TrimSpace(s)
	var tx, ty float64
	for s != "" {
		if !strings.HasPrefix(s, "translate") {
			return 0, 0, false
		}
		open, close := strings.Index(s, "("), strings.Index(s, ")")
		if open < 0 || close < open || strings.TrimSpace(s[len("translate"):open]) != "" {
			return 0, 0, false
		}
		args := numRe.FindAllString(s[open+1:close], -1)
		switch len(args) {
		case 1:
			v, _ := strconv.ParseFloat(args[0], 64)
			tx += v
		case 2:
			v, _ := strconv.ParseFloat(args[0], 64)
			w, _ := strconv.ParseFloat(args[1], 64)
			tx, ty = tx+v, ty+w
		default:
			return 0, 0, false
		}
		s = strings.TrimLeft(s[close+1:], " \t\r\n,")
	}
	return tx, ty, true
}

// length parses a number with an optional px unit; ok is false otherwise.
func length(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "px"))
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// firstLength reads the first value of a coordinate list attribute.
func firstLength(s string) (float64, bool) {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
	if len(f) == 0 {
		return 0, false
	}
	return length(f[0])
}

func (c *checker) attrNum(n *node, name string, def float64) (float64, bool) {
	s, ok := n.attrs[name]
	if !ok {
		return def, true
	}
	v, ok := length(s)
	if !ok {
		c.note("%s: %s=%q is not a number in px; skipped", n.loc(), name, s)
	}
	return v, ok
}

func (c *checker) addRect(n *node, dx, dy float64) {
	x, ok1 := c.attrNum(n, "x", 0)
	y, ok2 := c.attrNum(n, "y", 0)
	w, ok3 := c.attrNum(n, "width", 0)
	h, ok4 := c.attrNum(n, "height", 0)
	if !(ok1 && ok2 && ok3 && ok4) || w <= 0 || h <= 0 {
		return
	}
	b := box{x + dx, y + dy, x + dx + w, y + dy + h}
	inner := b
	if s, ok := n.inherited("stroke"); ok && s != "none" && s != "transparent" {
		sw := 1.0
		if v, ok := n.inherited("stroke-width"); ok {
			if f, ok := length(v); ok {
				sw = f
			}
		}
		inner = box{b.x0 + sw/2, b.y0 + sw/2, b.x1 - sw/2, b.y1 - sw/2}
	}
	c.rects = append(c.rects, rectInfo{n: n, b: b, inner: inner})
}

// fontSize returns the computed font-size of n in px.
func fontSize(n *node) float64 {
	if n == nil {
		return 16
	}
	parent := fontSize(n.parent)
	v, ok := n.prop("font-size")
	if !ok || v == "inherit" {
		return parent
	}
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.HasSuffix(v, "em"):
		if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "em"), 64); err == nil {
			return f * parent
		}
	case strings.HasSuffix(v, "%"):
		if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64); err == nil {
			return f / 100 * parent
		}
	case strings.HasSuffix(v, "pt"):
		if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "pt"), 64); err == nil {
			return f * 96 / 72
		}
	default:
		if f, ok := length(v); ok {
			return f
		}
	}
	return parent
}

// emLength parses dx/dy style values, allowing em units.
func emLength(s string, size float64) (float64, bool) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "em") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(s, "em"), 64)
		return f * size, err == nil
	}
	return firstLength(s)
}

type textSeg struct {
	text   string
	size   float64
	weight Weight
}

type textChunk struct {
	x, y   float64
	anchor string
	segs   []textSeg
	extra  float64 // dx inside the chunk
}

// addText lays out one <text> element in chunks: a new chunk starts at
// every tspan with its own x or y (or dy).
func (c *checker) addText(t *node, dx, dy float64) {
	anchorOf := func(n *node) string {
		v, _ := n.inherited("text-anchor")
		return v
	}
	var chunks []*textChunk
	var cur *textChunk
	penX := func() float64 {
		if cur == nil {
			return 0
		}
		w, _, _ := c.measure(cur)
		switch cur.anchor {
		case "middle":
			return cur.x + w/2
		case "end":
			return cur.x
		}
		return cur.x + w
	}
	start := func(n *node) bool {
		size := fontSize(n)
		xs, hasX := n.attrs["x"]
		ys, hasY := n.attrs["y"]
		ddx, hasDX := n.attrs["dx"]
		ddy, hasDY := n.attrs["dy"]
		if n != t && !hasX && !hasY && !hasDY {
			if hasDX && cur != nil {
				if v, ok := emLength(ddx, size); ok {
					cur.extra += v
				}
			}
			return true
		}
		x, y := penX(), 0.0
		if cur != nil {
			y = cur.y
		}
		if hasX {
			v, ok := firstLength(xs)
			if !ok {
				c.note("%s: x=%q is not a number in px; text skipped", n.loc(), xs)
				return false
			}
			x = v + dx
		} else if n == t {
			x = dx
		}
		if hasY {
			v, ok := firstLength(ys)
			if !ok {
				c.note("%s: y=%q is not a number in px; text skipped", n.loc(), ys)
				return false
			}
			y = v + dy
		} else if n == t {
			y = dy
		}
		if hasDX {
			if v, ok := emLength(ddx, size); ok {
				x += v
			}
		}
		if hasDY {
			if v, ok := emLength(ddy, size); ok {
				y += v
			}
		}
		anchor := anchorOf(n)
		if !hasX && n != t {
			anchor = "start"
		}
		cur = &textChunk{x: x, y: y, anchor: anchor}
		chunks = append(chunks, cur)
		return true
	}
	ok := true
	var rec func(n *node)
	rec = func(n *node) {
		if !ok {
			return
		}
		if n != t {
			if hidden(n) {
				return
			}
			if _, has := n.attrs["transform"]; has {
				c.note("%s: transform on text content ignored", n.loc())
			}
		}
		if !start(n) {
			ok = false
			return
		}
		size, w := fontSize(n), weightOf(n)
		for _, it := range n.items {
			if it.child != nil {
				rec(it.child)
				continue
			}
			cur.segs = append(cur.segs, textSeg{it.text, size, w})
		}
	}
	rec(t)
	if !ok {
		return
	}
	if len(chunks) == 0 {
		return
	}
	// Every line of the text belongs to the box around the text's own x, y.
	ax, ay := chunks[0].x, chunks[0].y
	full := collapse(t.textContent())
	for _, ch := range chunks {
		normalizeChunk(ch)
		w, ink, has := c.measure(ch)
		if !has {
			continue
		}
		x0 := ch.x
		switch ch.anchor {
		case "middle":
			x0 -= w / 2
		case "end":
			x0 -= w
		}
		ink.x0, ink.x1 = x0, x0+w
		c.lines = append(c.lines, textLine{n: t, ax: ax, ay: ay, ink: ink, text: full})
	}
}

// collapse turns white space into single spaces and trims the ends.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normalizeChunk applies the default SVG white-space handling across the
// segments of a chunk.
func normalizeChunk(ch *textChunk) {
	space := true // drop leading spaces
	for i := range ch.segs {
		var b strings.Builder
		for _, r := range ch.segs[i].text {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				if space {
					continue
				}
				space = true
				b.WriteRune(' ')
				continue
			}
			space = false
			b.WriteRune(r)
		}
		ch.segs[i].text = b.String()
	}
	for i := len(ch.segs) - 1; i >= 0; i-- {
		ch.segs[i].text = strings.TrimRight(ch.segs[i].text, " ")
		if ch.segs[i].text != "" {
			break
		}
	}
}

// measure returns the advance width of the chunk and its ink box relative
// to the baseline (only y is set). has is false when nothing is drawn.
func (c *checker) measure(ch *textChunk) (w float64, ink box, has bool) {
	ink = box{y0: math.Inf(1), y1: math.Inf(-1)}
	var missing []rune
	for _, s := range ch.segs {
		f := c.opt.Fonts[s.weight]
		if f == nil {
			f = c.opt.Fonts[Regular]
		}
		if f == nil {
			continue
		}
		scale := s.size / float64(f.UnitsPerEm())
		for _, r := range s.text {
			if r < 0x20 {
				continue
			}
			adv, ok := f.Advance(r)
			if !ok && r != ' ' {
				missing = append(missing, r)
			}
			w += float64(adv) * scale
			if _, yMin, _, yMax, ok := f.Bounds(r); ok {
				has = true
				ink.y0 = math.Min(ink.y0, ch.y-float64(yMax)*scale)
				ink.y1 = math.Max(ink.y1, ch.y-float64(yMin)*scale)
			}
		}
	}
	w += ch.extra
	if len(missing) > 0 {
		c.note("characters not in the font, measured as .notdef: %q", string(missing))
	}
	return w, ink, has
}

func excerpt(s string) string {
	if utf8.RuneCountInString(s) <= 24 {
		return s
	}
	r := []rune(s)
	return string(r[:24]) + "..."
}

func (c *checker) checkText() {
	const eps = 0.5
	for _, l := range c.lines {
		var best *rectInfo
		for i := range c.rects {
			r := &c.rects[i]
			if !r.b.contains(l.ax, l.ay, 0) {
				continue
			}
			if best == nil || r.b.w()*r.b.h() < best.b.w()*best.b.h() {
				best = r
			}
		}
		if best == nil {
			continue
		}
		in := best.inner
		over := []struct {
			side string
			by   float64
		}{
			{"left", in.x0 - l.ink.x0},
			{"right", l.ink.x1 - in.x1},
			{"top", in.y0 - l.ink.y0},
			{"bottom", l.ink.y1 - in.y1},
		}
		var sides []string
		for _, o := range over {
			if o.by > eps {
				sides = append(sides, fmt.Sprintf("%s by %.1f px", o.side, o.by))
			}
		}
		if len(sides) > 0 {
			c.add(Violation, "overflow", l.n, excerpt(l.text), "text leaves %s: %s", best.n.loc(), strings.Join(sides, ", "))
		}
	}
	for i := 0; i < len(c.lines); i++ {
		for j := i + 1; j < len(c.lines); j++ {
			a, b := c.lines[i], c.lines[j]
			if a.n == b.n {
				continue
			}
			ow := math.Min(a.ink.x1, b.ink.x1) - math.Max(a.ink.x0, b.ink.x0)
			oh := math.Min(a.ink.y1, b.ink.y1) - math.Max(a.ink.y0, b.ink.y0)
			if ow > eps && oh > eps {
				c.add(Violation, "overlap", a.n, excerpt(a.text), "text overlaps %s %q by %.1f x %.1f px", b.n.loc(), excerpt(b.text), ow, oh)
			}
		}
	}
}

func (c *checker) addArrow(n *node, dx, dy float64) {
	var pts []point
	var segs [][2]point
	switch n.name {
	case "line":
		var v [4]float64
		for i, a := range []string{"x1", "y1", "x2", "y2"} {
			f, ok := c.attrNum(n, a, 0)
			if !ok {
				return
			}
			v[i] = f
		}
		pts = []point{{v[0], v[1]}, {v[2], v[3]}}
		segs = [][2]point{{pts[0], pts[1]}}
	case "polyline":
		nums := numRe.FindAllString(n.attrs["points"], -1)
		for i := 0; i+1 < len(nums); i += 2 {
			x, _ := strconv.ParseFloat(nums[i], 64)
			y, _ := strconv.ParseFloat(nums[i+1], 64)
			pts = append(pts, point{x, y})
		}
		for i := 1; i < len(pts); i++ {
			segs = append(segs, [2]point{pts[i-1], pts[i]})
		}
	case "path":
		var ok bool
		pts, segs, ok = parsePath(n.attrs["d"])
		if !ok {
			c.note("%s: path with curves or arcs is not checked", n.loc())
			return
		}
	}
	if len(pts) < 2 {
		return
	}
	for i := range pts {
		pts[i].x += dx
		pts[i].y += dy
	}
	for i := range segs {
		for k := 0; k < 2; k++ {
			segs[i][k].x += dx
			segs[i][k].y += dy
		}
	}
	c.arrows = append(c.arrows, arrow{n: n, pts: pts, segs: segs})
}

// parsePath reads a path made of M, L, H, V and Z commands (absolute or
// relative). ok is false for any other command.
func parsePath(d string) (pts []point, segs [][2]point, ok bool) {
	toks := pathTok.FindAllString(d, -1)
	var cmd byte
	var cur, start point
	num := func(i *int) (float64, bool) {
		if *i >= len(toks) {
			return 0, false
		}
		v, err := strconv.ParseFloat(toks[*i], 64)
		if err != nil {
			return 0, false
		}
		*i++
		return v, true
	}
	lineTo := func(p point) {
		segs = append(segs, [2]point{cur, p})
		pts = append(pts, p)
		cur = p
	}
	for i := 0; i < len(toks); {
		t := toks[i]
		if c := t[0]; (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			cmd = c
			i++
			if cmd == 'Z' || cmd == 'z' {
				lineTo(start)
				continue
			}
		} else if cmd == 0 {
			return nil, nil, false
		}
		rel := cmd >= 'a'
		switch cmd | 0x20 {
		case 'm', 'l':
			x, ok1 := num(&i)
			y, ok2 := num(&i)
			if !ok1 || !ok2 {
				return nil, nil, false
			}
			p := point{x, y}
			if rel {
				p = point{cur.x + x, cur.y + y}
			}
			if cmd|0x20 == 'm' {
				cur, start = p, p
				pts = append(pts, p)
				if rel {
					cmd = 'l'
				} else {
					cmd = 'L'
				}
				continue
			}
			lineTo(p)
		case 'h':
			x, ok := num(&i)
			if !ok {
				return nil, nil, false
			}
			if rel {
				x += cur.x
			}
			lineTo(point{x, cur.y})
		case 'v':
			y, ok := num(&i)
			if !ok {
				return nil, nil, false
			}
			if rel {
				y += cur.y
			}
			lineTo(point{cur.x, y})
		default:
			return nil, nil, false
		}
	}
	return pts, segs, true
}

// borderDist is the distance from p to the outline of b.
func borderDist(p point, b box) float64 {
	if b.contains(p.x, p.y, 0) {
		return math.Min(math.Min(p.x-b.x0, b.x1-p.x), math.Min(p.y-b.y0, b.y1-p.y))
	}
	dx := math.Max(math.Max(b.x0-p.x, 0), p.x-b.x1)
	dy := math.Max(math.Max(b.y0-p.y, 0), p.y-b.y1)
	return math.Hypot(dx, dy)
}

// segmentHits reports whether the segment a-b enters the open box.
func segmentHits(a, b point, r box) bool {
	if r.x1 <= r.x0 || r.y1 <= r.y0 {
		return false
	}
	t0, t1 := 0.0, 1.0
	dx, dy := b.x-a.x, b.y-a.y
	clip := func(p, q float64) bool {
		if p == 0 {
			return q > 0
		}
		t := q / p
		if p < 0 {
			if t > t1 {
				return false
			}
			t0 = math.Max(t0, t)
		} else {
			if t < t0 {
				return false
			}
			t1 = math.Min(t1, t)
		}
		return true
	}
	if !clip(-dx, a.x-r.x0) || !clip(dx, r.x1-a.x) || !clip(-dy, a.y-r.y0) || !clip(dy, r.y1-a.y) {
		return false
	}
	return t1-t0 > 1e-9
}

type endpoint struct {
	p    point
	a    *arrow
	name string // start or end
}

func (c *checker) checkArrows() {
	tol := c.opt.Tol
	var ends []endpoint
	for i := range c.arrows {
		a := &c.arrows[i]
		first, last := a.pts[0], a.pts[len(a.pts)-1]
		for _, e := range []endpoint{{first, a, "start"}, {last, a, "end"}} {
			ends = append(ends, e)
			best := math.Inf(1)
			for _, r := range c.rects {
				best = math.Min(best, borderDist(e.p, r.b))
			}
			if best > tol {
				msg := fmt.Sprintf("%s (%.1f, %.1f) is not on a box edge", e.name, e.p.x, e.p.y)
				if !math.IsInf(best, 1) {
					msg += fmt.Sprintf(" (nearest edge %.1f px away)", best)
				}
				c.add(Violation, "loose-end", a.n, "", "%s", msg)
			}
		}
		for _, r := range c.rects {
			if r.b.contains(first.x, first.y, tol) || r.b.contains(last.x, last.y, tol) {
				continue
			}
			in := box{r.b.x0 + tol, r.b.y0 + tol, r.b.x1 - tol, r.b.y1 - tol}
			for k, s := range a.segs {
				if segmentHits(s[0], s[1], in) {
					c.add(Violation, "crosses-box", a.n, "", "segment %d passes through %s", k+1, r.n.loc())
					break
				}
			}
		}
	}
	// Group arrow ends lying within 3*tol of each other.
	parent := make([]int, len(ends))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i := range ends {
		for j := i + 1; j < len(ends); j++ {
			if math.Hypot(ends[i].p.x-ends[j].p.x, ends[i].p.y-ends[j].p.y) <= 3*tol {
				parent[find(i)] = find(j)
			}
		}
	}
	groups := map[int][]int{}
	for i := range ends {
		groups[find(i)] = append(groups[find(i)], i)
	}
	keys := make([]int, 0, len(groups))
	for _, g := range groups {
		if len(g) >= 3 {
			keys = append(keys, g[0])
		}
	}
	sort.Ints(keys)
	for _, k := range keys {
		g := groups[find(k)]
		var names []string
		var cx, cy float64
		for _, i := range g {
			names = append(names, ends[i].a.n.loc()+" "+ends[i].name)
			cx += ends[i].p.x
			cy += ends[i].p.y
		}
		cx, cy = cx/float64(len(g)), cy/float64(len(g))
		c.add(Warning, "star", ends[g[0]].a.n, "", "%d arrow ends meet within %.0f px around (%.1f, %.1f): %s", len(g), 3*tol, cx, cy, strings.Join(names, ", "))
	}
}

// WritePlain writes the report for a person. Violations start with "!",
// warnings with "?".
func (r *CheckReport) WritePlain(w io.Writer) error {
	verdict := "PASS"
	if r.Violations > 0 {
		verdict = "FAIL"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s, %s\n", verdict, plural(r.Violations, "violation"), plural(r.Warnings, "warning"))
	fmt.Fprintf(&b, "  checked %s, %s, %s\n", plural(r.Texts, "text line"), plural(r.Rects, "box"), plural(r.Arrows, "arrow"))
	if len(r.Findings) > 0 {
		b.WriteString("\nfindings\n")
		for _, f := range r.Findings {
			mark := "!"
			if f.Severity == Warning {
				mark = "?"
			}
			loc := f.Loc
			if f.Text != "" {
				loc += fmt.Sprintf(" %q", f.Text)
			}
			fmt.Fprintf(&b, "  %s %-11s %s: %s\n", mark, f.Kind, loc, f.Message)
		}
	}
	if len(r.Notes) > 0 {
		b.WriteString("\nnotes\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "x") {
		return fmt.Sprintf("%d %ses", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
