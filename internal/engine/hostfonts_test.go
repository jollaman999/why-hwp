package engine

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// mkdirs creates each path and returns them.
func mkdirs(t *testing.T, paths ...string) []string {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestFontDirs(t *testing.T) {
	root := t.TempDir()
	j := func(elem ...string) string { return filepath.Join(append([]string{root}, elem...)...) }
	extra := mkdirs(t, j("extra1"), j("extra2"))
	home := j("home")
	pf86, pf := j("pf86"), j("pf")
	mkdirs(t,
		j("win", "Fonts"),
		j("local", "Microsoft", "Windows", "Fonts"),
		filepath.Join(pf86, "HNC", "Office 2024", "HOffice130", "Shared", "TTF", "All"),
		filepath.Join(pf86, "HNC", "Office 2024", "HOffice130", "Shared", "TTF", "Hwp"),
		filepath.Join(pf86, "HNC", "Office 2024", "HOffice130", "Shared", "Fonts"),
		filepath.Join(pf86, "HNC", "Office 2020", "HOffice110", "Shared", "TTF", "All"),
		filepath.Join(pf, "HNC", "Office 2022", "HOffice120", "Shared", "TTF", "Hwp"),
		filepath.Join(pf, "HNC", "Other", "Shared", "TTF", "All"),
		filepath.Join(home, "Library", "Fonts"),
		filepath.Join(home, ".fonts"),
		filepath.Join(home, ".local", "share", "fonts"),
		j("xdg", "fonts"),
	)
	extraEnv := extra[0] + string(os.PathListSeparator) + " " + string(os.PathListSeparator) +
		extra[1] + string(os.PathListSeparator) + j("missing") + string(os.PathListSeparator) + extra[0]
	win := map[string]string{
		"WINDIR":            j("win"),
		"LOCALAPPDATA":      j("local"),
		"ProgramFiles(x86)": pf86,
		"ProgramFiles":      pf,
	}

	tests := []struct {
		name string
		goos string
		env  map[string]string
		want []string
	}{
		{"windows with Hancom Office", "windows", win, []string{
			j("win", "Fonts"),
			j("local", "Microsoft", "Windows", "Fonts"),
			filepath.Join(pf86, "HNC", "Office 2020", "HOffice110", "Shared", "TTF", "All"),
			filepath.Join(pf86, "HNC", "Office 2024", "HOffice130", "Shared", "TTF", "All"),
			filepath.Join(pf86, "HNC", "Office 2024", "HOffice130", "Shared", "TTF", "Hwp"),
			filepath.Join(pf, "HNC", "Office 2022", "HOffice120", "Shared", "TTF", "Hwp"),
		}},
		{"windows, extra dirs first, duplicates and missing dropped", "windows",
			merge(win, map[string]string{EnvFontDirs: extraEnv}),
			append(append([]string{}, extra...),
				j("win", "Fonts"),
				j("local", "Microsoft", "Windows", "Fonts"),
				filepath.Join(pf86, "HNC", "Office 2020", "HOffice110", "Shared", "TTF", "All"),
				filepath.Join(pf86, "HNC", "Office 2024", "HOffice130", "Shared", "TTF", "All"),
				filepath.Join(pf86, "HNC", "Office 2024", "HOffice130", "Shared", "TTF", "Hwp"),
				filepath.Join(pf, "HNC", "Office 2022", "HOffice120", "Shared", "TTF", "Hwp"),
			)},
		{"system fonts off", "windows",
			merge(win, map[string]string{EnvFontDirs: extraEnv, EnvSystemFonts: "0"}), extra},
		{"system fonts off, no extra dirs", "linux",
			map[string]string{"HOME": home, EnvSystemFonts: "0"}, nil},
		{"darwin", "darwin", map[string]string{"HOME": home},
			existing("/System/Library/Fonts", "/Library/Fonts", filepath.Join(home, "Library", "Fonts"))},
		{"linux without XDG_DATA_HOME", "linux", map[string]string{"HOME": home},
			existing(filepath.Join(home, ".local", "share", "fonts"), filepath.Join(home, ".fonts"),
				"/usr/share/fonts", "/usr/local/share/fonts")},
		{"linux with XDG_DATA_HOME", "linux", map[string]string{"HOME": home, "XDG_DATA_HOME": j("xdg")},
			existing(j("xdg", "fonts"), filepath.Join(home, ".fonts"),
				"/usr/share/fonts", "/usr/local/share/fonts")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fontDirs(tt.goos, envMap(tt.env)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("fontDirs:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func merge(a, b map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range a {
		m[k] = v
	}
	for k, v := range b {
		m[k] = v
	}
	return m
}

// existing keeps the paths that are directories on this computer.
func existing(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func TestHostFontMounts(t *testing.T) {
	got := hostFontMounts([]string{"a", "b"})
	want := []mount{
		{host: "a", guest: "/hostfonts-0", readOnly: true, fontPath: true},
		{host: "b", guest: "/hostfonts-1", readOnly: true, fontPath: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hostFontMounts = %+v, want %+v", got, want)
	}
}

// symlink makes link point to target, skipping the test where links cannot
// be made (Windows without the privilege).
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot make symbolic links: %v", err)
	}
}

func writeFiles(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWithLinkTargets(t *testing.T) {
	root := realDir(t.TempDir())
	j := func(elem ...string) string { return filepath.Join(append([]string{root}, elem...)...) }
	r1, r2 := j("r1"), j("r2")
	writeFiles(t,
		j("r1", "sub", "l.ttf"),
		j("r2", "r.ttf"),
		j("o1", "a.ttf"),
		j("o2", "fonts.dir"),
		j("o3", "c.ttf"),
		j("o4", "e.otf"),
		j("o5", "Identity-H"),
		j("o6", "k.ttf"),
		j("o6", "sub", "s.ttf"),
	)
	symlink(t, filepath.Join("..", "o1", "a.ttf"), j("r1", "a-file.ttf"))
	symlink(t, j("o1", "a.ttf"), j("r1", "b-again.ttf"))
	symlink(t, j("o2", "fonts.dir"), j("r1", "c-nonfont"))
	symlink(t, j("o3"), j("r1", "d-dir"))
	symlink(t, j("o4", "e.otf"), j("o3", "e.otf"))
	symlink(t, r1, j("o3", "back"))
	symlink(t, ".", j("o3", "self"))
	symlink(t, j("o5"), j("r1", "e-empty"))
	symlink(t, j("r2", "r.ttf"), j("r1", "f-in-r2.ttf"))
	symlink(t, j("missing.ttf"), j("r1", "g-broken.ttf"))
	symlink(t, "h-loop2", j("r1", "h-loop1"))
	symlink(t, "h-loop1", j("r1", "h-loop2"))
	symlink(t, filepath.Join("sub", "l.ttf"), j("r1", "i-local.ttf"))
	symlink(t, j("o6", "sub"), j("r1", "j-sub"))
	symlink(t, j("o6", "k.ttf"), j("r1", "k.ttf"))

	got := withLinkTargets([]string{r1, r2})
	want := []string{r1, j("o1"), j("o3"), j("o4"), j("o6"), r2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("withLinkTargets:\n got %q\nwant %q", got, want)
	}

	if got := withLinkTargets([]string{r2}); !reflect.DeepEqual(got, []string{r2}) {
		t.Errorf("withLinkTargets without links = %q, want %q", got, []string{r2})
	}

	linked := j("linked-root")
	symlink(t, r1, linked)
	got = withLinkTargets([]string{linked})
	want = []string{linked, j("o1"), j("o3"), j("o4"), r2, j("o6")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("withLinkTargets on a linked folder:\n got %q\nwant %q", got, want)
	}
}

// withFace copies sample.hwpx with every font face renamed to face.
func withFace(t *testing.T, face, dst string) {
	t.Helper()
	zr, err := zip.OpenReader(filepath.Join("..", "..", "testdata", "sample.hwpx"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == "Contents/header.xml" {
			s := string(b)
			for _, old := range []string{"함초롬돋움", "함초롬바탕"} {
				s = strings.ReplaceAll(s, `face="`+old+`"`, `face="`+face+`"`)
			}
			if s == string(b) {
				t.Fatal("header.xml has no face to rename")
			}
			b = []byte(s)
		}
		hdr := f.FileHeader
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestConvertUsesHostFont converts a document set in a font that only a
// host font directory has, once with that directory and once without, and
// looks for the font's name in the PDF.
func TestConvertUsesHostFont(t *testing.T) {
	needEngine(t)
	offline(t)
	ctx := testCtx(t)
	dir := t.TempDir()
	fonts := filepath.Join(dir, "fonts")
	if err := os.Mkdir(fonts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(filepath.Join("..", "..", "testdata", "fonts", "sans-subset.ttf"),
		filepath.Join(fonts, "sans-subset.ttf")); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "doc.hwpx")
	withFace(t, "Sans Serif Collection", src)
	t.Setenv(EnvSystemFonts, "0")

	const name = "SansSerifCollection"
	convert := func(fontDirs string) bool {
		t.Setenv(EnvFontDirs, fontDirs)
		pdf := filepath.Join(dir, "out.pdf")
		if err := Convert(ctx, src, pdf, "pdf"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(pdf)
		if err != nil {
			t.Fatal(err)
		}
		return bytes.Contains(b, []byte(name))
	}
	if !convert(fonts) {
		t.Errorf("PDF made with %s=%s does not name %s", EnvFontDirs, fonts, name)
	}
	if convert("") {
		t.Errorf("PDF made without %s names %s", EnvFontDirs, name)
	}

	linked := filepath.Join(dir, "linked")
	if err := os.Mkdir(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(fonts, "sans-subset.ttf"), filepath.Join(linked, "sans-subset.ttf"))
	if !convert(linked) {
		t.Errorf("PDF made with %s=%s (a link to a font outside it) does not name %s", EnvFontDirs, linked, name)
	}
}
