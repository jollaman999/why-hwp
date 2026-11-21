// Command why-hwp reads and edits HWP / HWPX documents.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// command is one subcommand. Run receives the arguments after the
// subcommand name and returns the process exit code.
type command struct {
	summary string
	run     func(args []string, stdout, stderr io.Writer) int
}

// commands is filled by the cmd_*.go files in this package, one file per
// subcommand, so subcommands can be added without touching this file.
var commands = map[string]command{}

// newFlagSet creates the FlagSet of a command. Tests replace it to see the
// flags each command defines.
var newFlagSet = flag.NewFlagSet

func register(name, summary string, run func(args []string, stdout, stderr io.Writer) int) {
	if _, dup := commands[name]; dup {
		panic("why-hwp: duplicate command " + name)
	}
	commands[name] = command{summary: summary, run: run}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	name := args[0]
	switch name {
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return 0
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(stderr, "why-hwp: unknown command %q\n\n", name)
		usage(stderr)
		return 2
	}
	rest := args[1:]
	if runtime.GOOS == "windows" {
		rest = msysPathArgs(name, rest)
	}
	return cmd.run(rest, stdout, stderr)
}

// argFlags lists a command's flags by how msysPathArgs treats them.
type argFlags struct {
	paths []string // flags whose value is a file or folder path
	bools []string // flags that take no value
}

// commandArgFlags holds argFlags for every command. Every command takes
// only file paths as positional arguments. main_test.go checks this table
// against the flags the commands define.
var commandArgFlags = map[string]argFlags{
	"apply":    {paths: []string{"spec", "o"}, bools: []string{"no-track", "force"}},
	"convert":  {paths: []string{"o"}, bools: []string{"force"}},
	"info":     {},
	"render":   {paths: []string{"o"}, bools: []string{"force"}},
	"svg2png":  {paths: []string{"o"}, bools: []string{"trim"}},
	"svgcheck": {paths: []string{"regular", "bold"}},
	"svgfont":  {paths: []string{"regular", "bold", "o"}, bools: []string{"w"}},
	"text":     {},
	"verify":   {paths: []string{"orig", "allow", "banned"}, bools: []string{"ignore-case"}},
	"version":  {},
}

// msysPathArgs rewrites MSYS drive paths such as /c/dir/file.hwp to
// C:\dir\file.hwp in the arguments of command name that hold paths: the
// positional arguments and the values of its path flags, given as
// -flag value or -flag=value. Other flag values are kept as they are.
// Git Bash normally does this before it starts a native program, but it
// leaves an argument untouched when it contains glob characters like '['.
// Arguments are walked the way the flag package parses them.
func msysPathArgs(name string, args []string) []string {
	af := commandArgFlags[name]
	out := slices.Clone(args)
	for i := 0; i < len(out); i++ {
		a := out[i]
		if a == "--" {
			msysPaths(out[i+1:])
			break
		}
		if len(a) < 2 || a[0] != '-' {
			msysPaths(out[i:])
			break
		}
		flagName := strings.TrimPrefix(a[1:], "-")
		if eq := strings.IndexByte(flagName, '='); eq >= 0 {
			if slices.Contains(af.paths, flagName[:eq]) {
				at := strings.IndexByte(a, '=')
				out[i] = a[:at+1] + msysPath(a[at+1:])
			}
			continue
		}
		if slices.Contains(af.bools, flagName) || i+1 == len(out) {
			continue
		}
		i++
		if slices.Contains(af.paths, flagName) {
			out[i] = msysPath(out[i])
		}
	}
	return out
}

// msysPaths applies msysPath to every element of args in place.
func msysPaths(args []string) {
	for i, a := range args {
		args[i] = msysPath(a)
	}
}

// msysPath returns s in Windows form when it starts with /X/ or is exactly
// /X for a drive letter X, and s unchanged otherwise.
func msysPath(s string) string {
	if len(s) < 2 || s[0] != '/' || !isASCIILetter(s[1]) {
		return s
	}
	if len(s) > 2 && s[2] != '/' {
		return s
	}
	rest := strings.ReplaceAll(s[2:], "/", `\`)
	if rest == "" {
		rest = `\`
	}
	return strings.ToUpper(s[1:2]) + ":" + rest
}

func isASCIILetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: why-hwp <command> [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-10s %s\n", name, commands[name].summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'why-hwp <command> -h' for command options.")
}
