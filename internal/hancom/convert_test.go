package hancom

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{"hwp": HWP, "HWPX": HWPX, ".pdf": PDF, "Txt": TXT} {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "docx", "text", "png"} {
		if _, err := ParseFormat(in); err == nil {
			t.Errorf("ParseFormat(%q): no error", in)
		}
	}
}

func TestSaveFormat(t *testing.T) {
	want := map[Format]string{HWP: "HWP", HWPX: "HWPX", PDF: "PDF", TXT: "TEXT"}
	for _, f := range Formats {
		if got := f.saveFormat(); got != want[f] {
			t.Errorf("%s.saveFormat() = %q, want %q", f, got, want[f])
		}
	}
}

func TestOpenFormat(t *testing.T) {
	for in, want := range map[string]string{"a.hwp": "HWP", `d\b.HWPX`: "HWPX", "c.docx": ""} {
		got, err := openFormat(in)
		if err != nil || got != want {
			t.Errorf("openFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"a.doc", "a.pdf", "a", "a.txt"} {
		if _, err := openFormat(in); err == nil {
			t.Errorf("openFormat(%q): no error", in)
		}
	}
}

func TestPlan(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "doc.hwpx")
	tests := []struct {
		out, to string
		dst     string
		f       Format
	}{
		{"", "hwp", filepath.Join(dir, "doc.hwp"), HWP},
		{"", "TXT", filepath.Join(dir, "doc.txt"), TXT},
		{"x.pdf", "", "x.pdf", PDF},
		{"x.PDF", "", "x.PDF", PDF},
		{"x.bin", "hwp", "x.bin", HWP},
		{"x.pdf", "hwpx", "x.pdf", HWPX},
	}
	for _, tt := range tests {
		dst, f, err := Plan(in, tt.out, tt.to)
		if err != nil || dst != tt.dst || f != tt.f {
			t.Errorf("Plan(%q, %q) = %q, %q, %v; want %q, %q", tt.out, tt.to, dst, f, err, tt.dst, tt.f)
		}
	}
	bad := []struct{ in, out, to, msg string }{
		{in, "", "", "-to or an output path with -o"},
		{in, "x", "", "no extension"},
		{in, "x.docx", "", "unknown output format"},
		{in, "", "png", "unknown output format"},
		{filepath.Join(dir, "a.pdf"), "", "hwp", "unsupported input"},
		{in, "", "hwpx", "output is the input"},
		{in, strings.ToUpper(in), "hwp", "output is the input"},
	}
	for _, b := range bad {
		_, _, err := Plan(b.in, b.out, b.to)
		if err == nil || !strings.Contains(err.Error(), b.msg) {
			t.Errorf("Plan(%q, %q, %q) error = %v, want %q", b.in, b.out, b.to, err, b.msg)
		}
	}
}

func TestParseTasklist(t *testing.T) {
	out := "\r\n\"Hwp.exe\",\"11500\",\"Console\",\"1\",\"39,956 K\"\r\n" +
		"\"hwp.EXE\",\"28480\",\"Console\",\"1\",\"1,000 K\"\r\n" +
		"\"HwpAgent.exe\",\"7\",\"Console\",\"1\",\"1 K\"\r\n"
	if got, want := parseTasklist([]byte(out), "Hwp.exe"), []int{11500, 28480}; !reflect.DeepEqual(got, want) {
		t.Errorf("parseTasklist = %v, want %v", got, want)
	}
	if got := parseTasklist([]byte("INFO: No tasks are running which match the specified criteria.\r\n"), "Hwp.exe"); got != nil {
		t.Errorf("no tasks: %v", got)
	}
}

func TestNewPIDs(t *testing.T) {
	if got, want := newPIDs([]int{1, 2, 3}, []int{2, 3, 4, 5}), []int{4, 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("newPIDs = %v, want %v", got, want)
	}
	if got := newPIDs([]int{1}, []int{1}); got != nil {
		t.Errorf("newPIDs = %v, want none", got)
	}
}

func TestParsePIDLine(t *testing.T) {
	for line, want := range map[string]int{"pid 28480": 28480, "pid 7\r": 7} {
		if got, ok := parsePIDLine(line); !ok || got != want {
			t.Errorf("parsePIDLine(%q) = %d, %v", line, got, ok)
		}
	}
	for _, line := range []string{"pid ", "pid 1,2", "pid x", "registermodule False", "pid 0"} {
		if _, ok := parsePIDLine(line); ok {
			t.Errorf("parsePIDLine(%q) ok", line)
		}
	}
}
