package main

import (
	"flag"
	"fmt"
	"io"
)

func init() {
	register("version", "Print the version", runVersion)
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	fmt.Fprintf(stdout, "why-hwp %s\n", version)
	return 0
}
