package main

import (
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePages(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []int
		err  string
	}{
		{"", nil, ""},
		{"2", []int{2}, ""},
		{"1-3", []int{1, 2, 3}, ""},
		{" 7-9, 1 ,4", []int{1, 4, 7, 8, 9}, ""},
		{"2-3,1-2", []int{1, 2, 3}, ""},
		{"3-3", []int{3}, ""},
		{"3-1", nil, `"3-1": the range runs backwards`},
		{"0", nil, `"0": want page numbers from 1 to 100000`},
		{"1-", nil, `"1-": want page numbers from 1 to 100000`},
		{"1,,2", nil, `"": want page numbers`},
		{"a", nil, `"a": want page numbers`},
		{"1-100001", nil, `"1-100001": want page numbers from 1 to 100000`},
	} {
		got, err := parsePages(tc.in)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("parsePages(%q) error = %v, want %q", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parsePages(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
}

func TestPageFileName(t *testing.T) {
	for _, tc := range []struct {
		page, count int
		want        string
	}{
		{1, 3, "doc-p001.png"},
		{12, 999, "doc-p012.png"},
		{7, 1000, "doc-p0007.png"},
		{1000, 1000, "doc-p1000.png"},
	} {
		if got := pageFileName("doc", tc.page, tc.count); got != tc.want {
			t.Errorf("pageFileName(doc, %d, %d) = %q, want %q", tc.page, tc.count, got, tc.want)
		}
	}
}

func TestRenderRefused(t *testing.T) {
	dir := t.TempDir()
	docx := filepath.Join(dir, "a.docx")
	if err := os.WriteFile(docx, []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"-dpi", "0", sampleHWP}, 2, "-dpi must be positive"},
		{[]string{"-pages", "2-1", sampleHWP}, 2, `-pages: "2-1": the range runs backwards`},
		{[]string{"-timeout", "0s", sampleHWP}, 2, "-timeout must be positive"},
		{[]string{"-o", dir, docx}, 2, "reads .hwp and .hwpx only"},
		{[]string{"-o", dir, filepath.Join(dir, "missing.hwp")}, 1, "missing.hwp"},
	} {
		code, _, msg := runCode(append([]string{"render"}, tc.args...)...)
		if code != tc.code || !strings.Contains(msg, tc.msg) {
			t.Errorf("%v: exit %d, stderr %q; want exit %d with %q", tc.args, code, msg, tc.code, tc.msg)
		}
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("refused runs wrote files: %v", ents)
	}
}

// pngSize returns the pixel size of the PNG at path.
func pngSize(t *testing.T, path string) (int, int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return cfg.Width, cfg.Height
}

// render with the built-in engine writes one PNG per page under the naming
// rule, sizes them by -dpi, fails on a page the document does not have and
// refuses to overwrite without -force.
func TestRenderBuiltin(t *testing.T) {
	tmp := engineEnv(t)
	dir := filepath.Join(t.TempDir(), "out")
	stdout, _ := runOK(t, "render", "-pages", "1", "-dpi", "96", "-o", dir, sampleHWP)
	first := filepath.Join(dir, "sample-p001.png")
	w96, h96 := pngSize(t, first)
	if want := first + ": page 1, "; !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout = %q, want it to start with %q", stdout, want)
	}
	// A4 portrait is 794x1123 CSS pixels.
	if w96 < 700 || w96 > 900 || h96 <= w96 {
		t.Errorf("96 dpi page is %dx%d px, want about 794x1123", w96, h96)
	}

	runOK(t, "render", "-pages", "1", "-dpi", "192", "-force", "-o", dir, sampleHWP)
	w192, h192 := pngSize(t, first)
	near := func(a, b int) bool { return a-b <= 1 && b-a <= 1 }
	if !near(w192, 2*w96) || !near(h192, 2*h96) {
		t.Errorf("192 dpi page is %dx%d px, want twice %dx%d", w192, h192, w96, h96)
	}

	code, _, msg := runCode("render", "-pages", "1", "-o", dir, sampleHWP)
	if code != 1 || !strings.Contains(msg, "already exists (use -force to overwrite)") {
		t.Errorf("existing output: exit %d, stderr %q", code, msg)
	}

	code, _, msg = runCode("render", "-pages", "99", "-o", dir, sampleHWP)
	if code != 1 || !strings.Contains(msg, "page 99 does not exist; the document has") {
		t.Errorf("-pages 99: exit %d, stderr %q", code, msg)
	}
	if _, err := os.Stat(filepath.Join(dir, "sample-p099.png")); err == nil {
		t.Error("-pages 99: output written")
	}

	all := t.TempDir()
	stdout, _ = runOK(t, "render", "-dpi", "48", "-o", all, sampleHWPX)
	ents, err := os.ReadDir(all)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(stdout, "\n"); len(ents) == 0 || len(ents) != lines {
		t.Errorf("every page: %d files, %d stdout lines", len(ents), lines)
	}
	for i, e := range ents {
		if want := pageFileName("sample", i+1, len(ents)); e.Name() != want {
			t.Errorf("file %d is %s, want %s", i, e.Name(), want)
		}
	}
	assertEmptyDir(t, tmp)
}
