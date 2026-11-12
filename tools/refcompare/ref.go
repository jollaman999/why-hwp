package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func runRef(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ref", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpus := fs.String("corpus", "", "folder with the .hwp and .hwpx inputs (required)")
	outdir := fs.String("out", "", "output folder; references go to <out>/ref (required)")
	exe := fs.String("exe", "", "prebuilt Windows why-hwp.exe (default: build it from this module)")
	force := fs.Bool("force", false, "make every reference again, even when it is up to date")
	timeout := fs.Duration("timeout", 120*time.Second, "Hancom Office time limit for one conversion")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: refcompare ref -corpus DIR -out DIR [-exe why-hwp.exe] [-force] [-timeout 120s]")
		fmt.Fprintln(stderr)
		fmt.Fprintf(stderr, "Makes <out>/ref/<name>.pdf, .txt and .hwpx (or .hwp) with Hancom Office on the PC\n")
		fmt.Fprintf(stderr, "given by %s (ssh target) and %s (WSL folder under /mnt/<drive>/).\n", envHost, envDir)
		fmt.Fprintf(stderr, "A document that prints two pages to a sheet also gets <name>%s, printed one page to a sheet.\n", oneUpSuffix)
		fmt.Fprintln(stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *corpus == "" || *outdir == "" || fs.NArg() != 0 || *timeout <= 0 {
		fs.Usage()
		return 2
	}
	logf := func(format string, a ...any) { fmt.Fprintf(stdout, format+"\n", a...) }

	docs, err := listCorpus(*corpus)
	if err != nil {
		fmt.Fprintf(stderr, "refcompare ref: %v\n", err)
		return 1
	}
	refDir := filepath.Join(*outdir, "ref")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "refcompare ref: %v\n", err)
		return 1
	}
	man, err := loadManifest(refDir)
	if err != nil {
		fmt.Fprintf(stderr, "refcompare ref: %v\n", err)
		return 1
	}

	type job struct {
		d   doc
		sha string
		to  string
	}
	var jobs []job
	sums := map[string]string{}
	upToDate := 0
	for _, d := range docs {
		sum, err := sha256File(d.Path)
		if err != nil {
			fmt.Fprintf(stderr, "refcompare ref: %v\n", err)
			return 1
		}
		sums[d.Name] = sum
		for _, to := range d.Targets() {
			name := d.Name + "." + to
			if !*force && man.fresh(name, sum) {
				logf("%s: up to date", name)
				upToDate++
				continue
			}
			jobs = append(jobs, job{d, sum, to})
		}
	}

	ctx := context.Background()
	var r *remote
	// ready makes the remote converter ready before the first conversion.
	// The int is the exit code when it cannot.
	ready := func() (int, error) {
		if r != nil {
			return 0, nil
		}
		nr, err := newRemote(*timeout, logf)
		if err != nil {
			return 2, err
		}
		if *exe == "" {
			logf("building Windows why-hwp from this module")
			built, err := buildConverter(ctx, *outdir)
			if err != nil {
				return 1, err
			}
			*exe = built
		}
		if err := nr.prepare(ctx, *exe); err != nil {
			return 1, err
		}
		r = nr
		return 0, nil
	}

	made, failed := 0, 0
	done := func(name string, start time.Time, err error) {
		if err != nil {
			failed++
			logf("%s: FAILED: %v", name, err)
			return
		}
		made++
		st, _ := os.Stat(filepath.Join(refDir, name))
		logf("%s: made, %d bytes, %.1fs", name, st.Size(), time.Since(start).Seconds())
	}
	for _, j := range jobs {
		if code, err := ready(); err != nil {
			fmt.Fprintf(stderr, "refcompare ref: %v\n", err)
			return code
		}
		name := j.d.Name + "." + j.to
		start := time.Now()
		out := filepath.Join(refDir, name)
		err := r.convert(ctx, j.d.Path, j.sha, j.to, out)
		if errors.Is(err, errEmptyText) {
			logf("%s: Hancom Office saved no text; keeping an empty reference", name)
			err = os.WriteFile(out, nil, 0o644)
		}
		if err == nil {
			err = man.record(name, j.sha)
		}
		done(name, start, err)
	}

	// A document that prints two pages to a sheet gets a one-up reference
	// as well (see oneup.go). The print setting is read from the HWPX
	// form of the document: the input, or what Hancom Office saved an
	// hwp input as.
	for _, d := range docs {
		sum := sums[d.Name]
		src := d.Path
		if d.Ext == "hwp" {
			if !man.fresh(d.Name+".hwpx", sum) {
				continue // its conversion failed above
			}
			src = filepath.Join(refDir, d.Name+".hwpx")
		}
		name := d.Name + oneUpSuffix
		method, err := printMethod(src)
		if err != nil {
			done(name, time.Now(), err)
			continue
		}
		if method != printTwoUp {
			continue
		}
		if !*force && man.fresh(name, sum) {
			logf("%s: up to date", name)
			upToDate++
			continue
		}
		if code, err := ready(); err != nil {
			fmt.Fprintf(stderr, "refcompare ref: %v\n", err)
			return code
		}
		start := time.Now()
		err = makeOneUp(ctx, r, src, filepath.Join(refDir, name))
		if err == nil {
			err = man.record(name, sum)
		}
		done(name, start, err)
	}

	if made == 0 && failed == 0 {
		logf("all %d documents are up to date", len(docs))
		return 0
	}
	logf("%d made, %d failed, %d up to date", made, failed, upToDate)
	if failed > 0 {
		return 1
	}
	return 0
}

// makeOneUp prints the HWPX file src one page to a sheet with Hancom Office
// and writes the PDF to out. The copy of src with the changed print setting
// (see writeOneUp) is kept only while it is printed.
func makeOneUp(ctx context.Context, r *remote, src, out string) error {
	dir, err := os.MkdirTemp("", "refcompare-oneup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	oneUp := filepath.Join(dir, "oneup.hwpx")
	if err := writeOneUp(src, oneUp); err != nil {
		return err
	}
	sum, err := sha256File(oneUp)
	if err != nil {
		return err
	}
	return r.convert(ctx, oneUp, sum, "pdf", out)
}
