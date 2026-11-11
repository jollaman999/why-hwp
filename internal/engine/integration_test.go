package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image/color"
	"image/png"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jollaman999/why-hwp/internal/load"
)

// TestMain points the compilation and font caches at a temporary directory
// so the tests neither read nor fill the user's caches.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "why-hwp-engine-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	userCacheDir = func() (string, error) { return dir, nil }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func needEngine(t *testing.T) {
	t.Helper()
	if len(engineGz) == 0 {
		t.Skip("engine.wasm.gz is empty")
	}
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func TestEmbeddedVersion(t *testing.T) {
	needEngine(t)
	start := time.Now()
	v, err := Version(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first Version call, compile into an empty cache included: %v", time.Since(start))
	t.Logf("version: %s", v)
	if !strings.HasPrefix(v, "why-hwp-engine ") || !strings.Contains(v, " rhwp ") || !strings.Contains(v, " resvg ") {
		t.Errorf("version = %q", v)
	}
}

func TestCompileCacheHit(t *testing.T) {
	needEngine(t)
	if _, err := get(); err != nil {
		t.Fatal(err)
	}
	dir := compileCacheDir()
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) == 0 {
		t.Fatalf("compilation cache %s is empty: %v", dir, err)
	}
	wasm, err := unpack(engineGz, engineSum)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	e, err := newEngine(context.Background(), wasm, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("compile with a warm cache: %v", time.Since(start))
	e.rt.Close(context.Background())
}

// docText is the non-blank block texts of a document read by why-hwp's own
// readers, one per line.
func docText(t *testing.T, name string) string {
	t.Helper()
	d, err := load.Document(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var lines []string
	for _, b := range d.Blocks {
		if s := strings.TrimSpace(b.Text); s != "" {
			lines = append(lines, s)
		}
	}
	return strings.Join(lines, "\n")
}

// offline makes Convert skip downloads and collects its warnings.
func offline(t *testing.T) *[]string {
	t.Setenv(EnvOffline, "1")
	var got []string
	old := Warn
	Warn = func(msg string) { got = append(got, msg) }
	t.Cleanup(func() { Warn = old })
	return &got
}

func TestConvertOffline(t *testing.T) {
	needEngine(t)
	warns := offline(t)
	ctx := testCtx(t)
	src := filepath.Join("..", "..", "testdata", "sample.hwp")
	srcx := filepath.Join("..", "..", "testdata", "sample.hwpx")
	want := docText(t, src)
	if want == "" {
		t.Fatal("sample.hwp has no text")
	}
	dir := t.TempDir()

	hwpx := filepath.Join(dir, "a.hwpx")
	if err := Convert(ctx, src, hwpx, "hwpx"); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(hwpx)
	if err != nil {
		t.Fatalf("hwpx output is not a zip: %v", err)
	}
	zr.Close()
	if got := docText(t, hwpx); got != want {
		t.Errorf("hwp -> hwpx text differs\n got: %q\nwant: %q", got, want)
	}

	txt := filepath.Join(dir, "a.txt")
	if err := Convert(ctx, src, txt, "txt"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(txt)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(want, "\n", 2)[0]
	if !strings.Contains(string(b), first) {
		t.Errorf("txt output lacks %q:\n%s", first, b)
	}

	hwp := filepath.Join(dir, "b.hwp")
	if err := os.WriteFile(hwp, []byte("old content to be replaced"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Convert(ctx, srcx, hwp, "hwp"); err != nil {
		t.Fatal(err)
	}
	hb, err := os.ReadFile(hwp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(hb, []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1")) {
		t.Errorf("hwp output does not start with the CFB signature: % x", hb[:min(8, len(hb))])
	}
	if got, wantx := docText(t, hwp), docText(t, srcx); got != wantx {
		t.Errorf("hwpx -> hwp text differs\n got: %q\nwant: %q", got, wantx)
	}
	t.Logf("offline warnings: %q", *warns)

	if err := Convert(ctx, src, filepath.Join(dir, "x.doc"), "doc"); err == nil {
		t.Error("format doc was accepted")
	}
	if err := Convert(ctx, filepath.Join(dir, "missing.hwp"), filepath.Join(dir, "y.pdf"), "pdf"); err == nil {
		t.Error("missing input was accepted")
	}
}

// planURLs runs font-plan on in against the current font cache.
func planURLs(t *testing.T, ctx context.Context, in string) []string {
	t.Helper()
	e, err := get()
	if err != nil {
		t.Fatal(err)
	}
	fonts, err := fontCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err := copyFile(in, filepath.Join(work, "input"+filepath.Ext(in))); err != nil {
		t.Fatal(err)
	}
	out, err := e.run(ctx, []string{"font-plan", "--in", "/in/input" + filepath.Ext(in)}, []mount{
		{host: work, guest: "/in", readOnly: true},
		{host: fonts, guest: "/fonts", readOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	reqs, err := parseFontPlan(out)
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, r := range reqs {
		urls = append(urls, r.URL)
	}
	return urls
}

func TestConvertPDFDownloadsFonts(t *testing.T) {
	needEngine(t)
	if testing.Short() {
		t.Skip("downloads fonts")
	}
	ctx := testCtx(t)
	src := filepath.Join("..", "..", "testdata", "sample.hwp")
	urls := planURLs(t, ctx, src)
	if len(urls) == 0 {
		t.Fatal("font-plan on an empty font cache asked for no fonts")
	}
	u, _ := url.Parse(urls[0])
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(u.Hostname(), port), 5*time.Second)
	if err != nil {
		t.Skipf("no network: %v", err)
	}
	conn.Close()
	t.Logf("font-plan before Convert: %d font(s): %v", len(urls), urls)

	var warns []string
	old := Warn
	Warn = func(msg string) { warns = append(warns, msg) }
	t.Cleanup(func() { Warn = old })
	t.Setenv(EnvOffline, "")

	pdf := filepath.Join(t.TempDir(), "a.pdf")
	start := time.Now()
	if err := Convert(ctx, src, pdf, "pdf"); err != nil {
		t.Fatal(err)
	}
	t.Logf("Convert to pdf with downloads: %v", time.Since(start))
	b, err := os.ReadFile(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Errorf("pdf output starts with %q", b[:min(8, len(b))])
	}
	if len(warns) != 0 {
		t.Errorf("warnings with downloads on: %q", warns)
	}
	fonts, _ := fontCacheDir()
	filepath.WalkDir(fonts, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(fonts, p)
			t.Logf("font cache: %s", filepath.ToSlash(rel))
		}
		return nil
	})
	if left := planURLs(t, ctx, src); len(left) != 0 {
		t.Errorf("font-plan after Convert still asks for %v", left)
	}
	if dst := os.Getenv("WHY_HWP_TEST_KEEP_PDF"); dst != "" {
		if err := copyFile(pdf, dst); err != nil {
			t.Fatal(err)
		}
	}
}

const svgTest = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 10 10">
<rect x="0" y="0" width="5" height="10" fill="#ff0000"/>
<text x="6" y="9" font-family="Sans Serif Collection" font-size="9" fill="#000000">H</text>
</svg>`

func TestSVG2PNG(t *testing.T) {
	needEngine(t)
	ctx := testCtx(t)
	font, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fonts", "sans-subset.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	dark := func(c color.Color) bool {
		r, g, b, _ := c.RGBA()
		return r < 0x8000 && g < 0x8000 && b < 0x8000
	}
	render := func(fonts [][]byte) (int, int, color.Color, color.Color, int) {
		out, err := SVG2PNG(ctx, []byte(svgTest), fonts, 200, 100)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		bd := img.Bounds()
		n := 0
		for y := bd.Min.Y; y < bd.Max.Y; y++ {
			for x := 120; x < bd.Max.X; x++ {
				if dark(img.At(x, y)) {
					n++
				}
			}
		}
		return bd.Dx(), bd.Dy(), img.At(50, 50), img.At(110, 5), n
	}

	w, h, left, right, withFont := render([][]byte{font})
	if w != 200 || h != 100 {
		t.Errorf("size %dx%d, want 200x100 (stretched, not letterboxed)", w, h)
	}
	if r, g, b, _ := left.RGBA(); r != 0xffff || g != 0 || b != 0 {
		t.Errorf("pixel (50,50) = %v, want red", left)
	}
	if r, g, b, _ := right.RGBA(); r != 0xffff || g != 0xffff || b != 0xffff {
		t.Errorf("pixel (110,5) = %v, want white background", right)
	}
	_, _, _, _, noFont := render(nil)
	t.Logf("dark pixels in the text area: %d with the font, %d without", withFont, noFont)
	if withFont == 0 || noFont != 0 {
		t.Errorf("text drawn with %d dark pixels given the font and %d without; want >0 and 0", withFont, noFont)
	}

	if _, err := SVG2PNG(ctx, []byte("<not svg"), nil, 10, 10); err == nil {
		t.Error("broken SVG was accepted")
	}
	if _, err := SVG2PNG(ctx, []byte(svgTest), nil, 0, 10); err == nil {
		t.Error("zero width was accepted")
	}
}

func TestCanceled(t *testing.T) {
	needEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Version(ctx); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("canceled context: err = %v", err)
	}
}
