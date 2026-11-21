package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jollaman999/why-hwp/internal/hancom"
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

// The tracked-change check counts what 'why-hwp info' counts, reads a file
// as what it holds whatever its extension, keeps an output with as many
// tracked changes as the input or more, and refuses one with fewer. apply
// on an HWPX file that already has tracked changes keeps them.
func TestTrackChangeCheck(t *testing.T) {
	engineEnv(t)
	dir := t.TempDir()
	spec := "../../testdata/apply/spec.json"
	tracked := filepath.Join(dir, "tracked.hwpx")
	runOK(t, "apply", "-spec", spec, "-o", tracked, "-author", "tester", "-date", "2026-01-02T03:04:05Z", sampleHWPX)
	small := filepath.Join(dir, "small.json")
	if err := os.WriteFile(small, []byte(`{"ops":[{"op":"set_text","loc":"s0/p0","text":"x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	again := filepath.Join(dir, "again.hwpx")
	runOK(t, "apply", "-spec", small, "-o", again, "-no-track", tracked)
	misnamed := filepath.Join(dir, "tracked.hwp")
	if b, err := os.ReadFile(tracked); err != nil {
		t.Fatal(err)
	} else if err := os.WriteFile(misnamed, b, 0o644); err != nil {
		t.Fatal(err)
	}
	old := engineTrackChanges
	t.Cleanup(func() { engineTrackChanges = old })
	engineTrackChanges = func(_ context.Context, name string) (int, error) {
		t.Errorf("engine asked to count %s", name)
		return 0, errors.New("not asked")
	}

	for _, tc := range []struct {
		name string
		n    int
	}{
		{sampleHWP, 0},
		{sampleHWPX, 0},
		{tracked, 2},
		{again, 2},
		{misnamed, 2},
	} {
		if n, err := trackChangeCount(tc.name, time.Minute); err != nil || n != tc.n {
			t.Errorf("trackChangeCount(%s) = %d, %v; want %d", tc.name, n, err, tc.n)
		}
	}
	info, _ := runOK(t, "info", tracked)
	if !strings.Contains(info, "track changes  2\n") {
		t.Errorf("info of %s does not say 2 tracked changes:\n%s", tracked, info)
	}

	for _, want := range []int{0, 1, 2} {
		if err := checkTrackChanges(want, tracked, time.Minute); err != nil {
			t.Errorf("input %d, output 2: %v", want, err)
		}
	}
	err := checkTrackChanges(2, sampleHWPX, time.Minute)
	if want := "tracked changes lost: input 2, output 0; the output is not kept"; err == nil || err.Error() != want {
		t.Errorf("input 2, output 0: %v, want %q", err, want)
	}
	if err := trackChangeLoss(229, 228); err == nil {
		t.Error("input 229, output 228 passed")
	}
	if err := trackChangeLoss(229, 229); err != nil {
		t.Errorf("input 229, output 229: %v", err)
	}
}

// A file the Go readers cannot open, such as HWP 3.0, is counted by the
// engine; when the engine cannot count it either, both reasons are given.
func TestTrackChangeCountEngine(t *testing.T) {
	hwp3 := filepath.Join(t.TempDir(), "old.hwp")
	if err := os.WriteFile(hwp3, []byte("HWP Document File V3.00 \x1a\x01\x02\x03\x04\x05"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := engineTrackChanges
	t.Cleanup(func() { engineTrackChanges = old })
	var asked []string
	engineTrackChanges = func(_ context.Context, name string) (int, error) {
		asked = append(asked, name)
		return 7, nil
	}
	if n, err := trackChangeCount(hwp3, time.Minute); err != nil || n != 7 {
		t.Errorf("HWP 3.0: %d, %v; want 7 from the engine", n, err)
	}
	if n, err := trackChangeCount(sampleHWP, time.Minute); err != nil || n != 0 {
		t.Errorf("%s: %d, %v; want 0", sampleHWP, n, err)
	}
	if want := []string{hwp3}; strings.Join(asked, ",") != strings.Join(want, ",") {
		t.Errorf("engine asked for %q, want %q", asked, want)
	}

	engineTrackChanges = func(context.Context, string) (int, error) { return 0, errors.New("cannot open") }
	_, err := trackChangeCount(hwp3, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "HWP 3.x files are not supported; built-in engine: cannot open") {
		t.Errorf("err = %v, want the reader's and the engine's reason", err)
	}
	if err := checkTrackChanges(0, hwp3, time.Minute); err == nil || !strings.HasPrefix(err.Error(), "count tracked changes in the output: ") {
		t.Errorf("uncountable output: %v", err)
	}
}

// A failed check writes nothing: an existing output kept by -force has its
// content afterwards, and no temporary file is left. Both engines write
// through convertChecked.
func TestConvertCheckedKeepsOutput(t *testing.T) {
	tmp := engineEnv(t)
	dst := filepath.Join(t.TempDir(), "out.hwpx")
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	conv := func(out string) (hancom.Result, error) {
		return hancom.Result{RegisterModule: true}, os.WriteFile(out, []byte("new"), 0o644)
	}
	var checked string
	lost := func(out string) error {
		b, _ := os.ReadFile(out)
		checked = string(b)
		return trackChangeLoss(3, 1)
	}
	_, err := convertChecked(dst, hancom.HWPX, true, lost, conv)
	if err == nil || !strings.Contains(err.Error(), "tracked changes lost: input 3, output 1") {
		t.Errorf("err = %v, want tracked changes lost", err)
	}
	if checked != "new" {
		t.Errorf("check saw %q, want the converted file", checked)
	}
	if b, _ := os.ReadFile(dst); string(b) != "old" {
		t.Errorf("existing output changed to %q", b)
	}
	assertEmptyDir(t, tmp)

	fresh := filepath.Join(t.TempDir(), "fresh.hwpx")
	if _, err := convertChecked(fresh, hancom.HWPX, false, lost, conv); err == nil {
		t.Error("lost tracked changes passed")
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Errorf("output written after a failed check: %v", err)
	}

	res, err := convertChecked(dst, hancom.HWPX, true, func(string) error { return nil }, conv)
	sum := md5.Sum([]byte("new"))
	if want := (hancom.Result{Out: dst, Format: hancom.HWPX, Size: 3, MD5: hex.EncodeToString(sum[:]), RegisterModule: true}); err != nil || res != want {
		t.Errorf("passed check: %+v, %v; want %+v", res, err, want)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Errorf("output %q, want %q", b, "new")
	}
	if _, err := convertChecked(dst, hancom.HWPX, false, nil, conv); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing output without -force: %v", err)
	}
	assertEmptyDir(t, tmp)
}
