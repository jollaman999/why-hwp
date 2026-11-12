package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jollaman999/why-hwp/internal/apply"
	"github.com/jollaman999/why-hwp/internal/engine"
	"github.com/jollaman999/why-hwp/internal/hwp"
)

func init() {
	register("apply", "Apply edits as tracked changes", runApply)
}

func runApply(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("apply", flag.ContinueOnError)
	fset.SetOutput(stderr)
	specPath := fset.String("spec", "", "JSON edit spec (required)")
	out := fset.String("o", "", "output .hwpx path (required)")
	author := fset.String("author", "", "tracked-change author name (required unless -no-track)")
	date := fset.String("date", "", "tracked-change time, RFC 3339 (default: now)")
	noTrack := fset.Bool("no-track", false, "edit without recording tracked changes")
	force := fset.Bool("force", false, "overwrite an existing output file")
	fset.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp apply -spec spec.json -o out.hwpx [-author NAME] [-date RFC3339] [-no-track] [-force] <in.hwpx|in.hwp>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Applies the ops in the spec to a copy of the document. Locations are the")
		fmt.Fprintln(stderr, "ones 'why-hwp text' prints for the input; ops do not renumber each other.")
		fmt.Fprintln(stderr, "A cell with several paragraphs is addressed by paragraph (.../c2/p1), as")
		fmt.Fprintln(stderr, "text prints it; the bare cell loc names its first paragraph. Text-box")
		fmt.Fprintln(stderr, "text (a loc ending in #box) cannot be edited.")
		fmt.Fprintln(stderr, "Ops: set_text, replace, insert_after, insert_before, delete, image.")
		fmt.Fprintln(stderr, "Image paths in the spec are relative to the spec file.")
		fmt.Fprintln(stderr, "An .hwp input is converted to HWPX with the built-in engine first; the")
		fmt.Fprintln(stderr, "conversion can lose parts of the document, so check the output with")
		fmt.Fprintln(stderr, "'why-hwp verify -orig in.hwp out.hwpx'.")
		fmt.Fprintln(stderr)
		fset.PrintDefaults()
	}
	if err := fset.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fset.NArg() != 1 {
		fset.Usage()
		return 2
	}
	in := fset.Arg(0)
	fail := func(code int, format string, a ...any) int {
		fmt.Fprintf(stderr, "why-hwp apply: "+format+"\n", a...)
		return code
	}
	isHWP := false
	switch strings.ToLower(filepath.Ext(in)) {
	case ".hwpx":
	case ".hwp":
		isHWP = true
	default:
		return fail(2, "unsupported input %q (want .hwpx or .hwp)", filepath.Ext(in))
	}
	if *specPath == "" || *out == "" {
		return fail(2, "-spec and -o are required")
	}
	if strings.ToLower(filepath.Ext(*out)) != ".hwpx" {
		return fail(2, "output must be a .hwpx file: %s", *out)
	}
	if !*noTrack && strings.TrimSpace(*author) == "" {
		return fail(2, "-author is required to record tracked changes (or use -no-track)")
	}
	when := time.Now()
	if *date != "" {
		t, err := time.Parse(time.RFC3339, *date)
		if err != nil {
			return fail(2, "-date: %v", err)
		}
		when = t
	}
	if same, err := samePath(in, *out); err != nil {
		return fail(1, "%v", err)
	} else if same {
		return fail(2, "output %s is the input file; write to a new file", *out)
	}
	if _, err := os.Stat(*out); err == nil && !*force {
		return fail(2, "%s already exists (use -force to overwrite)", *out)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fail(1, "%v", err)
	}

	f, err := os.Open(*specPath)
	if err != nil {
		return fail(1, "%v", err)
	}
	spec, err := apply.ParseSpec(f)
	f.Close()
	if err != nil {
		return fail(2, "%s: %v", *specPath, err)
	}
	src := in
	if isHWP {
		if _, err := hwp.Read(in); err != nil {
			return fail(1, "%s: %v", in, err)
		}
		tmp, err := os.MkdirTemp("", "why-hwp-apply-")
		if err != nil {
			return fail(1, "%v", err)
		}
		defer os.RemoveAll(tmp)
		src = filepath.Join(tmp, "input.hwpx")
		if err := engine.Convert(context.Background(), in, src, "hwpx"); err != nil {
			return fail(1, "%s: convert to HWPX: %v", in, err)
		}
		fmt.Fprintf(stderr, "why-hwp apply: converted %s to HWPX with the built-in engine; the conversion can lose parts of the document, check the result with why-hwp verify -orig %s %s\n", in, in, *out)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fail(1, "%v", err)
	}
	res, err := apply.Apply(data, spec, apply.Options{
		Track:   !*noTrack,
		Author:  *author,
		Date:    when,
		BaseDir: filepath.Dir(*specPath),
	})
	if err != nil {
		return fail(1, "%s: %v", in, err)
	}
	if err := writeFileAtomic(*out, res.Data); err != nil {
		return fail(1, "%v", err)
	}
	mode := "untracked"
	if !*noTrack {
		mode = fmt.Sprintf("tracked as %q (author id %s)", *author, res.AuthorID)
	}
	fmt.Fprintf(stdout, "%s: %d ops, %d insertions, %d deletions, %d images, %s\n",
		*out, res.Ops, res.Inserted, res.Deleted, res.Images, mode)
	return 0
}

// samePath reports whether a and b name the same file. b may not exist.
func samePath(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return os.SameFile(sa, sb), nil
}

// writeFileAtomic writes data to a temporary file next to name and renames
// it into place, so a failed write never leaves a partial document.
func writeFileAtomic(name string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
