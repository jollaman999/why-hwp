package svgtool

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

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

// cacheFamilies are, in the order of fontNames, the family names looked for
// in the why-hwp font cache, where files are named by hash rather than by
// font. The first name of each entry is shown in messages; matching ignores
// case, spaces, hyphens and underscores.
var cacheFamilies = [][2][]string{
	{{"Noto Sans KR"}, {"Noto Sans KR"}},
	{{"NanumGothic"}, {"NanumGothic", "NanumGothicBold"}},
	{{"NanumSquare", "NanumSquareR"}, {"NanumSquare", "NanumSquareB"}},
	{{"Malgun Gothic"}, {"Malgun Gothic"}},
}

// LoadedFont is a font found for one weight.
type LoadedFont struct {
	Path   string
	Source string // "flag", the environment variable, "search" or "cache"
	Font   *sfnt.Font
}

// FontFinder locates the regular and bold fonts.
type FontFinder struct {
	GOOS   string
	Getenv func(string) string
	// Dirs overrides the OS font directories when not nil.
	Dirs []string
	// CacheDir is the why-hwp font cache, searched by family name after
	// the font directories. Empty skips it.
	CacheDir string
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
// directories, then the first usable font in the cache directory whose
// family and weight match. Variable, CFF and restricted fonts found by the
// search are skipped; given explicitly they are an error.
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
	if ff.CacheDir != "" {
		for _, p := range cacheMatches(ff.CacheDir, w) {
			lf, err := loadFont(p, "cache")
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
	msg := fmt.Sprintf("no %s font found (looked for %s in %s", w, strings.Join(names, ", "), strings.Join(dirs, ", "))
	if ff.CacheDir != "" {
		fams := make([]string, len(cacheFamilies))
		for i, f := range cacheFamilies {
			fams[i] = f[idx][0]
		}
		msg += fmt.Sprintf(", and for the families %s in %s", strings.Join(fams, ", "), ff.CacheDir)
	}
	msg += ")"
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

// familyKey is name in lower case without spaces, hyphens and underscores.
func familyKey(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '_':
			return -1
		}
		return r
	}, strings.ToLower(name))
}

// cacheMatches returns the .ttf and .otf files under dir whose family and
// weight match w, in the order of cacheFamilies and then of their paths.
// Files that cannot be read are left out.
func cacheMatches(dir string, w Weight) []string {
	idx := 0
	if w == Bold {
		idx = 1
	}
	want := map[string]int{}
	for i, f := range cacheFamilies {
		for _, name := range f[idx] {
			want[familyKey(name)] = i
		}
	}
	found := make([][]string, len(cacheFamilies))
	_ = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if e != nil && e.IsDir() && p != dir {
				return fs.SkipDir
			}
			return nil
		}
		if e.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".ttf", ".otf":
		default:
			return nil
		}
		fi, err := readFaceInfo(p)
		if err != nil || fi.bold != (w == Bold) || fi.italic || fi.weight != 0 && fi.weight != 400 && fi.weight != 700 {
			return nil
		}
		best := -1
		for _, fam := range fi.families {
			if i, ok := want[familyKey(fam)]; ok && (best < 0 || i < best) {
				best = i
			}
		}
		if best >= 0 {
			found[best] = append(found[best], p)
		}
		return nil
	})
	var out []string
	for _, paths := range found {
		out = append(out, paths...)
	}
	return out
}

// faceInfo is what cacheMatches needs from a font file.
type faceInfo struct {
	families []string // every name ID 1 and 16 string
	weight   int      // OS/2 usWeightClass, 0 without an OS/2 table
	bold     bool
	italic   bool
}

// maxNameTable caps the name table read by readFaceInfo.
const maxNameTable = 1 << 20

// readFaceInfo reads the family names and the weight of a single sfnt font
// (TrueType or CFF) from its table directory and its name and OS/2 tables,
// without reading the rest of the file.
func readFaceInfo(path string) (faceInfo, error) {
	var fi faceInfo
	f, err := os.Open(path)
	if err != nil {
		return fi, err
	}
	defer f.Close()
	hdr := make([]byte, 12)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return fi, err
	}
	switch string(hdr[:4]) {
	case "\x00\x01\x00\x00", "true", "OTTO":
	default:
		return fi, errors.New("not a single sfnt font")
	}
	n := int(binary.BigEndian.Uint16(hdr[4:]))
	dir := make([]byte, 16*n)
	if _, err := io.ReadFull(f, dir); err != nil {
		return fi, err
	}
	table := func(tag string, limit int) ([]byte, error) {
		for i := 0; i < n; i++ {
			rec := dir[16*i:]
			if string(rec[:4]) != tag {
				continue
			}
			off, length := int64(binary.BigEndian.Uint32(rec[8:])), int(binary.BigEndian.Uint32(rec[12:]))
			if length > limit {
				return nil, fmt.Errorf("%s table is too large", tag)
			}
			b := make([]byte, length)
			if _, err := f.ReadAt(b, off); err != nil {
				return nil, err
			}
			return b, nil
		}
		return nil, nil
	}
	name, err := table("name", maxNameTable)
	if err != nil {
		return fi, err
	}
	subfamily := ""
	if len(name) >= 6 {
		count, strOff := int(binary.BigEndian.Uint16(name[2:])), int(binary.BigEndian.Uint16(name[4:]))
		for i := 0; i < count; i++ {
			r := 6 + 12*i
			if r+12 > len(name) {
				break
			}
			pid, eid := binary.BigEndian.Uint16(name[r:]), binary.BigEndian.Uint16(name[r+2:])
			id := binary.BigEndian.Uint16(name[r+6:])
			length, off := int(binary.BigEndian.Uint16(name[r+8:])), int(binary.BigEndian.Uint16(name[r+10:]))
			if (id != 1 && id != 2 && id != 16) || strOff+off+length > len(name) {
				continue
			}
			raw := name[strOff+off : strOff+off+length]
			var s string
			switch {
			case pid == 0 || pid == 3 && (eid == 1 || eid == 10):
				u := make([]uint16, len(raw)/2)
				for j := range u {
					u[j] = binary.BigEndian.Uint16(raw[2*j:])
				}
				s = string(utf16.Decode(u))
			case pid == 1 && eid == 0:
				s = string(raw)
			default:
				continue
			}
			if id == 2 {
				if subfamily == "" {
					subfamily = s
				}
				continue
			}
			fi.families = append(fi.families, s)
		}
	}
	os2, err := table("OS/2", 1<<10)
	if err != nil {
		return fi, err
	}
	if len(os2) >= 64 {
		fi.weight = int(binary.BigEndian.Uint16(os2[4:]))
		sel := binary.BigEndian.Uint16(os2[62:])
		fi.bold = sel&0x0020 != 0 || fi.weight == 700
		fi.italic = sel&0x0001 != 0
	} else {
		sub := strings.ToLower(subfamily)
		fi.bold = strings.Contains(sub, "bold")
		fi.italic = strings.Contains(sub, "italic")
	}
	return fi, nil
}
