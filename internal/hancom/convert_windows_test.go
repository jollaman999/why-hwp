//go:build windows

package hancom

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const sampleText = "why-hwp 변환 시험 ABC 123"

// newDocScript writes a new HWPX document holding $env:WHY_HWP_TEXT to
// $env:WHY_HWP_OUT and closes only the Hwp instance it started.
const newDocScript = `$ErrorActionPreference = 'Stop'
$hwp = New-Object -ComObject HWPFrame.HwpObject
try {
    $hwp.XHwpWindows.Item(0).Visible = $false
    $act = $hwp.CreateAction('InsertText')
    $set = $act.CreateSet()
    [void]$act.GetDefault($set)
    $set.SetItem('Text', $env:WHY_HWP_TEXT)
    if (-not $act.Execute($set)) { throw 'InsertText failed' }
    if (-not $hwp.SaveAs($env:WHY_HWP_OUT, 'HWPX', '')) { throw 'SaveAs failed' }
} finally {
    try { [void]$hwp.Clear(1) } catch { }
    $hwp.Quit()
    [void][Runtime.InteropServices.Marshal]::ReleaseComObject($hwp)
}
`

func requireHancom(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("starts Hancom Office; skipped with -short")
	}
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"[bool][type]::GetTypeFromProgID('HWPFrame.HwpObject')").Output()
	if err != nil || strings.TrimSpace(string(out)) != "True" {
		t.Skip("Hancom Office (HWPFrame.HwpObject) is not installed")
	}
}

func newHWPX(t *testing.T, dir, text string) string {
	t.Helper()
	src := filepath.Join(dir, "src.hwpx")
	script := filepath.Join(dir, "newdoc.ps1")
	if err := os.WriteFile(script, []byte(newDocScript), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	cmd.Env = append(os.Environ(), "WHY_HWP_TEXT="+text, "WHY_HWP_OUT="+src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make source document: %v\n%s", err, out)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("make source document: %v\n%s", err, out)
	}
	return src
}

// newDOCX writes a minimal Word document with one paragraph.
func newDOCX(t *testing.T, path, text string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`},
	}
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func convert(t *testing.T, in, out string, f Format, force bool) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := Convert(ctx, Options{In: in, Out: out, To: f, Timeout: 2 * time.Minute, Force: force})
	if err != nil {
		t.Fatalf("Convert %s -> %s: %v", filepath.Base(in), filepath.Base(out), err)
	}
	t.Logf("%s -> %s: %d bytes, md5 %s, RegisterModule %v", filepath.Base(in), filepath.Base(out), res.Size, res.MD5, res.RegisterModule)
	return res
}

func wantPrefix(t *testing.T, path string, prefix []byte) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, prefix) {
		t.Errorf("%s starts with % x, want % x", filepath.Base(path), b[:min(len(b), 8)], prefix)
	}
}

func wantText(t *testing.T, path, text string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), text) {
		t.Errorf("%s = %q, want it to contain %q", filepath.Base(path), b, text)
	}
}

func TestConvertWithHancom(t *testing.T) {
	requireHancom(t)
	before, err := hwpPIDs()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := newHWPX(t, dir, sampleText)
	p := func(name string) string { return filepath.Join(dir, name) }

	convert(t, src, p("a.hwp"), HWP, false)
	wantPrefix(t, p("a.hwp"), []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	convert(t, p("a.hwp"), p("b.hwpx"), HWPX, false)
	wantPrefix(t, p("b.hwpx"), []byte("PK\x03\x04"))
	convert(t, src, p("c.pdf"), PDF, false)
	wantPrefix(t, p("c.pdf"), []byte("%PDF-"))
	convert(t, src, p("d.txt"), TXT, false)
	wantText(t, p("d.txt"), sampleText)

	// The HWP and HWPX outputs read back with the same text.
	convert(t, p("a.hwp"), p("e.txt"), TXT, false)
	wantText(t, p("e.txt"), sampleText)
	convert(t, p("b.hwpx"), p("f.txt"), TXT, false)
	wantText(t, p("f.txt"), sampleText)

	newDOCX(t, p("g.docx"), sampleText)
	convert(t, p("g.docx"), p("g.txt"), TXT, false)
	wantText(t, p("g.txt"), sampleText)

	// An existing output is kept unless Force is set.
	if err := os.WriteFile(p("h.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(context.Background(), Options{In: src, Out: p("h.txt"), To: TXT}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing output: err = %v", err)
	}
	wantText(t, p("h.txt"), "keep")
	convert(t, src, p("h.txt"), TXT, true)
	wantText(t, p("h.txt"), sampleText)

	after, err := hwpPIDs()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(before)
	slices.Sort(after)
	if !slices.Equal(before, after) {
		t.Errorf("Hwp.exe processes before %v, after %v", before, after)
	}
}
