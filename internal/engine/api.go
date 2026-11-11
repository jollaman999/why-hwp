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
// and writes it to out, replacing any existing file. Fonts the document
// needs are downloaded into the font cache first unless WHY_HWP_OFFLINE=1,
// in which case the missing fonts are reported through Warn.
func Convert(ctx context.Context, in, out, to string) error {
	if !convertFormats[to] {
		return fmt.Errorf("engine: unsupported output format %q", to)
	}
	e, err := get()
	if err != nil {
		return err
	}
	fontsDir, err := fontCacheDir()
	if err != nil {
		return err
	}
	work, err := newWorkDir("in", "out", "dl")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	inName := "input" + strings.ToLower(filepath.Ext(in))
	if err := copyFile(in, filepath.Join(work, "in", inName)); err != nil {
		return err
	}
	guestIn := "/in/" + inName

	fontMu.Lock()
	miss, err := ensureFonts(ctx, e, httpClient, filepath.Join(work, "in"), guestIn,
		filepath.Join(work, "dl"), fontsDir, os.Getenv(EnvOffline) == "1")
	fontMu.Unlock()
	if err != nil {
		return err
	}
	if len(miss) > 0 {
		Warn(fmt.Sprintf("%s=1: at least %d font(s) not downloaded, output may use substitutes: %s",
			EnvOffline, len(miss), strings.Join(miss, ", ")))
	}

	outName := "output." + to
	fontMu.RLock()
	_, err = e.run(ctx, []string{"convert", "--to", to, "--in", guestIn, "--out", "/out/" + outName}, []mount{
		{host: filepath.Join(work, "in"), guest: "/in", readOnly: true},
		{host: filepath.Join(work, "out"), guest: "/out"},
		{host: fontsDir, guest: "/fonts", readOnly: true},
	})
	fontMu.RUnlock()
	if err != nil {
		return err
	}
	return copyFile(filepath.Join(work, "out", outName), out)
}

// SVG2PNG renders svg on a white background using only the given font
// files. The image is stretched to exactly w x h pixels; keeping the aspect
// ratio is up to the caller.
func SVG2PNG(ctx context.Context, svg []byte, fonts [][]byte, w, h int) ([]byte, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("engine: bad size %dx%d", w, h)
	}
	e, err := get()
	if err != nil {
		return nil, err
	}
	work, err := newWorkDir("in", "out")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

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

// Version returns the engine's version line.
func Version(ctx context.Context) (string, error) {
	e, err := get()
	if err != nil {
		return "", err
	}
	out, err := e.run(ctx, []string{"version"}, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// newWorkDir creates a fresh temporary directory with the given
// subdirectories. The caller removes it.
func newWorkDir(subs ...string) (string, error) {
	dir, err := os.MkdirTemp("", "why-hwp-engine-")
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
