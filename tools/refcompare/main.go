// Command refcompare measures how close a converter's output is to the
// output of Hancom Office (Hwp). It is a development tool and is not part of
// the why-hwp binary.
//
// "ref" makes reference outputs with Hancom Office on a remote Windows PC
// reached over ssh (WSL), and "cmp" compares candidate outputs against them.
// See README.md.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "ref":
		return runRef(args[1:], stdout, stderr)
	case "cmp":
		return runCmp(args[1:], stdout, stderr)
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "refcompare: unknown command %q\n\n", args[0])
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: refcompare <command> [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  ref  make reference PDF, TXT and HWP/HWPX outputs with Hancom Office on a remote PC")
	fmt.Fprintln(w, "  cmp  compare candidate outputs with the reference outputs")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'refcompare <command> -h' for the flags of a command.")
}
