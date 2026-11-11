package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	old := version
	version = "v1.2.3-test"
	defer func() { version = old }()

	var out, errOut bytes.Buffer
	code := run([]string{"version"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, errOut.String())
	}
	if got, want := out.String(), "why-hwp v1.2.3-test\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"no-such-command"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), `unknown command "no-such-command"`) {
		t.Errorf("stderr = %q, want unknown command message", errOut.String())
	}
}

func TestNoArgsPrintsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "Usage: why-hwp") {
		t.Errorf("stderr = %q, want usage", errOut.String())
	}
}

func TestHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"help"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, name := range []string{"info", "text", "apply", "svg2png", "svgfont", "svgcheck", "verify", "convert", "version"} {
		if !strings.Contains(out.String(), "  "+name+" ") {
			t.Errorf("usage does not list %q:\n%s", name, out.String())
		}
	}
}
