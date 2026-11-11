package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jollaman999/why-hwp/internal/load"
	"github.com/jollaman999/why-hwp/internal/model"
	"github.com/jollaman999/why-hwp/internal/verify"
)

func init() {
	register("verify", "Check a document for problems", runVerify)
}

func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "plain", "output format: plain or json")
	origName := fs.String("orig", "", "original document to compare against")
	allowName := fs.String("allow", "", "file of locations that may change, one per line (s0/p4/t0/* matches a prefix)")
	bannedName := fs.String("banned", "", "file of banned words, one per line (# comments, re: prefix for a regular expression)")
	ignoreCase := fs.Bool("ignore-case", false, "match banned words without regard to letter case")
	view := viewFlag(fs)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp verify [-orig file] [-allow locs.txt] [-banned words.txt] [-ignore-case] [-view accepted|original|markup] [-format plain|json] <file>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Files may be .hwpx, .hwp, .docx, .md or .txt; md and txt are read one")
		fmt.Fprintln(stderr, "block per line with locations l1, l2, ...")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "With -orig, lists blocks changed, added and removed against the original,")
		fmt.Fprintln(stderr, "fails when memos, tables, pictures or tracked changes were lost, and")
		fmt.Fprintln(stderr, "warns about numbers the original does not have. With -allow, a change")
		fmt.Fprintln(stderr, "outside the listed locations fails. Banned words and em dashes fail;")
		fmt.Fprintln(stderr, "en dashes, and em dashes in blocks equal to the original, only warn.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Both documents are read in -view (default accepted: every tracked change")
		fmt.Fprintln(stderr, "accepted, see why-hwp text -h). .hwp files have no views.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Exit status: 0 no violations (warnings allowed), 1 violations,")
		fmt.Fprintln(stderr, "2 usage error or a file that cannot be read.")
		fmt.Fprintln(stderr)
		fs.PrintDefaults()
	}
	name, code := parseFileArgs(fs, args, format, view)
	if name == "" {
		return code
	}

	cur, code := loadVerifyInput(name, model.View(*view), stderr)
	if cur == nil {
		return code
	}
	var orig *model.Document
	if *origName != "" {
		if orig, code = loadVerifyInput(*origName, model.View(*view), stderr); orig == nil {
			return code
		}
	}
	var opt verify.Options
	if *allowName != "" {
		err := withFile(*allowName, func(r io.Reader) (err error) {
			opt.Allow, err = verify.ParseAllow(r)
			return err
		})
		if err != nil {
			fmt.Fprintf(stderr, "why-hwp verify: -allow: %v\n", err)
			return 2
		}
	}
	if *bannedName != "" {
		err := withFile(*bannedName, func(r io.Reader) (err error) {
			opt.Banned, err = verify.ParseBanned(r, *ignoreCase)
			return err
		})
		if err != nil {
			fmt.Fprintf(stderr, "why-hwp verify: -banned: %v\n", err)
			return 2
		}
	}

	rep := verify.Check(orig, cur, opt)
	if *format == "json" {
		if code := writeJSON(stdout, stderr, rep); code != 0 {
			return 2
		}
	} else if err := rep.WritePlain(stdout); err != nil {
		fmt.Fprintf(stderr, "why-hwp verify: %v\n", err)
		return 2
	}
	if rep.Violations > 0 {
		return 1
	}
	return 0
}

// loadVerifyInput reads any format verify accepts. Unreadable input exits
// with 2 so that exit status 1 always means violations.
func loadVerifyInput(name string, view model.View, stderr io.Writer) (*model.Document, int) {
	doc, err := load.Any(name)
	var unsupported *load.UnsupportedError
	if errors.As(err, &unsupported) {
		fmt.Fprintf(stderr, "why-hwp verify: unsupported file type %q (want .hwpx, .hwp, .docx, .md or .txt)\n", unsupported.Ext)
		return nil, 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp verify: %v\n", err)
		return nil, 2
	}
	return doc.InView(view), 0
}

func withFile(name string, fn func(io.Reader) error) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return fn(f)
}
