package main

import (
	"archive/zip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jollaman999/why-hwp/internal/engine"
	"github.com/jollaman999/why-hwp/internal/hancom"
	"github.com/jollaman999/why-hwp/internal/hwp"
	"github.com/jollaman999/why-hwp/internal/hwpx"
	"github.com/jollaman999/why-hwp/internal/model"
)

func init() {
	register("convert", "Convert to HWP, HWPX, PDF or TXT", runConvert)
}

func runConvert(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	to := fs.String("to", "", "output format: hwp, hwpx, pdf or txt (default: from the -o extension)")
	out := fs.String("o", "", "output path (default: input path with the -to extension)")
	eng := fs.String("engine", "builtin", "converter: builtin (every OS) or hancom (Hancom Office, Windows only)")
	timeout := fs.Duration("timeout", 0, "time limit for one conversion, including the first engine compile and font downloads (default 10m with -engine builtin, 120s with -engine hancom); with -engine hancom only the Hwp process started by this run is killed on timeout")
	force := fs.Bool("force", false, "overwrite an existing output file")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp convert [-to hwp|hwpx|pdf|txt] [-o out] [-engine builtin|hancom] [-timeout 10m] [-force] <in.hwp|in.hwpx|in.docx>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Converts with the built-in engine (rhwp), which reads .hwp and .hwpx on every OS.")
		fmt.Fprintln(stderr, "-engine hancom uses the installed Hancom Office instead (Windows only; also reads .docx).")
		fmt.Fprintln(stderr, "Text output is UTF-8. An HWP or HWPX output with fewer tracked changes")
		fmt.Fprintln(stderr, "than the input is not written, and convert exits with status 1.")
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
	if *eng != "builtin" && *eng != "hancom" {
		fmt.Fprintf(stderr, "why-hwp convert: unknown -engine %q (want builtin or hancom)\n", *eng)
		return 2
	}
	timeoutSet := false
	fs.Visit(func(f *flag.Flag) { timeoutSet = timeoutSet || f.Name == "timeout" })
	if !timeoutSet {
		*timeout = defaultTimeout(*eng)
	} else if *timeout <= 0 {
		fmt.Fprintln(stderr, "why-hwp convert: -timeout must be positive")
		return 2
	}
	in := fs.Arg(0)
	dst, f, err := hancom.Plan(in, *out, *to)
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp convert: %v\n", err)
		return 2
	}
	if *eng == "builtin" && strings.EqualFold(filepath.Ext(in), ".docx") {
		hint := "-engine hancom reads .docx on Windows with Hancom Office"
		if runtime.GOOS == "windows" {
			hint = "use -engine hancom for .docx"
		}
		fmt.Fprintf(stderr, "why-hwp convert: %s: the built-in engine reads .hwp and .hwpx only; %s\n", in, hint)
		return 2
	}
	if _, err := os.Stat(in); err != nil {
		fmt.Fprintf(stderr, "why-hwp convert: %v\n", err)
		return 1
	}
	var check func(out string) error
	if (f == hancom.HWP || f == hancom.HWPX) && !strings.EqualFold(filepath.Ext(in), ".docx") {
		want, err := trackChangeCount(in, *timeout)
		switch {
		case err == nil:
			check = func(out string) error { return checkTrackChanges(want, out, *timeout) }
		case *eng == "hancom":
			fmt.Fprintf(stderr, "why-hwp convert: warning: %s: cannot count tracked changes, the output is not checked for lost ones: %v\n", in, err)
		default:
			fmt.Fprintf(stderr, "why-hwp convert: %s: count tracked changes: %v\n", in, err)
			return 1
		}
	}
	var res hancom.Result
	if *eng == "hancom" {
		res, err = convertChecked(dst, f, *force, check, func(out string) (hancom.Result, error) {
			return hancom.Convert(context.Background(), hancom.Options{
				In:      in,
				Out:     out,
				To:      f,
				Timeout: *timeout,
			})
		})
		if errors.Is(err, hancom.ErrUnsupported) {
			fmt.Fprintf(stderr, "why-hwp convert: -engine hancom: %v\n", err)
			return 1
		}
	} else {
		res, err = convertBuiltin(in, dst, f, *timeout, *force, check)
	}
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp convert: %s: %v\n", in, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %s, %d bytes, md5 %s\n", res.Out, res.Format, res.Size, res.MD5)
	return 0
}

// defaultTimeout is the -timeout used when none is given. The built-in
// engine can take minutes on a long document, while Hancom Office that has
// not finished in two minutes is usually stuck on a dialog.
func defaultTimeout(engine string) time.Duration {
	if engine == "hancom" {
		return 120 * time.Second
	}
	return 10 * time.Minute
}

