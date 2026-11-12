package svgtool

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jollaman999/why-hwp/internal/sfnt"
)

// variableCopy returns the fixture with its post table renamed to fvar,
// which makes it look like a variable font.
func variableCopy(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(fixtureFont)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(binary.BigEndian.Uint16(data[4:])); i++ {
		rec := data[12+16*i:]
		if string(rec[:4]) == "post" {
			copy(rec, "fvar")
			return data
		}
	}
	t.Fatal("fixture has no post table")
	return nil
}

func TestFontFinder(t *testing.T) {
	font, err := os.ReadFile(fixtureFont)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "truetype", "nanum")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p string, b []byte) {
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The first choice is a variable font and is skipped; the next one is
	// found in a subdirectory. There is no bold font.
	write(filepath.Join(dir, "NotoSansKR-Regular.ttf"), variableCopy(t))
	write(filepath.Join(sub, "NanumGothic.ttf"), font)
	write(filepath.Join(dir, "malgun.ttf"), font)
	env := map[string]string{}
	ff := FontFinder{Getenv: func(k string) string { return env[k] }, Dirs: []string{filepath.Join(dir, "missing"), dir}}

	lf, err := ff.Find(Regular, "")
	if err != nil {
		t.Fatal(err)
	}
	if lf.Path != filepath.Join(sub, "NanumGothic.ttf") || lf.Source != "search" {
		t.Errorf("found %s (%s), want NanumGothic.ttf from the search", lf.Path, lf.Source)
	}
	_, err = ff.Find(Bold, "")
	if err == nil || !strings.Contains(err.Error(), "-bold") {
		t.Errorf("missing bold font: err = %v, want a hint about -bold", err)
	}

	env[EnvFontBold] = filepath.Join(dir, "malgun.ttf")
	lf, err = ff.Find(Bold, "")
	if err != nil || lf.Source != "$"+EnvFontBold {
		t.Errorf("bold from the environment: %v, %+v", err, lf)
	}
	lf, err = ff.Find(Bold, filepath.Join(sub, "NanumGothic.ttf"))
	if err != nil || lf.Source != "flag -bold" {
		t.Errorf("bold from the flag: %v, %+v", err, lf)
	}
	_, err = ff.Find(Regular, filepath.Join(dir, "NotoSansKR-Regular.ttf"))
	if !errors.Is(err, sfnt.ErrVariable) {
		t.Errorf("explicit variable font: err = %v, want ErrVariable", err)
	}

	empty := FontFinder{Getenv: func(string) string { return "" }, Dirs: []string{t.TempDir()}}
	_, err = empty.Find(Regular, "")
	if err == nil || !strings.Contains(err.Error(), "Noto Sans KR") || !strings.Contains(err.Error(), "-regular") {
		t.Errorf("no font at all: err = %v, want advice to install Noto Sans KR or pass -regular", err)
	}
}

func TestFontDirs(t *testing.T) {
	env := map[string]string{"HOME": "/home/u", "WINDIR": `D:\Win`, "LOCALAPPDATA": `C:\Users\u\AppData\Local`}
	get := func(k string) string { return env[k] }
	win := FontDirs("windows", get)
	if len(win) != 2 || !strings.HasSuffix(win[0], "Fonts") || !strings.Contains(win[1], "Microsoft") {
		t.Errorf("windows dirs %q", win)
	}
	linux := FontDirs("linux", get)
	if len(linux) != 4 || linux[0] != "/usr/share/fonts" || linux[1] != "/usr/local/share/fonts" {
		t.Errorf("linux dirs %q", linux)
	}
	mac := FontDirs("darwin", get)
	if len(mac) != 2 || mac[0] != "/Library/Fonts" {
		t.Errorf("darwin dirs %q", mac)
	}
}

