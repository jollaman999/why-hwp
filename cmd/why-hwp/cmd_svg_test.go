package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testFont = "../../testdata/fonts/sans-subset.ttf"
	testSVG  = "../../testdata/svg"
)

func TestSVGFontTwice(t *testing.T) {
	dir := t.TempDir()
	once, twice := filepath.Join(dir, "once.svg"), filepath.Join(dir, "twice.svg")
	in := filepath.Join(testSVG, "embed.svg")
	var out, errOut bytes.Buffer
	if code := run([]string{"svgfont", "-regular", testFont, "-bold", testFont, "-o", once, in}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errOut.String())
	}
	for _, want := range []string{"regular: " + testFont + " (flag -regular), 10 characters", "bold: " + testFont + " (flag -bold), 12 characters"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut.String())
		}
	}
	// -w on a copy of the output must leave it as it is.
	data, err := os.ReadFile(once)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(twice, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"svgfont", "-regular", testFont, "-bold", testFont, "-w", twice}, &out, &errOut); code != 0 {
		t.Fatalf("second run: exit code = %d (stderr %q)", code, errOut.String())
	}
	again, err := os.ReadFile(twice)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again) {
		t.Error("running svgfont on its own output changed the file")
	}
	if !bytes.Contains(data, []byte(`font-family="'WhyHwpEmbed', NoSuchFont"`)) {
		t.Error("font-family attribute not prefixed")
	}
}

func TestSVGFontUsage(t *testing.T) {
	in := filepath.Join(testSVG, "embed.svg")
	for _, args := range [][]string{
		{"svgfont", in},
		{"svgfont", "-w", "-o", "x.svg", in},
		{"svgfont", "-w"},
		{"svgfont", "-family", "a'b", "-w", in},
	} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 2 {
			t.Errorf("%q: exit code = %d, want 2 (stderr %q)", args, code, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	code := run([]string{"svgfont", "-regular", filepath.Join(testSVG, "good.svg"), "-o", filepath.Join(t.TempDir(), "x.svg"), in}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "not a TrueType font") {
		t.Errorf("non-font -regular: exit %d, stderr %q", code, errOut.String())
	}
}

func TestSVGCheckExitCodes(t *testing.T) {
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"svgcheck", "-regular", testFont, "-bold", testFont, filepath.Join(testSVG, "good.svg")}, 0, "PASS: 0 violations, 0 warnings"},
		{[]string{"svgcheck", "-regular", testFont, "-bold", testFont, filepath.Join(testSVG, "bad.svg")}, 1, "FAIL: 4 violations, 1 warning"},
		{[]string{"svgcheck", "-regular", testFont, filepath.Join(testSVG, "no-such.svg")}, 2, ""},
		{[]string{"svgcheck", "-format", "xml", filepath.Join(testSVG, "good.svg")}, 2, ""},
		{[]string{"svgcheck", "-regular", filepath.Join(testSVG, "good.svg"), "-bold", testFont, filepath.Join(testSVG, "good.svg")}, 2, ""},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		code := run(c.args, &out, &errOut)
		if code != c.code {
			t.Errorf("%q: exit code = %d, want %d (stderr %q)", c.args, code, c.code, errOut.String())
		}
		if !strings.Contains(out.String(), c.want) {
			t.Errorf("%q: stdout lacks %q:\n%s", c.args, c.want, out.String())
		}
	}
}

func TestSVGCheckJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"svgcheck", "-format", "json", "-regular", testFont, "-bold", testFont, filepath.Join(testSVG, "bad.svg")}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit code = %d (stderr %q)", code, errOut.String())
	}
	var rep struct {
		Violations int `json:"violations"`
		Warnings   int `json:"warnings"`
		Findings   []struct {
			Severity, Kind, Loc string
		} `json:"findings"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Violations != 4 || rep.Warnings != 1 || len(rep.Findings) != 5 || rep.Findings[0].Kind != "overflow" || rep.Findings[0].Loc != "long" {
		t.Errorf("unexpected report: %+v", rep)
	}
}
