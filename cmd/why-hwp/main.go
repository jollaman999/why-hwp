// Command why-hwp reads and edits HWP / HWPX documents.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
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
	return cmd.run(args[1:], stdout, stderr)
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
