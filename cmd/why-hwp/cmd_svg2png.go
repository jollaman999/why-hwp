package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	out := fs.String("o", "", "output PNG path (default: input path with .png)")
	trim := fs.Bool("trim", false, "cut white or transparent margins")
	timeout := fs.Duration("timeout", 2*time.Minute, "time limit for rendering, including the first engine compile")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp svg2png [-width-mm N | -width-px N] [-dpi 600] [-trim] [-timeout 2m] [-o out.png] <in.svg>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Without -width-mm or -width-px the SVG's own size (96 px per inch) is rendered at -dpi.")
		fmt.Fprintln(stderr, "Text is drawn only with fonts embedded in the SVG (run 'why-hwp svgfont' first);")
		fmt.Fprintln(stderr, "system fonts are not used, so the PNG is the same on every machine.")
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
	scan, err := svg2png.ScanFonts(data)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svg2png: %s: %v\n", in, err)
		return 1
	}
	for _, s := range scan.Skipped {
		fmt.Fprintf(stderr, "why-hwp svg2png: warning: %s: skipped embedded %s\n", in, s)
	}
	if len(scan.Fonts) == 0 && scan.HasText {
		fmt.Fprintf(stderr, "why-hwp svg2png: warning: %s has text but no embedded font, so no text will be drawn; run 'why-hwp svgfont' on it first\n", in)
	}
	if *timeout <= 0 {
		*timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	res, err := svg2png.Render(ctx, svg2png.Options{
		SVG:    data,
		Fonts:  scan.Fonts,
		Width:  w,
		Height: h,
		DPI:    *dpi,
		Trim:   *trim,
	})
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(stderr, "why-hwp svg2png: rendering did not finish within %s (raise -timeout)\n", *timeout)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svg2png: %v\n", err)
		return 1
	}
	if err := os.WriteFile(dst, res.PNG, 0o644); err != nil {
		fmt.Fprintf(stderr, "why-hwp svg2png: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %dx%d px, %.0f dpi, %d embedded font(s)\n", dst, res.Width, res.Height, *dpi, len(scan.Fonts))
	return 0
}
