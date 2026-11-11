package svgtool

import (
	"encoding/binary"
	"errors"
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
