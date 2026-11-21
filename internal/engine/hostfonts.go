package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnvFontDirs names the environment variable listing extra font directories,
// separated by os.PathListSeparator. They come before the OS font folders.
const EnvFontDirs = "WHY_HWP_FONT_DIRS"

// EnvSystemFonts names the environment variable that, when "0", leaves the
// OS font folders out so that only EnvFontDirs is used.
const EnvSystemFonts = "WHY_HWP_SYSTEM_FONTS"

// hostFontEnv is the variable through which rhwp takes extra font
// directories. Under WASI fontdb's load_system_fonts finds nothing, so the
// host folders reach rhwp only this way.
const hostFontEnv = "RHWP_FONT_PATH"

// hostFontDirs returns the existing font directories of this computer, in
// lookup order: EnvFontDirs, then the OS folders unless EnvSystemFonts is
// "0", each followed by the folders its symbolic links lead out to. Fonts
// installed there are used before downloaded ones and are never copied into
// the font cache.
func hostFontDirs() []string {
	return withLinkTargets(fontDirs(runtime.GOOS, os.Getenv))
}

// fontDirs is hostFontDirs for the given OS and environment.
func fontDirs(goos string, getenv func(string) string) []string {
	var cands []string
	for _, d := range strings.Split(getenv(EnvFontDirs), string(os.PathListSeparator)) {
		if d = strings.TrimSpace(d); d != "" {
			cands = append(cands, d)
		}
	}
	if getenv(EnvSystemFonts) != "0" {
		cands = append(cands, osFontDirs(goos, getenv)...)
	}
	seen := map[string]bool{}
	var dirs []string
	for _, d := range cands {
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// osFontDirs lists the usual font folders of goos, existing or not.
func osFontDirs(goos string, getenv func(string) string) []string {
	home := getenv("HOME")
	under := func(base string, elem ...string) []string {
		if base == "" {
			return nil
		}
		return []string{filepath.Join(append([]string{base}, elem...)...)}
	}
	var dirs []string
	switch goos {
	case "windows":
		dirs = append(dirs, under(getenv("WINDIR"), "Fonts")...)
		dirs = append(dirs, under(getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts")...)
		// Hancom Office keeps its own fonts (HaanYGodic and others) outside
		// the Windows font folder; the version folders change between releases.
		for _, pf := range []string{getenv("ProgramFiles(x86)"), getenv("ProgramFiles")} {
			if pf == "" {
				continue
			}
			for _, sub := range []string{"All", "Hwp"} {
				m, _ := filepath.Glob(filepath.Join(pf, "HNC", "*", "HOffice*", "Shared", "TTF", sub))
				dirs = append(dirs, m...)
			}
		}
	case "darwin":
		dirs = append(dirs, "/System/Library/Fonts", "/Library/Fonts")
		dirs = append(dirs, under(home, "Library", "Fonts")...)
	default:
		if x := getenv("XDG_DATA_HOME"); x != "" {
			dirs = append(dirs, filepath.Join(x, "fonts"))
		} else {
			dirs = append(dirs, under(home, ".local", "share", "fonts")...)
		}
		dirs = append(dirs, under(home, ".fonts")...)
		dirs = append(dirs, "/usr/share/fonts", "/usr/local/share/fonts")
	}
	return dirs
}

// withLinkTargets returns dirs with, after each one, the folders that hold
// the targets of symbolic links under it which point outside every folder
// already in the list. rhwp's fontdb resolves a link with canonicalize,
// which under WASI fails for an absolute target or one that climbs out of
// the mounted folder, and then skips the link; mounting the folder that
// holds the target lets fontdb read the font file itself. A link to a file
// counts only when the target has an extension fontdb loads; a linked folder
// is added only when it holds such a file itself. Links in the added folders
// are followed the same way. Broken links and link loops are skipped, and an
// added folder inside another one in the list is dropped.
func withLinkTargets(dirs []string) []string {
	var covered []string
	for _, d := range dirs {
		covered = append(covered, realDir(d))
	}
	var out []string
	added := map[string]bool{}
	scanned := map[string]bool{}
	var follow func(targets []string)
	follow = func(targets []string) {
		for _, t := range targets {
			if scanned[t] || within(t, covered) {
				continue
			}
			scanned[t] = true
			fonts, next := scanFontDir(t)
			if fonts {
				out = append(out, t)
				added[t] = true
				covered = append(covered, t)
			}
			follow(next)
		}
	}
	for _, d := range dirs {
		out = append(out, d)
		_, next := scanFontDir(realDir(d))
		follow(next)
	}
	var kept []string
	for _, d := range out {
		if added[d] && within(d, without(covered, d)) {
			continue
		}
		kept = append(kept, d)
	}
	return kept
}

// realDir is d with its symbolic links resolved, or d cleaned when that fails.
func realDir(d string) string {
	if r, err := filepath.EvalSymlinks(d); err == nil {
		return r
	}
	return filepath.Clean(d)
}

// within reports whether p is one of dirs or lies under one of them.
func within(p string, dirs []string) bool {
	for _, d := range dirs {
		if p == d {
			return true
		}
		prefix := d
		if !strings.HasSuffix(prefix, string(filepath.Separator)) {
			prefix += string(filepath.Separator)
		}
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// without returns dirs less every entry equal to d.
func without(dirs []string, d string) []string {
	var out []string
	for _, x := range dirs {
		if x != d {
			out = append(out, x)
		}
	}
	return out
}

// scanFontDir walks dir without following links. fonts reports whether a
// regular file with a font extension is under it; targets lists, in walk
// order, the resolved folders that its symbolic links lead to: the folder
// itself for a link to a folder, the parent for a link to a font file.
func scanFontDir(dir string) (fonts bool, targets []string) {
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			t, err := filepath.EvalSymlinks(p)
			if err != nil {
				return nil
			}
			fi, err := os.Stat(t)
			switch {
			case err != nil:
			case fi.IsDir():
				targets = append(targets, t)
			case fi.Mode().IsRegular() && isFontFile(t):
				targets = append(targets, filepath.Dir(t))
			}
		case e.Type().IsRegular() && isFontFile(p):
			fonts = true
		}
		return nil
	})
	return fonts, targets
}

// isFontFile reports whether fontdb loads a file named p: it matches these
// extensions exactly.
func isFontFile(p string) bool {
	switch filepath.Ext(p) {
	case ".ttf", ".ttc", ".TTF", ".TTC", ".otf", ".otc", ".OTF", ".OTC":
		return true
	}
	return false
}

// hostFontMounts mounts dirs read-only as /hostfonts-<i> and marks them for
// rhwp's font path.
func hostFontMounts(dirs []string) []mount {
	var ms []mount
	for i, d := range dirs {
		ms = append(ms, mount{host: d, guest: fmt.Sprintf("/hostfonts-%d", i), readOnly: true, fontPath: true})
	}
	return ms
}
