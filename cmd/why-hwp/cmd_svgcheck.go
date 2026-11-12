package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jollaman999/why-hwp/internal/sfnt"
	"github.com/jollaman999/why-hwp/internal/svgtool"
)

func init() {
	register("svgcheck", "Check an SVG figure for overflowing text and loose arrows", runSVGCheck)
}

func runSVGCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("svgcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	regular := fs.String("regular", "", "regular TrueType font to measure with (default: $"+svgtool.EnvFontRegular+", then the OS font folders, the why-hwp font cache and a NanumGothic download)")
	bold := fs.String("bold", "", "bold TrueType font to measure with (default: $"+svgtool.EnvFontBold+", then the OS font folders, the why-hwp font cache and a NanumGothic download)")
	format := fs.String("format", "plain", "output format: plain or json")
	tol := fs.Float64("tol", 2, "how far in px an arrow end may be from a box edge")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp svgcheck [-regular font.ttf] [-bold font.ttf] [-format plain|json] [-tol 2] <in.svg>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Measures the text with the font's advance widths (font-size and")
		fmt.Fprintln(stderr, "text-anchor applied) and fails when text leaves the smallest <rect>")
		fmt.Fprintln(stderr, "holding its anchor point, when text of different <text> elements")
		fmt.Fprintln(stderr, "overlaps, when an arrow (line, polyline or straight path with")
		fmt.Fprintln(stderr, "marker-start or marker-end) ends more than -tol px from every box edge,")
		fmt.Fprintln(stderr, "or when an arrow passes through a box it neither starts nor ends in.")
		fmt.Fprintln(stderr, "Three or more arrow ends within 3*tol px of each other only warn.")
		fmt.Fprintln(stderr, "Only translate transforms are followed; anything skipped is listed in notes.")
		fmt.Fprintln(stderr, "Fonts are found as in why-hwp svgfont.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Exit status: 0 no violations (warnings allowed), 1 violations,")
		fmt.Fprintln(stderr, "2 usage error, a file that cannot be read or no usable font.")
		fmt.Fprintln(stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *format != "plain" && *format != "json" {
		fmt.Fprintf(stderr, "why-hwp svgcheck: unknown -format %q (want plain or json)\n", *format)
		return 2
	}
	if *tol < 0 {
		fmt.Fprintln(stderr, "why-hwp svgcheck: -tol must not be negative")
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	in := fs.Arg(0)
	data, err := os.ReadFile(in)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svgcheck: %v\n", err)
		return 2
	}
	weights, err := svgtool.NeededWeights(data)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svgcheck: %s: %v\n", in, err)
		return 2
	}
	finder := fontFinder(stderr)
	explicit := map[svgtool.Weight]string{svgtool.Regular: *regular, svgtool.Bold: *bold}
	fonts := map[svgtool.Weight]*sfnt.Font{}
	for _, w := range weights {
		lf, err := finder.Find(w, explicit[w])
		if err != nil {
			fmt.Fprintf(stderr, "why-hwp svgcheck: %v\n", err)
			return 2
		}
		fonts[w] = lf.Font
	}
	rep, err := svgtool.Check(data, svgtool.CheckOptions{Tol: *tol, Fonts: fonts})
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svgcheck: %s: %v\n", in, err)
		return 2
	}
	if *format == "json" {
		if code := writeJSON(stdout, stderr, rep); code != 0 {
			return 2
		}
	} else if err := rep.WritePlain(stdout); err != nil {
		fmt.Fprintf(stderr, "why-hwp svgcheck: %v\n", err)
		return 2
	}
	if rep.Violations > 0 {
		return 1
	}
	return 0
}
