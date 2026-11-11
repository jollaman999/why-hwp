package svgtool

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jollaman999/why-hwp/internal/sfnt"
)

func check(t *testing.T, f *sfnt.Font, svg string) *CheckReport {
	t.Helper()
	rep, err := Check([]byte(svg), CheckOptions{Tol: 2, Fonts: map[Weight]*sfnt.Font{Regular: f, Bold: f}})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func kinds(rep *CheckReport) string {
	var k []string
	for _, f := range rep.Findings {
		k = append(k, f.Kind+"@"+f.Loc)
	}
	return strings.Join(k, " ")
}

const svgHead = `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300"><defs><marker id="a"><path d="M0,0 L10,5 L0,10 z"/></marker></defs>`

func TestCheckGoodFigure(t *testing.T) {
	rep := check(t, readFont(t), string(readSVG(t, "good.svg")))
	if rep.Violations != 0 || rep.Warnings != 0 {
		t.Errorf("good.svg: %d violations, %d warnings: %s", rep.Violations, rep.Warnings, kinds(rep))
	}
	if rep.Texts != 5 || rep.Arrows != 3 || rep.Rects != 5 {
		t.Errorf("good.svg: checked %d texts, %d arrows, %d boxes; want 5, 3, 5", rep.Texts, rep.Arrows, rep.Rects)
	}
}

func TestCheckBadFigure(t *testing.T) {
	rep := check(t, readFont(t), string(readSVG(t, "bad.svg")))
	want := "overflow@long overlap@text#2 crosses-box@through loose-end@loose star@line#3"
	if got := kinds(rep); got != want {
		t.Errorf("findings %q, want %q", got, want)
	}
	if rep.Violations != 4 || rep.Warnings != 1 {
		t.Errorf("%d violations, %d warnings; want 4 and 1", rep.Violations, rep.Warnings)
	}
}

func TestCheckOverflow(t *testing.T) {
	f := readFont(t)
	w, _ := f.Advance('M')
	width := float64(w) * 20 / float64(f.UnitsPerEm()) * 5 // MMMMM at 20 px
	for _, c := range []struct {
		anchor string
		x      float64
		box    float64 // box width
		want   string
	}{
		{"start", 10, width + 4, ""},
		{"start", 10, width - 4, "overflow@text#1"},
		{"middle", 100, width + 4, ""},
		{"middle", 100, width - 4, "overflow@text#1"},
		{"end", 200, width + 4, ""},
		{"end", 200, width - 4, "overflow@text#1"},
	} {
		x0 := c.x - 2
		switch c.anchor {
		case "middle":
			x0 = c.x - c.box/2
		case "end":
			x0 = c.x - c.box + 2
		}
		svg := fmt.Sprintf(svgHead+`<rect x="%.2f" y="10" width="%.2f" height="40" fill="none"/><text x="%.2f" y="40" font-size="20" text-anchor="%s">MMMMM</text></svg>`, x0, c.box, c.x, c.anchor)
		if got := kinds(check(t, f, svg)); got != c.want {
			t.Errorf("anchor %s, box %.1f for text %.1f wide: %q, want %q", c.anchor, c.box, width, got, c.want)
		}
	}
	// Ink above the box top.
	svg := svgHead + `<rect x="0" y="30" width="300" height="40" fill="none"/><text x="10" y="40" font-size="20">Hd</text></svg>`
	if got := kinds(check(t, f, svg)); got != "overflow@text#1" {
		t.Errorf("text over the top edge: %q", got)
	}
	// The stroke narrows the inside.
	inside := fmt.Sprintf(svgHead+`<rect x="8" y="10" width="%.2f" height="40" fill="none" stroke="#000" stroke-width="%s"/><text x="10" y="40" font-size="20">MMMMM</text></svg>`, width+4, "%s")
	if got := kinds(check(t, f, fmt.Sprintf(inside, "1"))); got != "" {
		t.Errorf("thin stroke: %q", got)
	}
	if got := kinds(check(t, f, fmt.Sprintf(inside, "8"))); got != "overflow@text#1" {
		t.Errorf("thick stroke: %q", got)
	}
}

func TestCheckTranslateAndTspan(t *testing.T) {
	f := readFont(t)
	svg := svgHead + `<g transform="translate(100, 50)"><rect x="0" y="0" width="60" height="50" fill="none"/>` +
		`<text x="5" y="20" font-size="12">Ab<tspan x="5" dy="1.5em">Cd</tspan></text></g>` +
		`<g transform="scale(2)"><rect width="10" height="10"/></g></svg>`
	rep := check(t, f, svg)
	if rep.Violations != 0 || rep.Texts != 2 || rep.Rects != 1 {
		t.Errorf("violations %d, texts %d, rects %d: %s", rep.Violations, rep.Texts, rep.Rects, kinds(rep))
	}
	if len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0], "scale(2)") {
		t.Errorf("notes %q, want one about scale(2)", rep.Notes)
	}
	// Without the translate the second line would be at y 38 and the box at
	// 0..50; moving the tspan below the box must be caught.
	svg = strings.Replace(svg, `dy="1.5em"`, `dy="40"`, 1)
	if got := kinds(check(t, f, svg)); got != "overflow@text#1" {
		t.Errorf("tspan below the box: %q", got)
	}
}

