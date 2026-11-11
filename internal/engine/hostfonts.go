package engine

import (
	"fmt"
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
// "0". Fonts installed there are used before downloaded ones and are never
// copied into the font cache.
func hostFontDirs() []string {
	return fontDirs(runtime.GOOS, os.Getenv)
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

// hostFontMounts mounts dirs read-only as /hostfonts-<i> and marks them for
// rhwp's font path.
func hostFontMounts(dirs []string) []mount {
	var ms []mount
	for i, d := range dirs {
		ms = append(ms, mount{host: d, guest: fmt.Sprintf("/hostfonts-%d", i), readOnly: true, fontPath: true})
	}
	return ms
}
