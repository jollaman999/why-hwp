package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jollaman999/why-hwp/internal/svg2png"
)

func init() {
	register("svg2png", "Render an SVG file to PNG", runSVG2PNG)
}

func runSVG2PNG(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("svg2png", flag.ContinueOnError)
	fs.SetOutput(stderr)
	widthMM := fs.Float64("width-mm", 0, "output width in millimetres (height follows the SVG aspect ratio)")
	widthPx := fs.Int("width-px", 0, "output width in pixels (height follows the SVG aspect ratio)")
	dpi := fs.Float64("dpi", 600, "resolution for -width-mm and for the SVG's own size; stored in the PNG")
	browser := fs.String("browser", "", "browser executable (default: $"+svg2png.EnvBrowser+", then Chrome, Edge, Chromium, Firefox)")
	out := fs.String("o", "", "output PNG path (default: input path with .png)")
	trim := fs.Bool("trim", false, "cut white or transparent margins")
	timeout := fs.Duration("timeout", 60*time.Second, "browser time limit")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp svg2png [-width-mm N | -width-px N] [-dpi 600] [-browser path] [-o out.png] <in.svg>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Without -width-mm or -width-px the SVG's own size (96 px per inch) is rendered at -dpi.")
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
	if *widthMM > 0 && *widthPx > 0 {
		fmt.Fprintln(stderr, "why-hwp svg2png: use either -width-mm or -width-px, not both")
		return 2
	}
	if *widthMM < 0 || *widthPx < 0 || *dpi <= 0 {
		fmt.Fprintln(stderr, "why-hwp svg2png: -width-mm, -width-px and -dpi must be positive")
		return 2
	}
	in := fs.Arg(0)
	dst := *out
	if dst == "" {
		dst = strings.TrimSuffix(in, filepath.Ext(in)) + ".png"
	}

	data, err := os.ReadFile(in)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svg2png: %v\n", err)
		return 1
	}
	sw, sh, err := svg2png.Size(data)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svg2png: %s: %v\n", in, err)
		return 1
	}
	w, h, err := svg2png.TargetSize(sw, sh, *widthMM, *widthPx, *dpi)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svg2png: %v\n", err)
		return 1
	}
	b, err := svg2png.DefaultFinder(runtime.GOOS).Find(*browser)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svg2png: %v\n", err)
		return 1
	}
	res, err := svg2png.Render(context.Background(), svg2png.Options{
		Browser: b,
		SVG:     data,
		Width:   w,
		Height:  h,
		DPI:     *dpi,
		Trim:    *trim,
		Timeout: *timeout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svg2png: %v\n", err)
		return 1
	}
	if err := os.WriteFile(dst, res.PNG, 0o644); err != nil {
		fmt.Fprintf(stderr, "why-hwp svg2png: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %dx%d px, %.0f dpi, %s (%s)\n", dst, res.Width, res.Height, *dpi, b.Name, b.Path)
	return 0
}
