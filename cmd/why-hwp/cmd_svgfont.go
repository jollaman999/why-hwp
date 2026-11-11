package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/jollaman999/why-hwp/internal/svgtool"
)

func init() {
	register("svgfont", "Embed the Korean font subset an SVG uses", runSVGFont)
}

func runSVGFont(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("svgfont", flag.ContinueOnError)
	fs.SetOutput(stderr)
	regular := fs.String("regular", "", "regular TrueType font (default: $"+svgtool.EnvFontRegular+", then a search of the OS font folders)")
	bold := fs.String("bold", "", "bold TrueType font (default: $"+svgtool.EnvFontBold+", then a search of the OS font folders)")
	family := fs.String("family", svgtool.DefaultFamily, "font-family name of the embedded font")
	out := fs.String("o", "", "output SVG path")
	inPlace := fs.Bool("w", false, "overwrite the input SVG")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp svgfont [-regular font.ttf] [-bold font.ttf] [-family NAME] (-o out.svg | -w) <in.svg>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Embeds, per weight, a TrueType subset holding only the characters of the")
		fmt.Fprintln(stderr, "SVG's <text> elements, as <style id=\"embedded-font\"> at the start of <defs>,")
		fmt.Fprintln(stderr, "and puts the family first in every font-family. Running it again on its")
		fmt.Fprintln(stderr, "output gives the same file. Text with font-weight bold or 600 and more")
		fmt.Fprintln(stderr, "uses the bold font.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Fonts must be TrueType (.ttf with glyf outlines); CFF (.otf), variable")
		fmt.Fprintln(stderr, "and collection (.ttc) fonts are refused. Without a flag or environment")
		fmt.Fprintln(stderr, "variable these are searched for: NotoSansKR-Regular/Bold.ttf,")
		fmt.Fprintln(stderr, "NanumGothic.ttf/NanumGothicBold.ttf, NanumSquareR/B.ttf, malgun/malgunbd.ttf.")
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
	if (*out == "") == !*inPlace {
		fmt.Fprintln(stderr, "why-hwp svgfont: give either -o out.svg or -w")
		return 2
	}
	if !svgtool.ValidFamily(*family) {
		fmt.Fprintf(stderr, "why-hwp svgfont: invalid -family %q (use letters, digits, space, _ and -)\n", *family)
		return 2
	}
	in := fs.Arg(0)
	dst := *out
	if *inPlace {
		dst = in
	}

	data, err := os.ReadFile(in)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svgfont: %v\n", err)
		return 1
	}
	sets, err := svgtool.Collect(data)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svgfont: %s: %v\n", in, err)
		return 1
	}
	finder := svgtool.FontFinder{GOOS: runtime.GOOS, Getenv: os.Getenv}
	explicit := map[svgtool.Weight]string{svgtool.Regular: *regular, svgtool.Bold: *bold}
	var faces []svgtool.Face
	for _, w := range []svgtool.Weight{svgtool.Regular, svgtool.Bold} {
		runes := sets[w]
		if len(runes) == 0 {
			continue
		}
		lf, err := finder.Find(w, explicit[w])
		if err != nil {
			fmt.Fprintf(stderr, "why-hwp svgfont: %v\n", err)
			return 1
		}
		face, sub, err := svgtool.SubsetFace(lf.Font, w, *family, runes)
		if err != nil {
			fmt.Fprintf(stderr, "why-hwp svgfont: %s: %v\n", lf.Path, err)
			return 1
		}
		fmt.Fprintf(stderr, "%s: %s (%s), %d characters, %d glyphs, %.1f KB\n",
			w, lf.Path, lf.Source, len(runes)-len(sub.Missing), sub.Glyphs, float64(len(sub.Font))/1024)
		if len(sub.Missing) > 0 {
			fmt.Fprintf(stderr, "%s: %d characters not in the font: %q\n", w, len(sub.Missing), string(sub.Missing))
		}
		if len(sub.Empty) > 0 {
			fmt.Fprintf(stderr, "%s: %d characters have an empty glyph and come out blank: %q\n", w, len(sub.Empty), string(sub.Empty))
		}
		faces = append(faces, face)
	}
	if len(faces) == 0 {
		fmt.Fprintln(stderr, "why-hwp svgfont: no <text> in the SVG; nothing embedded")
	}
	res, err := svgtool.Embed(data, *family, faces)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp svgfont: %s: %v\n", in, err)
		return 1
	}
	if err := os.WriteFile(dst, res, 0o644); err != nil {
		fmt.Fprintf(stderr, "why-hwp svgfont: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %.1f KB\n", dst, float64(len(res))/1024)
	return 0
}
