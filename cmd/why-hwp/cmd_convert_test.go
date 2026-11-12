package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runCode(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// convert with the built-in engine writes the text of the document, reports
// the size and MD5 of the file it wrote, refuses to overwrite without
// -force, and leaves nothing in the temporary folder.
func TestConvertBuiltin(t *testing.T) {
	tmp := engineEnv(t)
	dst := filepath.Join(t.TempDir(), "sample.txt")
	stdout, _ := runOK(t, "convert", "-to", "txt", "-o", dst, sampleHWP)
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if first, _, _ := strings.Cut(string(got), "\n"); strings.TrimSpace(first) != "샘플 사업 계획서" {
		t.Errorf("first line = %q, want %q", first, "샘플 사업 계획서")
	}
	sum := md5.Sum(got)
	if want := fmt.Sprintf("%s: txt, %d bytes, md5 %s\n", dst, len(got), hex.EncodeToString(sum[:])); stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}

	if err := os.WriteFile(dst, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, msg := runCode("convert", "-to", "txt", "-o", dst, sampleHWPX)
	if code != 1 || !strings.Contains(msg, "already exists (use -force to overwrite)") {
		t.Errorf("existing output: exit %d, stderr %q", code, msg)
	}
	if b, _ := os.ReadFile(dst); string(b) != "keep" {
		t.Errorf("existing output changed to %q", b)
	}
	runOK(t, "convert", "-force", "-to", "txt", "-o", dst, sampleHWPX)
	if b, _ := os.ReadFile(dst); !bytes.Equal(b, got) {
		t.Errorf("-force: output %q, want %q", b, got)
	}

	hwpx := filepath.Join(t.TempDir(), "sample.hwpx")
	runOK(t, "convert", "-o", hwpx, sampleHWP)
	want, _ := runOK(t, "text", sampleHWP)
	if text, _ := runOK(t, "text", hwpx); text != want {
		t.Errorf("text of the HWPX output differs\n got:\n%s\nwant:\n%s", text, want)
	}

	late := filepath.Join(t.TempDir(), "late.pdf")
	code, _, msg = runCode("convert", "-timeout", "1ms", "-o", late, sampleHWP)
	if code != 1 || !strings.Contains(msg, "conversion did not finish within 1ms") {
		t.Errorf("-timeout 1ms: exit %d, stderr %q", code, msg)
	}
	if _, err := os.Stat(late); err == nil {
		t.Error("-timeout 1ms: output written")
	}
	assertEmptyDir(t, tmp)
}

func TestConvertRefused(t *testing.T) {
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
		{[]string{"-engine", "word", "-to", "pdf", sampleHWP}, 2, `unknown -engine "word"`},
		{[]string{"-to", "pdf", docx}, 2, "the built-in engine reads .hwp and .hwpx only"},
		{[]string{"-to", "hwpx", sampleHWPX}, 2, "output is the input file"},
	} {
		code, _, msg := runCode(append([]string{"convert"}, tc.args...)...)
		if code != tc.code || !strings.Contains(msg, tc.msg) {
			t.Errorf("%v: exit %d, stderr %q; want exit %d with %q", tc.args, code, msg, tc.code, tc.msg)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "a.pdf")); err == nil {
		t.Error("docx: output written")
	}
}

func TestConvertHancomNeedsWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Hancom Office may be installed")
	}
	out := filepath.Join(t.TempDir(), "a.pdf")
	code, _, msg := runCode("convert", "-engine", "hancom", "-o", out, sampleHWP)
	if want := "why-hwp convert: -engine hancom: convert needs Windows with Hancom Office\n"; code != 1 || msg != want {
		t.Errorf("exit %d, stderr %q; want exit 1, %q", code, msg, want)
	}
}

func TestConvertDefaultTimeout(t *testing.T) {
	if got := defaultTimeout("builtin"); got != 10*time.Minute {
		t.Errorf("builtin default = %v, want 10m", got)
	}
	if got := defaultTimeout("hancom"); got != 120*time.Second {
		t.Errorf("hancom default = %v, want 120s", got)
	}
	if code, _, msg := runCode("convert", "-timeout", "0s", "-to", "txt", sampleHWP); code != 2 || !strings.Contains(msg, "-timeout must be positive") {
		t.Errorf("-timeout 0s: exit %d, stderr %q", code, msg)
	}
}
