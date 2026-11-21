package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jollaman999/why-hwp/internal/engine"
)

func init() {
	register("render", "Render pages to PNG images", runRender)
}

func runRender(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pagesFlag := fs.String("pages", "", "pages to render, such as 1-3 or 1,4,7-9 (default: every page)")
	dpi := fs.Int("dpi", 150, "resolution in dots per inch")
	out := fs.String("o", "", "output folder, created when missing (default: the input's folder)")
	timeout := fs.Duration("timeout", 10*time.Minute, "time limit for rendering, including the first engine compile and font downloads")
	force := fs.Bool("force", false, "overwrite existing PNG files")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp render [-pages 1-3] [-dpi 150] [-o dir] [-timeout 10m] [-force] <in.hwp|in.hwpx>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Draws pages as PNG images with the built-in engine, the way 'why-hwp convert -to pdf'")
		fmt.Fprintln(stderr, "draws them. Page N of in.hwp is written to <dir>/in-pNNN.png, the number padded")
		fmt.Fprintln(stderr, "with zeros to three digits (more when the document has 1000 pages or more).")
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
	if *dpi <= 0 {
		fmt.Fprintln(stderr, "why-hwp render: -dpi must be positive")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "why-hwp render: -timeout must be positive")
		return 2
	}
	pages, err := parsePages(*pagesFlag)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp render: -pages: %v\n", err)
		return 2
	}
	in := fs.Arg(0)
	switch strings.ToLower(filepath.Ext(in)) {
	case ".hwp", ".hwpx":
	default:
		fmt.Fprintf(stderr, "why-hwp render: %s: reads .hwp and .hwpx only\n", in)
		return 2
	}
	if _, err := os.Stat(in); err != nil {
		fmt.Fprintf(stderr, "why-hwp render: %v\n", err)
		return 1
	}
	dir := *out
	if dir == "" {
		dir = filepath.Dir(in)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "why-hwp render: %v\n", err)
		return 1
	}
	base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var written []string
	err = engine.Render(ctx, in, *dpi, pages, func(count int, images []engine.PageImage) error {
		dsts := make([]string, len(images))
		for i, img := range images {
			dsts[i] = filepath.Join(dir, pageFileName(base, img.Page, count))
			if !*force {
				if _, err := os.Lstat(dsts[i]); err == nil {
					return fmt.Errorf("%s: already exists (use -force to overwrite)", dsts[i])
				}
			}
		}
		for i, img := range images {
			if _, _, err := copyOut(img.Path, dsts[i], *force); err != nil {
				return err
			}
			written = append(written, fmt.Sprintf("%s: page %d, %dx%d px, %d dpi", dsts[i], img.Page, img.Width, img.Height, *dpi))
		}
		return nil
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("rendering did not finish within %s (raise -timeout)", *timeout)
		}
		fmt.Fprintf(stderr, "why-hwp render: %s: %v\n", in, err)
		return 1
	}
	for _, line := range written {
		fmt.Fprintln(stdout, line)
	}
	return 0
}

// parsePages reads a page list such as "1-3,5": page numbers and ranges
// separated by commas. It returns the pages in ascending order without
// repeats, or nil for "" (every page).
func parsePages(s string) ([]int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	seen := map[int]bool{}
	var pages []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		first, err := pageNumber(lo, part)
		if err != nil {
			return nil, err
		}
		last := first
		if isRange {
			if last, err = pageNumber(hi, part); err != nil {
				return nil, err
			}
			if last < first {
				return nil, fmt.Errorf("%q: the range runs backwards", part)
			}
		}
		for p := first; p <= last; p++ {
			if !seen[p] {
				seen[p] = true
				pages = append(pages, p)
			}
		}
	}
	sort.Ints(pages)
	return pages, nil
}

// maxPage bounds the page numbers -pages takes, so that a mistyped range
// does not expand into millions of pages.
const maxPage = 100000

// pageNumber reads one page number of the -pages item part.
func pageNumber(s, part string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > maxPage {
		return 0, fmt.Errorf("%q: want page numbers from 1 to %d, such as 1-3 or 1,4", part, maxPage)
	}
	return n, nil
}

// pageFileName names the PNG of page in a document of count pages whose
// file name without extension is base: base-p001.png, with the page number
// padded to three digits, or to the digits of count when that is more.
func pageFileName(base string, page, count int) string {
	width := len(strconv.Itoa(count))
	if width < 3 {
		width = 3
	}
	return fmt.Sprintf("%s-p%0*d.png", base, width, page)
}
