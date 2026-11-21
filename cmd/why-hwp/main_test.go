package main

import (
	"bytes"
	"flag"
	"slices"
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
		cmd  string
		in   []string
		want []string
	}{
		{"text", []string{"/c/x/[a]/b.hwp"}, []string{`C:\x\[a]\b.hwp`}},
		{"text", []string{"/D/doc.hwp"}, []string{`D:\doc.hwp`}},
		{"text", []string{"/c"}, []string{`C:\`}},
		{"text", []string{"/c/"}, []string{`C:\`}},
		{
			"convert",
			[]string{"-to", "hwpx", "-o", "/c/[a]/out.hwpx", "/c/[a]/in.hwp"},
			[]string{"-to", "hwpx", "-o", `C:\[a]\out.hwpx`, `C:\[a]\in.hwp`},
		},
		{
			"apply",
			[]string{"-o=/c/[a]/out.hwpx", "--spec=/e/s.json", "/c/in.hwpx"},
			[]string{`-o=C:\[a]\out.hwpx`, `--spec=E:\s.json`, `C:\in.hwpx`},
		},
		{
			"apply",
			[]string{"-author", "/c/name", "-date", "/d/x", "-spec", "/c/s.json", "-o", "/c/[a]/x", "-no-track", "/c/in.hwpx"},
			[]string{"-author", "/c/name", "-date", "/d/x", "-spec", `C:\s.json`, "-o", `C:\[a]\x`, "-no-track", `C:\in.hwpx`},
		},
		{
			"apply",
			[]string{"-author=/c/name", "--author", "/c/[a]", "-force", "-o", "/c/out.hwpx", "/c/in.hwpx"},
			[]string{"-author=/c/name", "--author", "/c/[a]", "-force", "-o", `C:\out.hwpx`, `C:\in.hwpx`},
		},
		{"apply", []string{"-force=true", "/c/in.hwpx"}, []string{"-force=true", `C:\in.hwpx`}},
		{"text", []string{"-format=json", "-view", "/c/x", "/c/in.hwpx"}, []string{"-format=json", "-view", "/c/x", `C:\in.hwpx`}},
		{"convert", []string{"-format=json", "-o=rel/out.hwpx"}, []string{"-format=json", "-o=rel/out.hwpx"}},
		{
			"render",
			[]string{"-dpi", "/c/1", "-timeout", "10m", "-o", "/c/[a]", "/c/[a]/in.hwp"},
			[]string{"-dpi", "/c/1", "-timeout", "10m", "-o", `C:\[a]`, `C:\[a]\in.hwp`},
		},
		{
			"svgfont",
			[]string{"-family", "/c/f", "-regular", "/c/r.ttf", "-bold=/c/b.ttf", "-w", "/c/in.svg"},
			[]string{"-family", "/c/f", "-regular", `C:\r.ttf`, `-bold=C:\b.ttf`, "-w", `C:\in.svg`},
		},
		{
			"verify",
			[]string{"-orig", "/c/o.hwp", "-allow=/c/a.txt", "-banned", "/c/b.txt", "-ignore-case", "/c/in.hwpx"},
			[]string{"-orig", `C:\o.hwp`, `-allow=C:\a.txt`, "-banned", `C:\b.txt`, "-ignore-case", `C:\in.hwpx`},
		},
		{"text", []string{"/c/in.hwpx", "-o", "/c/x"}, []string{`C:\in.hwpx`, "-o", `C:\x`}},
		{"text", []string{"--", "-/c", "/c/x"}, []string{"--", "-/c", `C:\x`}},
		{"apply", []string{"-o"}, []string{"-o"}},
		{
			"text",
			[]string{`C:\x\b.hwp`, "C:/x/b.hwp", "b.hwp", "-", ""},
			[]string{`C:\x\b.hwp`, "C:/x/b.hwp", "b.hwp", "-", ""},
		},
		{
			"text",
			[]string{"/cd/x.hwp", "/1/x.hwp", "//srv/share", "/", "/c:x"},
			[]string{"/cd/x.hwp", "/1/x.hwp", "//srv/share", "/", "/c:x"},
		},
		{"text", []string{"text=/c/x"}, []string{"text=/c/x"}},
	}
	for _, tt := range tests {
		got := msysPathArgs(tt.cmd, tt.in)
		if len(got) != len(tt.want) || strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("msysPathArgs(%q, %q) = %q, want %q", tt.cmd, tt.in, got, tt.want)
		}
	}
}

func TestMsysPathArgsKeepsInput(t *testing.T) {
	in := []string{"-o", "/c/out.hwpx", "/c/x.hwp"}
	msysPathArgs("convert", in)
	if strings.Join(in, "|") != "-o|/c/out.hwpx|/c/x.hwp" {
		t.Errorf("input changed to %q", in)
	}
}

// nonPathStringFlags are the string flags whose value is not a path.
// A new string flag must go either here or in commandArgFlags.
var nonPathStringFlags = map[string][]string{
	"apply":    {"author", "date"},
	"convert":  {"to", "engine"},
	"info":     {"format", "view"},
	"render":   {"pages"},
	"svgcheck": {"format"},
	"svgfont":  {"family"},
	"text":     {"format", "view"},
	"verify":   {"format", "view"},
}

// commandFlagTypes runs "why-hwp <name> -h" and returns the kind of every
// flag defined on the FlagSet the command creates: "bool", "string" or
// "other".
func commandFlagTypes(t *testing.T, name string) map[string]string {
	t.Helper()
	var sets []*flag.FlagSet
	old := newFlagSet
	newFlagSet = func(name string, h flag.ErrorHandling) *flag.FlagSet {
		fs := old(name, h)
		sets = append(sets, fs)
		return fs
	}
	defer func() { newFlagSet = old }()
	var out, errOut bytes.Buffer
	if code := run([]string{name, "-h"}, &out, &errOut); code != 0 {
		t.Fatalf("%s -h: exit code = %d, want 0 (stderr %q)", name, code, errOut.String())
	}
	if len(sets) != 1 {
		t.Fatalf("%s -h: created %d FlagSets with newFlagSet, want 1", name, len(sets))
	}
	types := map[string]string{}
	sets[0].VisitAll(func(f *flag.Flag) {
		kind := "other"
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			kind = "bool"
		} else if g, ok := f.Value.(flag.Getter); ok {
			if _, ok := g.Get().(string); ok {
				kind = "string"
			}
		}
		types[f.Name] = kind
	})
	return types
}

func TestCommandArgFlagsMatchFlags(t *testing.T) {
	for name := range commands {
		if _, ok := commandArgFlags[name]; !ok {
			t.Errorf("command %q is missing from commandArgFlags", name)
		}
	}
	for name, af := range commandArgFlags {
		if _, ok := commands[name]; !ok {
			t.Errorf("commandArgFlags has unknown command %q", name)
			continue
		}
		types := commandFlagTypes(t, name)
		for _, f := range af.paths {
			if typ, ok := types[f]; !ok || typ != "string" {
				t.Errorf("%s: path flag -%s is not a string flag (type %q, defined %v)", name, f, typ, ok)
			}
			if slices.Contains(nonPathStringFlags[name], f) {
				t.Errorf("%s: -%s is both a path flag and in nonPathStringFlags", name, f)
			}
		}
		for _, f := range af.bools {
			if typ, ok := types[f]; !ok || typ != "bool" {
				t.Errorf("%s: bool flag -%s is not a bool flag (type %q, defined %v)", name, f, typ, ok)
			}
		}
		for _, f := range nonPathStringFlags[name] {
			if typ, ok := types[f]; !ok || typ != "string" {
				t.Errorf("%s: non-path flag -%s is not a string flag (type %q, defined %v)", name, f, typ, ok)
			}
		}
		for f, typ := range types {
			switch typ {
			case "bool":
				if !slices.Contains(af.bools, f) {
					t.Errorf("%s: bool flag -%s is missing from commandArgFlags bools", name, f)
				}
			case "string":
				if !slices.Contains(af.paths, f) && !slices.Contains(nonPathStringFlags[name], f) {
					t.Errorf("%s: string flag -%s is in neither commandArgFlags paths nor nonPathStringFlags", name, f)
				}
			}
		}
	}
}
