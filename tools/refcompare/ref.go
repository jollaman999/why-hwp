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
	for _, d := range docs {
		sum, err := sha256File(d.Path)
		if err != nil {
			fmt.Fprintf(stderr, "refcompare ref: %v\n", err)
			return 1
		}
		for _, to := range d.Targets() {
			name := d.Name + "." + to
			if !*force && man.fresh(name, sum) {
				logf("%s: up to date", name)
				continue
			}
			jobs = append(jobs, job{d, sum, to})
		}
	}
	if len(jobs) == 0 {
		logf("all %d documents are up to date", len(docs))
		return 0
	}

	ctx := context.Background()
	r, err := newRemote(*timeout, logf)
	if err != nil {
		fmt.Fprintf(stderr, "refcompare ref: %v\n", err)
		return 2
	}
	if *exe == "" {
		logf("building Windows why-hwp from this module")
		built, err := buildConverter(ctx, *outdir)
		if err != nil {
			fmt.Fprintf(stderr, "refcompare ref: %v\n", err)
			return 1
		}
		*exe = built
	}
	if err := r.prepare(ctx, *exe); err != nil {
		fmt.Fprintf(stderr, "refcompare ref: %v\n", err)
		return 1
	}

	failed := 0
	for _, j := range jobs {
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
		if err != nil {
			failed++
			logf("%s: FAILED: %v", name, err)
			continue
		}
		st, _ := os.Stat(out)
		logf("%s: made, %d bytes, %.1fs", name, st.Size(), time.Since(start).Seconds())
	}
	logf("%d made, %d failed, %d up to date", len(jobs)-failed, failed, len(docs)*3-len(jobs))
	if failed > 0 {
		return 1
	}
	return 0
}
