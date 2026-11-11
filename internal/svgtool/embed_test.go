package svgtool

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jollaman999/why-hwp/internal/sfnt"
)

var (
	fixtureFont = filepath.Join("..", "..", "testdata", "fonts", "sans-subset.ttf")
	svgDir      = filepath.Join("..", "..", "testdata", "svg")
)

func readFont(t *testing.T) *sfnt.Font {
	t.Helper()
	data, err := os.ReadFile(fixtureFont)
	if err != nil {
		t.Fatal(err)
	}
	f, err := sfnt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func readSVG(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(svgDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// embedWith runs what why-hwp svgfont does, with the fixture font for both
// weights.
func embedWith(t *testing.T, f *sfnt.Font, svg []byte) []byte {
	t.Helper()
	sets, err := Collect(svg)
	if err != nil {
		t.Fatal(err)
	}
	var faces []Face
	for _, w := range []Weight{Regular, Bold} {
		if len(sets[w]) == 0 {
			continue
		}
		sub, err := f.Subset(sets[w])
		if err != nil {
			t.Fatal(err)
		}
		faces = append(faces, Face{Weight: w, TTF: sub.Font})
	}
	out, err := Embed(svg, DefaultFamily, faces)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sortedRunes(s string) string {
	rs := []rune(s)
	sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
	var out []rune
	for i, r := range rs {
		if i == 0 || r != rs[i-1] {
			out = append(out, r)
		}
	}
	return string(out)
}

func TestCollect(t *testing.T) {
	sets, err := Collect(readSVG(t, "embed.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(sets[Regular]), sortedRunes("Plain & light"); got != want {
		t.Errorf("regular = %q, want %q", got, want)
	}
	if got, want := string(sets[Bold]), sortedRunes("Bold Heavy été"); got != want {
		t.Errorf("bold = %q, want %q", got, want)
	}
}

var faceRe = regexp.MustCompile(`(?s)@font-face\{font-family:'([^']+)';font-weight:(\d+);src:url\("data:font/ttf;base64,([^"]*)"\) format\('truetype'\)\}`)

func TestEmbed(t *testing.T) {
	f := readFont(t)
	in := readSVG(t, "embed.svg")
	out := string(embedWith(t, f, in))

	if n := strings.Count(out, `<style id="embedded-font">`); n != 1 {
		t.Fatalf("%d embedded styles, want 1", n)
	}
	rootEnd := strings.Index(out, `viewBox="0 0 300 120">`) + len(`viewBox="0 0 300 120">`)
	if !strings.HasPrefix(out[rootEnd:], `<defs><style id="embedded-font">@font-face{`) {
		t.Errorf("no <defs> with the style right after <svg>: %.80q", out[rootEnd:])
	}
	faces := faceRe.FindAllStringSubmatch(out, -1)
	if len(faces) != 2 || faces[0][2] != "400" || faces[1][2] != "700" {
		t.Fatalf("found %d @font-face rules, want 400 and 700", len(faces))
	}
	sets, _ := Collect(in)
	for i, w := range []Weight{Regular, Bold} {
		if faces[i][1] != DefaultFamily {
			t.Errorf("family %q, want %q", faces[i][1], DefaultFamily)
		}
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(faces[i][3], "\\\n", ""))
		if err != nil {
			t.Fatal(err)
		}
		sub, err := sfnt.Parse(data)
		if err != nil {
			t.Fatalf("%s face does not parse: %v", w, err)
		}
		if got, want := string(sub.Runes()), string(sets[w]); got != want {
			t.Errorf("%s face maps %q, want %q", w, got, want)
		}
	}

	for _, want := range []string{
		`font-family="'WhyHwpEmbed', NoSuchFont"`,
		`style="font-weight: 700; font-family: 'WhyHwpEmbed', 'NoSuchFont', serif"`,
		`.note { font-family: 'WhyHwpEmbed', Arial, sans-serif; font-size: 12px }`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s", want)
		}
	}

	// Nothing else changed.
	back := regexp.MustCompile(`(?s)<defs><style id="embedded-font">.*?</style></defs>`).ReplaceAllString(out, "")
	back = strings.ReplaceAll(back, `'WhyHwpEmbed', `, "")
	if back != string(in) {
		t.Errorf("bytes outside the embedded font and family names changed:\n%.400s", back)
	}
}

func TestEmbedTwiceIsSame(t *testing.T) {
	f := readFont(t)
	for _, name := range []string{"embed.svg", "good.svg", "bad.svg"} {
		once := embedWith(t, f, readSVG(t, name))
		twice := embedWith(t, f, once)
		if string(once) != string(twice) {
			t.Errorf("%s: second run changed the file", name)
		}
	}
}

func TestEmbedDefs(t *testing.T) {
	f := readFont(t)
	cases := []struct{ name, in, want string }{
		{
			"self-closing defs",
			`<svg xmlns="http://www.w3.org/2000/svg"><defs/><text>A</text></svg>`,
			`<svg xmlns="http://www.w3.org/2000/svg"><defs><style id="embedded-font">`,
		},
		{
			"defs with content",
			`<svg xmlns="http://www.w3.org/2000/svg"><defs><marker id="m"/></defs><text>A</text></svg>`,
			`<svg xmlns="http://www.w3.org/2000/svg"><defs><style id="embedded-font">`,
		},
		{
			"stale embedded style",
			`<svg xmlns="http://www.w3.org/2000/svg"><defs><marker id="m"/><style id="embedded-font">old</style></defs><text>A</text></svg>`,
			`<svg xmlns="http://www.w3.org/2000/svg"><defs><style id="embedded-font">`,
		},
	}
	for _, c := range cases {
		out := string(embedWith(t, f, []byte(c.in)))
		if !strings.HasPrefix(out, c.want) {
			t.Errorf("%s: output starts %.120q", c.name, out)
		}
		if strings.Count(out, "embedded-font") != 1 || strings.Contains(out, ">old<") {
			t.Errorf("%s: want exactly one fresh embedded style: %.200q", c.name, out)
		}
		if !strings.Contains(out, `<marker id="m"/>`) && c.name != "self-closing defs" {
			t.Errorf("%s: marker lost", c.name)
		}
	}
	if _, err := Embed([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), DefaultFamily, nil); err == nil {
		t.Error("empty <svg/> accepted")
	}
	if _, err := Embed([]byte(`<svg><text>A</text></svg>`), `x"y`, nil); err == nil {
		t.Error("family with a quote accepted")
	}
}

func TestPrefixFamily(t *testing.T) {
	cases := []struct {
		in, want string
		changed  bool
	}{
		{"Arial", "'WhyHwpEmbed', Arial", true},
		{"", "'WhyHwpEmbed'", true},
		{"  'Noto Sans KR', sans-serif", "'WhyHwpEmbed', 'Noto Sans KR', sans-serif", true},
		{"'WhyHwpEmbed', Arial", "'WhyHwpEmbed', Arial", false},
		{"Arial, whyhwpembed", "Arial, whyhwpembed", false},
		{"&quot;WhyHwpEmbed&quot;", "&quot;WhyHwpEmbed&quot;", false},
	}
	for _, c := range cases {
		got, changed := prefixFamily(c.in, DefaultFamily, '\'')
		if got != c.want || changed != c.changed {
			t.Errorf("prefixFamily(%q) = %q, %v; want %q, %v", c.in, got, changed, c.want, c.changed)
		}
	}
	css := `@font-face{font-family:'Other';src:url(x.ttf)} text { font-family: Arial }`
	got, _ := prefixCSS(css, DefaultFamily, '\'')
	if want := `@font-face{font-family:'Other';src:url(x.ttf)} text { font-family: 'WhyHwpEmbed', Arial }`; got != want {
		t.Errorf("prefixCSS = %q, want %q", got, want)
	}
}

var hookKey = regexp.MustCompile(`(?i)AKIA[0-9A-Z]{16}`)

// keyHead is split so that this file does not hold a key-like run itself.
const keyHead = "AK" + "IA"

func TestBreakKeyLike(t *testing.T) {
	cases := []string{
		"xx" + keyHead + "0123456789ABCDEFyy",
		"ak" + "ia" + "abcdefghijklmnop",
		"Ak" + "IA" + "MwAAAAAAAAAAAAAAAAAA",
		"AKI" + keyHead + "0123456789ABCDEFGH",
		keyHead + "0123456789ABCDEF" + keyHead + "0123456789ABCDEF",
		"no key here " + keyHead + "0123",
	}
	for _, in := range cases {
		out := BreakKeyLike(in)
		if strings.ReplaceAll(out, "\\\n", "") != in {
			t.Errorf("%q: removing the breaks gives %q", in, strings.ReplaceAll(out, "\\\n", ""))
		}
		for _, line := range strings.Split(out, "\n") {
			if hookKey.MatchString(line) {
				t.Errorf("%q: line %q still looks like a key", in, line)
			}
		}
		if hookKey.MatchString(in) && !strings.Contains(out, "\\\n") {
			t.Errorf("%q: not broken", in)
		}
	}
	if out := BreakKeyLike("no key here " + keyHead + "0123"); out != "no key here "+keyHead+"0123" {
		t.Errorf("text without a key changed: %q", out)
	}
}

// TestEmbeddedKeyLikeRun embeds a font whose base64 has a key-like run and
// checks that the output has none on any line and still decodes.
func TestEmbeddedKeyLikeRun(t *testing.T) {
	// Bytes whose base64 is exactly this text, with "AKIA" and 16 key
	// characters in it.
	ttf, err := base64.StdEncoding.DecodeString("AAAA" + keyHead + "0123456789ABCDEFAAAA")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Embed([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><text>A</text></svg>`), DefaultFamily, []Face{{Regular, ttf}})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if hookKey.MatchString(line) {
			t.Fatalf("line still looks like a key: %q", line)
		}
	}
	m := faceRe.FindStringSubmatch(string(out))
	if m == nil {
		t.Fatal("no @font-face")
	}
	got, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(m[3], "\\\n", ""))
	if err != nil || string(got) != string(ttf) {
		t.Errorf("font data changed: %v", err)
	}
}
