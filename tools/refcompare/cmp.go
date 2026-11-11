package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// row is the result for one document.
type row struct {
	Name    string
	Err     string // why the PDF could not be compared
	PDF     *pdfResult
	Txt     *txtResult
	TxtNote string
	RT      *pdfResult // candidate HWP/HWPX printed by Hancom Office
	RTNote  string
	Pass    bool
}

func runCmp(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cmp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outdir := fs.String("out", "", "folder that holds ref/ from 'refcompare ref'; report.md is written here (required)")
	candDir := fs.String("cand", "", "folder with candidate outputs <name>.pdf, <name>.txt, <name>.hwpx|hwp (required)")
	tol := fs.Float64("tol", 1.0, "largest allowed word deviation in points")
	fit := fs.String("fit", fitScale, "how a reference page is fitted to the candidate page: scale, shift or none")
	skipMemo := fs.Bool("skip-memo", false, "ignore reference words that fall outside the fitted page (Hancom memo balloons)")
	reconvert := fs.Bool("reconvert", false, "also print the candidate HWP/HWPX to PDF with Hancom Office on the remote PC and compare it with the reference PDF")
	exe := fs.String("exe", "", "with -reconvert: prebuilt Windows why-hwp.exe (default: build it from this module)")
	force := fs.Bool("force", false, "with -reconvert: print every candidate again")
	timeout := fs.Duration("timeout", 120*time.Second, "with -reconvert: Hancom Office time limit for one conversion")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: refcompare cmp -out DIR -cand DIR [-tol 1.0] [-fit scale] [-skip-memo] [-reconvert]")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Compares the candidate outputs with <out>/ref and writes the table to stdout and <out>/report.md.")
		fmt.Fprintln(stderr, "Exit code 0 when every document passes, 1 when one fails or cannot be compared.")
		fmt.Fprintln(stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *outdir == "" || *candDir == "" || fs.NArg() != 0 || *tol < 0 || *timeout <= 0 {
		fs.Usage()
		return 2
	}
	switch *fit {
	case fitScale, fitShift, fitNone:
	default:
		fmt.Fprintf(stderr, "refcompare cmp: -fit %q: want scale, shift or none\n", *fit)
		return 2
	}
	opt := cmpOptions{Tol: *tol, Fit: *fit, SkipMemo: *skipMemo}
	refDir := filepath.Join(*outdir, "ref")
	names, err := refNames(refDir)
	if err != nil {
		fmt.Fprintf(stderr, "refcompare cmp: %v\n", err)
		return 1
	}

	var rc *reconverter
	if *reconvert {
		rc, err = newReconverter(*outdir, *exe, *force, *timeout, func(f string, a ...any) { fmt.Fprintf(stderr, f+"\n", a...) })
		if err != nil {
			fmt.Fprintf(stderr, "refcompare cmp: %v\n", err)
			return 1
		}
	}

	rows := make([]row, 0, len(names))
	for _, name := range names {
		rows = append(rows, compareDoc(name, refDir, *candDir, opt, rc))
	}
	report := renderReport(rows, opt, *candDir, *reconvert)
	fmt.Fprint(stdout, report)
	if err := os.WriteFile(filepath.Join(*outdir, "report.md"), []byte(report), 0o644); err != nil {
		fmt.Fprintf(stderr, "refcompare cmp: %v\n", err)
		return 1
	}
	for _, r := range rows {
		if !r.Pass {
			return 1
		}
	}
	return 0
}

// refNames lists the documents that have a reference PDF.
func refNames(refDir string) ([]string, error) {
	ents, err := os.ReadDir(refDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		n, ok := strings.CutSuffix(e.Name(), ".pdf")
		if ok && inputExt(n) != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("%s: no reference PDFs (run 'refcompare ref' first)", refDir)
	}
	return names, nil
}

func compareDoc(name, refDir, candDir string, opt cmpOptions, rc *reconverter) row {
	r := row{Name: name}
	ref, err := readPDFWords(filepath.Join(refDir, name+".pdf"))
	if err != nil {
		r.Err = err.Error()
		return r
	}
	candPDF := filepath.Join(candDir, name+".pdf")
	if _, err := os.Stat(candPDF); err != nil {
		r.Err = "no candidate PDF"
	} else if cand, err := readPDFWords(candPDF); err != nil {
		r.Err = err.Error()
	} else {
		res := comparePDF(ref, cand, opt)
		r.PDF = &res
	}

	rt, errRT := os.ReadFile(filepath.Join(refDir, name+".txt"))
	ct, errCT := os.ReadFile(filepath.Join(candDir, name+".txt"))
	switch {
	case errRT != nil:
		r.TxtNote = "no reference"
	case errCT != nil:
		r.TxtNote = "no candidate"
	default:
		t := compareText(rt, ct)
		r.Txt = &t
	}

	if rc != nil {
		other := otherFormat(inputExt(name))
		src := filepath.Join(candDir, name+"."+other)
		if _, err := os.Stat(src); err != nil {
			r.RTNote = "no candidate " + other
		} else if pdf, err := rc.toPDF(src); err != nil {
			r.RTNote = "Hancom failed: " + err.Error()
		} else if words, err := readPDFWords(pdf); err != nil {
			r.RTNote = err.Error()
		} else {
			res := comparePDF(ref, words, opt)
			r.RT = &res
		}
	}

	r.Pass = r.PDF != nil && r.PDF.pass(opt)
	if rc != nil && (r.RT == nil || !r.RT.pass(opt)) {
		r.Pass = false
	}
	return r
}

// reconverter prints candidate HWP/HWPX files to PDF with Hancom Office and
// keeps the PDFs in <out>/reconv.
type reconverter struct {
	dir   string
	man   *manifest
	r     *remote
	force bool
	exe   string
	ready bool
	log   func(string, ...any)
}

func newReconverter(outdir, exe string, force bool, timeout time.Duration, log func(string, ...any)) (*reconverter, error) {
	dir := filepath.Join(outdir, "reconv")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	man, err := loadManifest(dir)
	if err != nil {
		return nil, err
	}
	r, err := newRemote(timeout, log)
	if err != nil {
		return nil, err
	}
	return &reconverter{dir: dir, man: man, r: r, force: force, exe: exe, log: log}, nil
}

func (c *reconverter) toPDF(src string) (string, error) {
	sum, err := sha256File(src)
	if err != nil {
		return "", err
	}
	name := filepath.Base(src) + ".pdf"
	out := filepath.Join(c.dir, name)
	if !c.force && c.man.fresh(name, sum) {
		return out, nil
	}
	ctx := context.Background()
	if !c.ready {
		if c.exe == "" {
			c.log("building Windows why-hwp from this module")
			if c.exe, err = buildConverter(ctx, filepath.Dir(c.dir)); err != nil {
				return "", err
			}
		}
		if err := c.r.prepare(ctx, c.exe); err != nil {
			return "", err
		}
		c.ready = true
	}
	c.log("printing %s with Hancom Office", filepath.Base(src))
	if err := c.r.convert(ctx, src, sum, "pdf", out); err != nil {
		return "", err
	}
	if err := c.man.record(name, sum); err != nil {
		return "", err
	}
	return out, nil
}
