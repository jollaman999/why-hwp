package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jollaman999/why-hwp/internal/engine"
)

// engineEnv keeps the engine's compile and font caches and apply's
// temporary files in directories of the test, and stops font downloads.
// It returns the temporary directory apply uses.
func engineEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("TMPDIR", tmp)
	t.Setenv(engine.EnvOffline, "1")
	t.Setenv(engine.EnvSystemFonts, "0")
	return tmp
}

func runOK(t *testing.T, args ...string) (string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("%v: exit code = %d (stderr %q)", args, code, errOut.String())
	}
	return out.String(), errOut.String()
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		t.Errorf("left behind in the temporary directory: %s", e.Name())
	}
}

// The HWPX the engine makes from an .hwp file has the locations and text
// that 'why-hwp text' prints for the .hwp file, so locations taken from
// the .hwp work in apply.
func TestEngineHWPXMatchesHWP(t *testing.T) {
	engineEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conv := filepath.Join(t.TempDir(), "sample.hwpx")
	if err := engine.Convert(ctx, sampleHWP, conv, "hwpx"); err != nil {
		t.Fatal(err)
	}
	want, _ := runOK(t, "text", sampleHWP)
	got, _ := runOK(t, "text", conv)
	if got != want {
		t.Errorf("text of the converted file differs\n got:\n%s\nwant:\n%s", got, want)
	}
}

// apply on an .hwp file gives the same text as apply on the .hwpx copy of
// the same document, says that it converted, and removes its temporary file.
func TestApplyHWP(t *testing.T) {
	tmp := engineEnv(t)
	dir := t.TempDir()
	spec := "../../testdata/apply/spec.json"
	fromHWP, fromHWPX := filepath.Join(dir, "b.hwpx"), filepath.Join(dir, "x.hwpx")
	_, msg := runOK(t, "apply", "-spec", spec, "-o", fromHWP, "-no-track", sampleHWP)
	if want := "converted " + sampleHWP + " to HWPX with the built-in engine"; !strings.Contains(msg, want) {
		t.Errorf("stderr = %q, want %q", msg, want)
	}
	if want := "why-hwp verify -orig " + sampleHWP + " " + fromHWP; !strings.Contains(msg, want) {
		t.Errorf("stderr = %q, want %q", msg, want)
	}
	assertEmptyDir(t, tmp)
	runOK(t, "apply", "-spec", spec, "-o", fromHWPX, "-no-track", sampleHWPX)
	if got, want := textColumn(t, fromHWP), textColumn(t, fromHWPX); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("apply on hwp:\n got %q\nwant %q", got, want)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"ops":[{"op":"delete","loc":"s0/p999"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "bad.hwpx")
	var stdout, errOut bytes.Buffer
	if code := run([]string{"apply", "-spec", bad, "-o", out, "-no-track", sampleHWP}, &stdout, &errOut); code != 1 {
		t.Errorf("bad loc: exit code = %d, want 1 (stderr %q)", code, errOut.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("bad loc: output written")
	}
	assertEmptyDir(t, tmp)
}

// Encrypted .hwp files are refused before the engine sees them.
func TestApplyEncryptedHWP(t *testing.T) {
	tmp := engineEnv(t)
	dir := t.TempDir()
	sample, err := os.ReadFile(sampleHWP)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(sample, []byte("HWP Document File"))
	if i < 0 {
		t.Fatal("signature not found in sample")
	}
	for _, tc := range []struct {
		bit byte
		msg string
	}{
		{2, "document is encrypted"},
		{4, "distribution document"},
	} {
		data := bytes.Clone(sample)
		data[i+36] |= tc.bit
		in := filepath.Join(dir, "locked.hwp")
		if err := os.WriteFile(in, data, 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "out.hwpx")
		var stdout, errOut bytes.Buffer
		code := run([]string{"apply", "-spec", "../../testdata/apply/spec.json", "-o", out, "-no-track", in}, &stdout, &errOut)
		if code != 1 || !strings.Contains(errOut.String(), tc.msg) {
			t.Errorf("bit %d: exit code = %d, stderr %q, want 1 and %q", tc.bit, code, errOut.String(), tc.msg)
		}
		if strings.Contains(errOut.String(), "converted") {
			t.Errorf("bit %d: converted before refusing: %q", tc.bit, errOut.String())
		}
		if _, err := os.Stat(out); err == nil {
			t.Errorf("bit %d: output written", tc.bit)
		}
		assertEmptyDir(t, tmp)
	}
}
