package main

import (
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
)

func init() {
	register("convert", "Convert to HWP, HWPX, PDF or TXT", runConvert)
}

func runConvert(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	to := fs.String("to", "", "output format: hwp, hwpx, pdf or txt (default: from the -o extension)")
	out := fs.String("o", "", "output path (default: input path with the -to extension)")
	eng := fs.String("engine", "builtin", "converter: builtin (every OS) or hancom (Hancom Office, Windows only)")
	timeout := fs.Duration("timeout", 120*time.Second, "time limit for one conversion, including the first engine compile and font downloads; with -engine hancom only the Hwp process started by this run is killed on timeout")
	force := fs.Bool("force", false, "overwrite an existing output file")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp convert [-to hwp|hwpx|pdf|txt] [-o out] [-engine builtin|hancom] [-timeout 120s] [-force] <in.hwp|in.hwpx|in.docx>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Converts with the built-in engine (rhwp), which reads .hwp and .hwpx on every OS.")
		fmt.Fprintln(stderr, "-engine hancom uses the installed Hancom Office instead (Windows only; also reads .docx).")
		fmt.Fprintln(stderr, "Text output is UTF-8.")
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
	if *timeout <= 0 {
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
	var res hancom.Result
	if *eng == "hancom" {
		res, err = hancom.Convert(context.Background(), hancom.Options{
			In:      in,
			Out:     dst,
			To:      f,
			Timeout: *timeout,
			Force:   *force,
		})
		if errors.Is(err, hancom.ErrUnsupported) {
			fmt.Fprintf(stderr, "why-hwp convert: -engine hancom: %v\n", err)
			return 1
		}
	} else {
		res, err = convertBuiltin(in, dst, f, *timeout, *force)
	}
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp convert: %s: %v\n", in, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %s, %d bytes, md5 %s\n", res.Out, res.Format, res.Size, res.MD5)
	return 0
}

// convertBuiltin converts in to dst with the built-in engine. The engine
// writes into a temporary folder; the result is then copied to dst, which
// must not exist unless force is set.
func convertBuiltin(in, dst string, f hancom.Format, timeout time.Duration, force bool) (res hancom.Result, err error) {
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
	conv := filepath.Join(tmp, "output."+string(f))

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := engine.Convert(ctx, in, conv, string(f)); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return res, fmt.Errorf("conversion did not finish within %s", timeout)
		}
		return res, err
	}
	st, err := os.Stat(conv)
	if err != nil {
		return res, fmt.Errorf("engine reported success but wrote no output: %w", err)
	}
	if st.Size() == 0 && f != hancom.TXT {
		return res, errors.New("engine wrote an empty output file")
	}
	size, sum, err := copyOut(conv, dst, force)
	if err != nil {
		return res, err
	}
	return hancom.Result{Out: dst, Format: f, Size: size, MD5: sum}, nil
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
