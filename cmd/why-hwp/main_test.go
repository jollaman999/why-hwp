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
	for _, name := range []string{"info", "text", "apply", "svg2png", "svgfont", "svgcheck", "verify", "convert", "render", "version"} {
		if !strings.Contains(out.String(), "  "+name+" ") {
			t.Errorf("usage does not list %q:\n%s", name, out.String())
		}
	}
}

func TestMsysPathArgs(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{[]string{"/c/x/[a]/b.hwp"}, []string{`C:\x\[a]\b.hwp`}},
		{[]string{"/D/doc.hwp"}, []string{`D:\doc.hwp`}},
		{[]string{"/c"}, []string{`C:\`}},
		{[]string{"/c/"}, []string{`C:\`}},
		{
			[]string{"-to", "hwpx", "-o", "/c/[a]/out.hwpx", "/c/[a]/in.hwp"},
			[]string{"-to", "hwpx", "-o", `C:\[a]\out.hwpx`, `C:\[a]\in.hwp`},
		},
		{
			[]string{"-o=/c/[a]/out.hwpx", "--spec=/e/s.json"},
			[]string{`-o=C:\[a]\out.hwpx`, `--spec=E:\s.json`},
		},
		{[]string{"-format=json", "-o=rel/out.hwpx"}, []string{"-format=json", "-o=rel/out.hwpx"}},
		{
			[]string{`C:\x\b.hwp`, "C:/x/b.hwp", "b.hwp", "-", ""},
			[]string{`C:\x\b.hwp`, "C:/x/b.hwp", "b.hwp", "-", ""},
		},
		{
			[]string{"/cd/x.hwp", "/1/x.hwp", "//srv/share", "/", "/c:x"},
			[]string{"/cd/x.hwp", "/1/x.hwp", "//srv/share", "/", "/c:x"},
		},
		{[]string{"text=/c/x"}, []string{"text=/c/x"}},
	}
	for _, tt := range tests {
		got := msysPathArgs(tt.in)
		if len(got) != len(tt.want) || strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("msysPathArgs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMsysPathArgsKeepsInput(t *testing.T) {
	in := []string{"/c/x.hwp"}
	msysPathArgs(in)
	if in[0] != "/c/x.hwp" {
		t.Errorf("input changed to %q", in[0])
	}
}