// convertBuiltin converts in to dst with the built-in engine, as
// convertChecked does.
func convertBuiltin(in, dst string, f hancom.Format, timeout time.Duration, force bool, check func(string) error) (hancom.Result, error) {
	return convertChecked(dst, f, force, check, func(out string) (hancom.Result, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := engine.Convert(ctx, in, out, string(f)); err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return hancom.Result{}, fmt.Errorf("conversion did not finish within %s", timeout)
			}
			return hancom.Result{}, err
		}
		st, err := os.Stat(out)
		if err != nil {
			return hancom.Result{}, fmt.Errorf("engine reported success but wrote no output: %w", err)
		}
		if st.Size() == 0 && f != hancom.TXT {
			return hancom.Result{}, errors.New("engine wrote an empty output file")
		}
		return hancom.Result{Format: f}, nil
	})
}

// convertChecked has conv write the converted document to a file in a
// temporary folder and copies that file to dst, which must not exist unless
// force is set. A non-nil check runs on the converted file first; when it
// fails, nothing is written to dst and an existing dst keeps its content.
func convertChecked(dst string, f hancom.Format, force bool, check func(string) error, conv func(out string) (hancom.Result, error)) (res hancom.Result, err error) {
	if !force {
		if _, err := os.Lstat(dst); err == nil {
			return res, fmt.Errorf("%s: already exists (use -force to overwrite)", dst)
		}
	}
	tmp, err := os.MkdirTemp("", "why-hwp-convert-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "output."+string(f))
	if res, err = conv(out); err != nil {
		return res, err
	}
	if check != nil {
		if err := check(out); err != nil {
			return hancom.Result{}, err
		}
	}
	size, sum, err := copyOut(out, dst, force)
	if err != nil {
		return hancom.Result{}, err
	}
	res.Out, res.Format, res.Size, res.MD5 = dst, f, size, sum
	return res, nil
}

// copyOut copies src to dst and returns the size and MD5 of what it wrote.
// Without force it fails when dst exists.
func copyOut(src, dst string, force bool) (int64, string, error) {
	r, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer r.Close()
	flag := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	w, err := os.OpenFile(dst, flag, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return 0, "", fmt.Errorf("%s: already exists (use -force to overwrite)", dst)
		}
		return 0, "", err
	}
	h := md5.New()
	n, err := io.Copy(io.MultiWriter(w, h), r)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// trackChangeCount returns the number of tracked changes in the HWP or HWPX
// file name, counted as 'why-hwp info' counts them. The file is read as what
// it holds, whatever its extension. A file the readers of why-hwp cannot
// open, such as an HWP 3.0 file or a distribution document, is counted by
// the built-in engine within timeout.
func trackChangeCount(name string, timeout time.Duration) (int, error) {
	n, err := readTrackChanges(name)
	if err == nil {
		return n, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	n, eerr := engineTrackChanges(ctx, name)
	if eerr != nil {
		return 0, fmt.Errorf("%v; built-in engine: %v", err, eerr)
	}
	return n, nil
}

// engineTrackChanges counts tracked changes with the built-in engine. Tests
// replace it.
var engineTrackChanges = engine.TrackChanges

// readTrackChanges counts the tracked changes in name with the HWPX reader
// when it is a zip package and with the HWP reader otherwise.
func readTrackChanges(name string) (int, error) {
	f, err := os.Open(name)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	head := make([]byte, 4)
	n, _ := f.ReadAt(head, 0)
	var doc *model.Document
	if string(head[:n]) == "PK\x03\x04" {
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return 0, err
		}
		doc, err = hwpx.Parse(zr)
		if err != nil {
			return 0, err
		}
	} else if doc, err = hwp.Parse(f); err != nil {
		return 0, err
	}
	return doc.Summary.TrackChanges, nil
}

// checkTrackChanges counts the tracked changes in out and fails when there
// are fewer than want, the count of the input.
func checkTrackChanges(want int, out string, timeout time.Duration) error {
	got, err := trackChangeCount(out, timeout)
	if err != nil {
		return fmt.Errorf("count tracked changes in the output: %w", err)
	}
	return trackChangeLoss(want, got)
}

// trackChangeLoss fails when the output has fewer tracked changes than the
// input. There is no option to keep such an output: tracked changes must
// not be dropped silently.
func trackChangeLoss(in, out int) error {
	if out < in {
		return fmt.Errorf("tracked changes lost: input %d, output %d; the output is not kept", in, out)
	}
	return nil
}