// namedCopy returns the fixture renamed to family, with weight 700 when bold.
func namedCopy(t *testing.T, family string, bold bool) []byte {
	t.Helper()
	data, err := os.ReadFile(fixtureFont)
	if err != nil {
		t.Fatal(err)
	}
	f, err := sfnt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := f.SubsetAs(f.Runes(), sfnt.Naming{Family: family, Bold: bold})
	if err != nil {
		t.Fatal(err)
	}
	return sub.Font
}

// tableRecord returns the table directory record of tag in data.
func tableRecord(t *testing.T, data []byte, tag string) []byte {
	t.Helper()
	for i := 0; i < int(binary.BigEndian.Uint16(data[4:])); i++ {
		rec := data[12+16*i:]
		if string(rec[:4]) == tag {
			return rec[:16]
		}
	}
	t.Fatalf("font has no %q table", tag)
	return nil
}

func TestFontFinderCache(t *testing.T) {
	cache := t.TempDir()
	put := func(dir, file string, b []byte) string {
		d := filepath.Join(cache, "webfonts", dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(d, file)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	restricted := namedCopy(t, "Noto Sans KR", false)
	os2 := binary.BigEndian.Uint32(tableRecord(t, restricted, "OS/2")[8:])
	binary.BigEndian.PutUint16(restricted[os2+8:], 0x0002)
	variable := namedCopy(t, "Noto Sans KR", false)
	copy(tableRecord(t, variable, "post"), "fvar")

	// The first choice, Noto Sans KR, is restricted in one entry and
	// variable in another, so the regular font is NanumGothic. A family
	// that only starts with Noto Sans KR does not match.
	put("10", "font.otf", restricted)
	put("11", "font.otf", variable)
	put("12", "font.otf", namedCopy(t, "Noto Sans KR ExtraLight", false))
	nanum := put("20", "font.otf", namedCopy(t, "NanumGothic", false))
	notoBold := put("30", "font.otf", namedCopy(t, "Noto Sans KR", true))
	put("40", "resource.json", []byte("{}"))

	osDir := t.TempDir()
	ff := FontFinder{Getenv: func(string) string { return "" }, Dirs: []string{osDir}, CacheDir: cache}
	lf, err := ff.Find(Regular, "")
	if err != nil {
		t.Fatal(err)
	}
	if lf.Path != nanum || lf.Source != "cache" {
		t.Errorf("regular: found %s (%s), want %s from the cache", lf.Path, lf.Source, nanum)
	}
	lf, err = ff.Find(Bold, "")
	if err != nil {
		t.Fatal(err)
	}
	if lf.Path != notoBold || lf.Source != "cache" {
		t.Errorf("bold: found %s (%s), want %s from the cache", lf.Path, lf.Source, notoBold)
	}

	// The OS font folders come before the cache.
	font, err := os.ReadFile(fixtureFont)
	if err != nil {
		t.Fatal(err)
	}
	osFont := filepath.Join(osDir, "malgun.ttf")
	if err := os.WriteFile(osFont, font, 0o644); err != nil {
		t.Fatal(err)
	}
	lf, err = ff.Find(Regular, "")
	if err != nil || lf.Path != osFont || lf.Source != "search" {
		t.Errorf("OS folder and cache: %v, %+v; want %s from the search", err, lf, osFont)
	}

	// Refused cache fonts are listed, and nothing else is taken.
	only := t.TempDir()
	for i, b := range [][]byte{restricted, variable} {
		d := filepath.Join(only, "webfonts", fmt.Sprint(i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "font.otf"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ff = FontFinder{Getenv: func(string) string { return "" }, Dirs: []string{t.TempDir()}, CacheDir: only}
	_, err = ff.Find(Regular, "")
	if err == nil || !strings.Contains(err.Error(), "restricted") || !strings.Contains(err.Error(), "variable") || !strings.Contains(err.Error(), only) {
		t.Errorf("only refused cache fonts: err = %v, want both refusals and the cache folder", err)
	}
	_, err = ff.Find(Bold, "")
	if err == nil || strings.Contains(err.Error(), "skipped") {
		t.Errorf("no bold cache font: err = %v, want not found without skipped regular fonts", err)
	}
}
