package svgtool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jollaman999/why-hwp/internal/sfnt"
)

// Environment variables naming the fonts when no flag does.
const (
	EnvFontRegular = "WHY_HWP_FONT_REGULAR"
	EnvFontBold    = "WHY_HWP_FONT_BOLD"
)

// fontNames are the file names searched for, in order of preference, as
// regular and bold pairs.
var fontNames = [][2]string{
	{"NotoSansKR-Regular.ttf", "NotoSansKR-Bold.ttf"},
	{"NanumGothic.ttf", "NanumGothicBold.ttf"},
	{"NanumSquareR.ttf", "NanumSquareB.ttf"},
	{"malgun.ttf", "malgunbd.ttf"},
}

// LoadedFont is a font found for one weight.
type LoadedFont struct {
	Path   string
	Source string // "flag", the environment variable, or "search"
	Font   *sfnt.Font
}

// FontFinder locates the regular and bold fonts.
type FontFinder struct {
	GOOS   string
	Getenv func(string) string
	// Dirs overrides the OS font directories when not nil.
	Dirs []string
}

// FontDirs lists the font directories searched on goos.
func FontDirs(goos string, getenv func(string) string) []string {
	home := getenv("HOME")
	switch goos {
	case "windows":
		windir := getenv("WINDIR")
		if windir == "" {
			windir = `C:\Windows`
		}
		dirs := []string{filepath.Join(windir, "Fonts")}
		if la := getenv("LOCALAPPDATA"); la != "" {
			dirs = append(dirs, filepath.Join(la, "Microsoft", "Windows", "Fonts"))
		}
		return dirs
	case "darwin":
		dirs := []string{"/Library/Fonts"}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, "Library", "Fonts"))
		}
		return dirs
	default:
		dirs := []string{"/usr/share/fonts", "/usr/local/share/fonts"}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, ".local", "share", "fonts"), filepath.Join(home, ".fonts"))
		}
		return dirs
	}
}

// Find returns the font for w: the explicit path when given, then the
// environment variable, then the first usable file found in the font
// directories. Variable, CFF and restricted fonts found by the search are
// skipped; given explicitly they are an error.
func (ff FontFinder) Find(w Weight, explicit string) (*LoadedFont, error) {
	flagName, env := "-regular", EnvFontRegular
	if w == Bold {
		flagName, env = "-bold", EnvFontBold
	}
	if explicit != "" {
		return loadFont(explicit, "flag "+flagName)
	}
	if p := ff.Getenv(env); p != "" {
		return loadFont(p, "$"+env)
	}
	dirs := ff.Dirs
	if dirs == nil {
		dirs = FontDirs(ff.GOOS, ff.Getenv)
	}
	idx := 0
	if w == Bold {
		idx = 1
	}
	want := map[string]int{}
	for i, pair := range fontNames {
		want[strings.ToLower(pair[idx])] = i
	}
	found := make([][]string, len(fontNames))
	for _, d := range dirs {
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				if e != nil && e.IsDir() && p != d {
					return fs.SkipDir
				}
				return nil
			}
			if e.IsDir() {
				return nil
			}
			if i, ok := want[strings.ToLower(e.Name())]; ok {
				found[i] = append(found[i], p)
			}
			return nil
		})
	}
	var skipped []string
	for _, paths := range found {
		for _, p := range paths {
			lf, err := loadFont(p, "search")
			if err == nil {
				return lf, nil
			}
			skipped = append(skipped, err.Error())
		}
	}
	names := make([]string, len(fontNames))
	for i, pair := range fontNames {
		names[i] = pair[idx]
	}
	msg := fmt.Sprintf("no %s font found (looked for %s in %s)", w, strings.Join(names, ", "), strings.Join(dirs, ", "))
	if len(skipped) > 0 {
		sort.Strings(skipped)
		msg += "; skipped: " + strings.Join(skipped, "; ")
	}
	if w == Bold {
		return nil, errors.New(msg + ". The figure has bold text: pass -bold font.ttf or set $" + EnvFontBold)
	}
	return nil, errors.New(msg + ". Install the static Noto Sans KR TrueType fonts (NotoSansKR-Regular.ttf, NotoSansKR-Bold.ttf), or pass -regular font.ttf or set $" + EnvFontRegular)
}

func loadFont(path, source string) (*LoadedFont, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := sfnt.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &LoadedFont{Path: path, Source: source, Font: f}, nil
}