func TestCheckOverlap(t *testing.T) {
	f := readFont(t)
	near := svgHead + `<text x="10" y="40" font-size="16">One</text><text x="20" y="45" font-size="16">Two</text></svg>`
	if got := kinds(check(t, f, near)); got != "overlap@text#1" {
		t.Errorf("overlapping texts: %q", got)
	}
	apart := svgHead + `<text x="10" y="40" font-size="16">One</text><text x="10" y="70" font-size="16">Two</text></svg>`
	if got := kinds(check(t, f, apart)); got != "" {
		t.Errorf("separate texts: %q", got)
	}
	lines := svgHead + `<text x="10" y="40" font-size="16">One<tspan x="10" dy="20">Two</tspan></text></svg>`
	if got := kinds(check(t, f, lines)); got != "" {
		t.Errorf("lines of one text: %q", got)
	}
}

func TestCheckArrows(t *testing.T) {
	f := readFont(t)
	boxes := `<rect id="a" x="10" y="10" width="50" height="30"/><rect id="b" x="110" y="10" width="50" height="30"/><rect id="c" x="210" y="10" width="50" height="30"/>`
	cases := []struct{ name, el, want string }{
		{"line box to box", `<line x1="60" y1="25" x2="110" y2="25" marker-end="url(#a)"/>`, ""},
		{"within tol", `<line x1="61.5" y1="25" x2="108.5" y2="25" marker-end="url(#a)"/>`, ""},
		{"no marker", `<line x1="60" y1="25" x2="90" y2="25"/>`, ""},
		{"mid-air", `<line x1="60" y1="25" x2="90" y2="25" marker-end="url(#a)"/>`, "loose-end@line#1"},
		{"inside a box", `<line x1="60" y1="25" x2="135" y2="25" marker-end="url(#a)"/>`, "loose-end@line#1"},
		{"through b", `<line x1="60" y1="25" x2="210" y2="25" marker-end="url(#a)"/>`, "crosses-box@line#1"},
		{"path around b", `<path d="M35 40 V60 H235 V40" marker-end="url(#a)"/>`, ""},
		{"relative path through b", `<path d="m60 25 h150" style="marker-start:url(#a)"/>`, "crosses-box@path#2"},
		{"polyline through b", `<polyline points="35,40 135,25 235,40" marker-end="url(#a)"/>`, "crosses-box@polyline#1"},
		{"curve skipped", `<path d="M60 25 C80 0 90 0 110 25" marker-end="url(#a)"/>`, ""},
		{"inherited marker", `<g marker-end="url(#a)"><line x1="60" y1="25" x2="90" y2="25"/></g>`, "loose-end@line#1"},
	}
	for _, c := range cases {
		rep := check(t, f, svgHead+boxes+c.el+`</svg>`)
		if got := kinds(rep); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if c.name == "curve skipped" && (len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0], "curves")) {
			t.Errorf("curve skipped: notes %q", rep.Notes)
		}
	}
}

func TestCheckStar(t *testing.T) {
	f := readFont(t)
	boxes := `<rect x="100" y="100" width="80" height="40"/><rect x="0" y="0" width="40" height="20"/><rect x="120" y="0" width="40" height="20"/><rect x="240" y="0" width="40" height="20"/>`
	star := `<line x1="20" y1="20" x2="140" y2="100" marker-end="url(#a)"/><line x1="140" y1="20" x2="140" y2="100" marker-end="url(#a)"/><line x1="260" y1="20" x2="143" y2="100" marker-end="url(#a)"/>`
	rep := check(t, f, svgHead+boxes+star+`</svg>`)
	if got := kinds(rep); got != "star@line#1" || rep.Violations != 0 || rep.Warnings != 1 {
		t.Errorf("three ends at one point: %q (%d violations)", got, rep.Violations)
	}
	spread := strings.Replace(strings.Replace(star, `x2="140" y2="100"`, `x2="120" y2="100"`, 1), `x2="143"`, `x2="165"`, 1)
	if got := kinds(check(t, f, svgHead+boxes+spread+`</svg>`)); got != "" {
		t.Errorf("spread ends: %q", got)
	}
}

func TestParseTranslate(t *testing.T) {
	cases := []struct {
		in     string
		x, y   float64
		accept bool
	}{
		{"translate(10,20)", 10, 20, true},
		{"translate(10 20) translate(-5)", 5, 20, true},
		{" translate( 1.5e1 , -2 ) ", 15, -2, true},
		{"scale(2)", 0, 0, false},
		{"translate(1,2) rotate(45)", 0, 0, false},
	}
	for _, c := range cases {
		x, y, ok := parseTranslate(c.in)
		if ok != c.accept || (ok && (x != c.x || y != c.y)) {
			t.Errorf("parseTranslate(%q) = %v, %v, %v", c.in, x, y, ok)
		}
	}
}

func TestWritePlain(t *testing.T) {
	rep := check(t, readFont(t), string(readSVG(t, "bad.svg")))
	var b strings.Builder
	if err := rep.WritePlain(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"FAIL: 4 violations, 1 warning", `! overflow    long "A label far too long": text leaves small`, "? star"} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output lacks %q:\n%s", want, out)
		}
	}
}
