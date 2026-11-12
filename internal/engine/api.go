package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Warn receives warnings that do not stop an operation, such as fonts left
// out in offline mode. Replace it before the first call to this package.
var Warn = func(msg string) {
	fmt.Fprintln(os.Stderr, "why-hwp: warning: "+msg)
}

// fontMu orders writers of the font cache (font-install) against readers
// (convert) within this process.
var fontMu sync.RWMutex

// Formats that Convert can write.
var convertFormats = map[string]bool{"hwp": true, "hwpx": true, "pdf": true, "txt": true}

// Convert converts the document at in to format to (hwp, hwpx, pdf or txt)
// and writes it to out, replacing any existing file. Only pdf output draws
// glyphs, so only then are fonts used: those the document needs are
// downloaded into the font cache first unless WHY_HWP_OFFLINE=1, in which
// case the missing fonts are reported through Warn. Fonts installed on this
// computer (see EnvFontDirs and EnvSystemFonts) are used as they are and not
// downloaded.
//
// When ctx ends first, Convert returns ctx's error at once and leaves out
// alone; the engine run already started goes on to its end in the
// background (see the package comment).
func Convert(ctx context.Context, in, out, to string) error {
	if !convertFormats[to] {
		return fmt.Errorf("engine: unsupported output format %q", to)
	}
	e, err := loadRunner(ctx)
	if err != nil {
		return err
	}
	work, err := newWorkDir("in", "out", "dl")
	if err != nil {
		return err
	}
	outName := "output." + to
	_, err = detach(ctx, "convert", func() (missingFonts, error) {
		return convertIn(ctx, e, work, in, outName, to)
	}, func(miss missingFonts) error {
		if len(miss) > 0 {
			Warn(fmt.Sprintf("%s=1: at least %d font(s) not downloaded, output may use substitutes: %s",
				EnvOffline, len(miss), strings.Join(miss, ", ")))
		}
		return copyFile(filepath.Join(work, "out", outName), out)
	}, func() { os.RemoveAll(work) })
	return err
}

// convertIn does the work of Convert inside the work directory work: it
// copies in there, installs the fonts a pdf needs and has the engine write
// outName in work/out. It returns the fonts left out in offline mode.
//
// The font cache is planned against under the read lock, so that a run
// left behind by a caller whose ctx ended does not hold up a document
// whose fonts are all in the cache; the write lock is taken only when
// fonts have to be installed.
func convertIn(ctx context.Context, e runner, work, in, outName, to string) (missingFonts, error) {
	inName := "input" + strings.ToLower(filepath.Ext(in))
	if err := copyFile(in, filepath.Join(work, "in", inName)); err != nil {
		return nil, err
	}
	guestIn := "/in/" + inName
	mounts := []mount{
		{host: filepath.Join(work, "in"), guest: "/in", readOnly: true},
		{host: filepath.Join(work, "out"), guest: "/out"},
	}

	var miss missingFonts
	if to == "pdf" {
		fontsDir, err := fontCacheDir()
		if err != nil {
			return nil, err
		}
		hostFonts := hostFontMounts(hostFontDirs())
		inDir, dlDir := filepath.Join(work, "in"), filepath.Join(work, "dl")
		fontMu.RLock()
		miss, err = ensureFonts(ctx, e, httpClient, inDir, guestIn, dlDir, fontsDir, hostFonts, true)
		fontMu.RUnlock()
		if err == nil && len(miss) > 0 && os.Getenv(EnvOffline) != "1" {
			fontMu.Lock()
			miss, err = ensureFonts(ctx, e, httpClient, inDir, guestIn, dlDir, fontsDir, hostFonts, false)
			fontMu.Unlock()
		}
		if err != nil {
			return nil, err
		}
		mounts = append(append(mounts, mount{host: fontsDir, guest: "/fonts", readOnly: true}), hostFonts...)
	}

	fontMu.RLock()
	defer fontMu.RUnlock()
	_, err := e.run(ctx, []string{"convert", "--to", to, "--in", guestIn, "--out", "/out/" + outName}, mounts)
	return miss, err
}

// loadRunner returns the engine Convert runs. Tests replace it.
var loadRunner = func(ctx context.Context) (runner, error) {
	e, err := get(ctx)
	if err != nil {
		return nil, err
	}
	return e, nil
}

// SVG2PNG renders svg on a white background using only the given font
// files. The image is stretched to exactly w x h pixels; keeping the aspect
// ratio is up to the caller. A done ctx returns at once, as in Convert.
func SVG2PNG(ctx context.Context, svg []byte, fonts [][]byte, w, h int) ([]byte, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("engine: bad size %dx%d", w, h)
	}
	e, err := get(ctx)
	if err != nil {
		return nil, err
	}
	work, err := newWorkDir("in", "out")
	if err != nil {
		return nil, err
	}
	return detach(ctx, "svg2png", func() ([]byte, error) {
		return svg2pngIn(ctx, e, work, svg, fonts, w, h)
	}, nil, func() { os.RemoveAll(work) })
}

