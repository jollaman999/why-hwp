package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jollaman999/why-hwp/internal/hancom"
)

func init() {
	register("convert", "Convert to HWP, HWPX, PDF or TXT with Hancom Office (Windows)", runConvert)
}

func runConvert(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	to := fs.String("to", "", "output format: hwp, hwpx, pdf or txt (default: from the -o extension)")
	out := fs.String("o", "", "output path (default: input path with the -to extension)")
	timeout := fs.Duration("timeout", 120*time.Second, "Hancom Office time limit; on timeout only the Hwp process started by this run is killed")
	force := fs.Bool("force", false, "overwrite an existing output file")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp convert [-to hwp|hwpx|pdf|txt] [-o out] [-timeout 120s] [-force] <in.hwp|in.hwpx|in.docx>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Converts with the installed Hancom Office (Windows only). Text output is UTF-8.")
		fmt.Fprintln(stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "why-hwp convert: -timeout must be positive")
		return 2
	}
	in := fs.Arg(0)
	dst, f, err := hancom.Plan(in, *out, *to)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp convert: %v\n", err)
		return 2
	}
	if _, err := os.Stat(in); err != nil {
		fmt.Fprintf(stderr, "why-hwp convert: %v\n", err)
		return 1
	}
	res, err := hancom.Convert(context.Background(), hancom.Options{
		In:      in,
		Out:     dst,
		To:      f,
		Timeout: *timeout,
		Force:   *force,
	})
	if err != nil {
		if errors.Is(err, hancom.ErrUnsupported) {
			fmt.Fprintf(stderr, "why-hwp convert: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "why-hwp convert: %s: %v\n", in, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %s, %d bytes, md5 %s\n", res.Out, res.Format, res.Size, res.MD5)
	return 0
}
