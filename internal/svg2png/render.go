package svg2png

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/draw"
	"image/png"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Options describes one render.
type Options struct {
	Browser Browser
	SVG     []byte  // SVG document
	Width   int     // output width in device pixels
	Height  int     // output height in device pixels
	DPI     float64 // written to the PNG pHYs chunk
	Trim    bool    // cut white or transparent margins after rendering
	Timeout time.Duration
	// TempDir is the parent of the per-render work directory; empty means
	// os.TempDir().
	TempDir string
}

// Result is a rendered image.
type Result struct {
	PNG    []byte
	Width  int
	Height int
	// Raw is the size of the browser screenshot before cropping.
	RawWidth, RawHeight int
}

// Render draws the SVG with the browser and returns the PNG. The browser runs
// with a throwaway profile directory, and the work directory is removed
// before Render returns.
func Render(ctx context.Context, o Options) (res Result, err error) {
	if o.Width < 1 || o.Height < 1 {
		return Result{}, errors.New("output size must be positive")
	}
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}
	dir, err := os.MkdirTemp(o.TempDir, "why-hwp-svg2png-")
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if rerr := os.RemoveAll(dir); rerr != nil && err == nil {
			err = fmt.Errorf("remove work directory: %w", rerr)
		}
	}()

	if err := os.WriteFile(filepath.Join(dir, "input.svg"), o.SVG, 0o600); err != nil {
		return Result{}, err
	}
	// One CSS pixel is one device pixel, so the page, the window and the
	// screenshot all have the exact integer target size.
	page := filepath.Join(dir, "page.html")
	if err := os.WriteFile(page, []byte(pageHTML(o.Width, o.Height)), 0o600); err != nil {
		return Result{}, err
	}
	if o.Browser.Kind == Firefox {
		// Firefox needs an existing profile directory. Pin the pixel ratio so
		// the OS display scaling does not enlarge the screenshot.
		prof := filepath.Join(dir, "profile")
		if err := os.Mkdir(prof, 0o700); err != nil {
			return Result{}, err
		}
		prefs := "user_pref(\"layout.css.devPixelsPerPx\", \"1.0\");\n"
		if err := os.WriteFile(filepath.Join(prof, "user.js"), []byte(prefs), 0o600); err != nil {
			return Result{}, err
		}
	}
	shot := filepath.Join(dir, "shot.png")
	args := browserArgs(o.Browser.Kind, dir, page, shot, o.Width, o.Height)

	cctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, o.Browser.Path, args...)
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return Result{}, fmt.Errorf("%s did not finish within %s", o.Browser.Name, o.Timeout)
	}
	raw, rerr := os.ReadFile(shot)
	if rerr != nil {
		msg := tail(out.String(), 2000)
		if runErr != nil {
			return Result{}, fmt.Errorf("%s failed: %v\n%s", o.Browser.Name, runErr, msg)
		}
		return Result{}, fmt.Errorf("%s wrote no screenshot\n%s", o.Browser.Name, msg)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return Result{}, fmt.Errorf("decode screenshot: %w", err)
	}
	b := img.Bounds()
	res.RawWidth, res.RawHeight = b.Dx(), b.Dy()
	if b.Dx() < o.Width-1 || b.Dy() < o.Height-1 {
		return Result{}, fmt.Errorf("screenshot is %dx%d, want %dx%d", b.Dx(), b.Dy(), o.Width, o.Height)
	}
	crop := image.Rect(b.Min.X, b.Min.Y, b.Min.X+min(o.Width, b.Dx()), b.Min.Y+min(o.Height, b.Dy()))
	if o.Trim {
		crop = trimRect(img, crop)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	draw.Draw(dst, dst.Bounds(), img, crop.Min, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return Result{}, err
	}
	data := buf.Bytes()
	if o.DPI > 0 {
		data = withPHYs(data, o.DPI)
	}
	res.PNG = data
	res.Width, res.Height = crop.Dx(), crop.Dy()
	return res, nil
}

func pageHTML(w, h int) string {
	return `<!DOCTYPE html>
<html><head><meta charset="utf-8"><style>
html,body{margin:0;padding:0;overflow:hidden;background:#fff}
img{display:block;width:` + strconv.Itoa(w) + `px;height:` + strconv.Itoa(h) + `px}
</style></head><body><img src="input.svg" alt=""></body></html>
`
}

func browserArgs(k Kind, dir, page, shot string, w, h int) []string {
	pageURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(page)}).String()
	if filepath.VolumeName(page) != "" {
		pageURL = (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(page)}).String()
	}
	size := strconv.Itoa(w) + "," + strconv.Itoa(h)
	if k == Firefox {
		return []string{
			"--headless",
			"--no-remote",
			"--profile", filepath.Join(dir, "profile"),
			"--window-size=" + size,
			"--screenshot", shot,
			pageURL,
		}
	}
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--hide-scrollbars",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-sync",
		"--mute-audio",
		"--user-data-dir=" + filepath.Join(dir, "profile"),
		"--force-device-scale-factor=1",
		"--window-size=" + size,
		"--screenshot=" + shot,
	}
	if os.Geteuid() == 0 {
		args = append(args, "--no-sandbox")
	}
	return append(args, pageURL)
}

// trimRect shrinks r to exclude rows and columns at the edges that are all
// white or fully transparent.
func trimRect(img image.Image, r image.Rectangle) image.Rectangle {
	blank := func(x, y int) bool {
		cr, cg, cb, ca := img.At(x, y).RGBA()
		if ca == 0 {
			return true
		}
		const t = 0xfafa
		return cr >= t && cg >= t && cb >= t
	}
	rowBlank := func(y int) bool {
		for x := r.Min.X; x < r.Max.X; x++ {
			if !blank(x, y) {
				return false
			}
		}
		return true
	}
	colBlank := func(x int) bool {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			if !blank(x, y) {
				return false
			}
		}
		return true
	}
	for r.Min.Y < r.Max.Y && rowBlank(r.Min.Y) {
		r.Min.Y++
	}
	for r.Max.Y > r.Min.Y && rowBlank(r.Max.Y-1) {
		r.Max.Y--
	}
	for r.Min.X < r.Max.X && colBlank(r.Min.X) {
		r.Min.X++
	}
	for r.Max.X > r.Min.X && colBlank(r.Max.X-1) {
		r.Max.X--
	}
	if r.Empty() {
		return image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Min.Y+1)
	}
	return r
}

// withPHYs inserts a pHYs chunk after IHDR so the PNG carries its DPI.
func withPHYs(p []byte, dpi float64) []byte {
	const ihdrEnd = 8 + 4 + 4 + 13 + 4
	if len(p) < ihdrEnd {
		return p
	}
	ppm := uint32(math.Round(dpi / 0.0254))
	chunk := make([]byte, 4+4+9+4)
	binary.BigEndian.PutUint32(chunk[0:], 9)
	copy(chunk[4:], "pHYs")
	binary.BigEndian.PutUint32(chunk[8:], ppm)
	binary.BigEndian.PutUint32(chunk[12:], ppm)
	chunk[16] = 1 // unit: metre
	binary.BigEndian.PutUint32(chunk[17:], crc32.ChecksumIEEE(chunk[4:17]))
	out := make([]byte, 0, len(p)+len(chunk))
	out = append(out, p[:ihdrEnd]...)
	out = append(out, chunk...)
	return append(out, p[ihdrEnd:]...)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