// svg2pngIn does the work of SVG2PNG inside the work directory work.
func svg2pngIn(ctx context.Context, e runner, work string, svg []byte, fonts [][]byte, w, h int) ([]byte, error) {
	if err := os.WriteFile(filepath.Join(work, "in", "x.svg"), svg, 0o644); err != nil {
		return nil, err
	}
	args := []string{"svg2png", "--in", "/in/x.svg", "--out", "/out/x.png",
		"--width", strconv.Itoa(w), "--height", strconv.Itoa(h)}
	for i, f := range fonts {
		name := fmt.Sprintf("font-%d", i)
		if err := os.WriteFile(filepath.Join(work, "in", name), f, 0o644); err != nil {
			return nil, err
		}
		args = append(args, "--font", "/in/"+name)
	}
	if _, err := e.run(ctx, args, []mount{
		{host: filepath.Join(work, "in"), guest: "/in", readOnly: true},
		{host: filepath.Join(work, "out"), guest: "/out"},
	}); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(work, "out", "x.png"))
}

// Version returns the engine's version line. A done ctx returns at once, as
// in Convert.
func Version(ctx context.Context) (string, error) {
	e, err := get(ctx)
	if err != nil {
		return "", err
	}
	out, err := detach(ctx, "version", func() ([]byte, error) {
		return e.run(ctx, []string{"version"}, nil)
	}, nil, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// detach runs job in a goroutine of its own and waits for it or for ctx,
// whichever ends first.
//
// When job ends first, detach calls keep (if not nil) with job's result on
// the caller's goroutine, then cleanup (if not nil), and returns. keep is
// where results leave the work directory, so nothing reaches the caller's
// files after detach has returned.
//
// When ctx ends first, detach calls cleanup and returns "engine <what>:
// <ctx error>" at once without calling keep. job goes on to its end; engine
// runs cannot be stopped from outside without slowing every run down (see
// newEngine). The work directory is thus removed under the running job,
// which then fails or writes nothing, and its result is dropped. Once job
// ends, its goroutine calls cleanup again for what could not be removed
// while in use (open files on Windows). cleanup must allow being called
// twice, at the same time too.
func detach[T any](ctx context.Context, what string, job func() (T, error), keep func(T) error, cleanup func()) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		if cleanup != nil {
			cleanup()
		}
		return zero, fmt.Errorf("engine %s: %w", what, err)
	}
	type result struct {
		v   T
		err error
	}
	done := make(chan result)
	abandoned := make(chan struct{})
	detached.Add(1)
	go func() {
		defer detached.Done()
		v, err := job()
		select {
		case done <- result{v, err}:
		case <-abandoned:
			if cleanup != nil {
				cleanup()
			}
		}
	}()
	select {
	case r := <-done:
		if cleanup != nil {
			defer cleanup()
		}
		if r.err != nil {
			return zero, r.err
		}
		if keep != nil {
			if err := keep(r.v); err != nil {
				return zero, err
			}
		}
		return r.v, nil
	case <-ctx.Done():
		close(abandoned)
		if cleanup != nil {
			cleanup()
		}
		return zero, fmt.Errorf("engine %s: %w", what, ctx.Err())
	}
}

// detached counts the jobs of detach still running; tests wait on it.
var detached sync.WaitGroup

// workDirPrefix starts the names of the work directories in the temporary
// folder.
const workDirPrefix = "why-hwp-engine-"

// staleWorkAge is how long a work directory has gone unmodified before
// newWorkDir takes it for one left behind and deletes it.
const staleWorkAge = time.Hour

// newWorkDir creates a fresh temporary directory with the given
// subdirectories. The caller removes it. Work directories of earlier runs
// left in the temporary folder are deleted first (see removeStaleWork).
func newWorkDir(subs ...string) (string, error) {
	removeStaleWork(os.TempDir(), time.Now())
	dir, err := os.MkdirTemp("", workDirPrefix)
	if err != nil {
		return "", err
	}
	for _, s := range subs {
		if err := os.Mkdir(filepath.Join(dir, s), 0o755); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

// copyFile copies src to dst, creating or truncating dst.
func copyFile(src, dst string) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// removeStaleWork deletes the directories named workDirPrefix* in tmp that
// were last modified more than staleWorkAge before now. A run timed out on
// Windows can leave its work directory behind when the process exits, since
// files the engine still has open cannot be deleted. Symbolic links and
// other files are left alone. Errors are ignored.
func removeStaleWork(tmp string, now time.Time) {
	ents, _ := os.ReadDir(tmp)
	for _, ent := range ents {
		if !ent.IsDir() || !strings.HasPrefix(ent.Name(), workDirPrefix) {
			continue
		}
		info, err := ent.Info()
		if err != nil || now.Sub(info.ModTime()) <= staleWorkAge {
			continue
		}
		os.RemoveAll(filepath.Join(tmp, ent.Name()))
	}
}
