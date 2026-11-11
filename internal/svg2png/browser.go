package svg2png

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Kind is the browser engine family, which decides the command line.
type Kind int

const (
	Chromium Kind = iota // Chrome, Edge, Chromium
	Firefox
)

func (k Kind) String() string {
	if k == Firefox {
		return "firefox"
	}
	return "chromium"
}

// Browser is a browser executable that can take headless screenshots.
type Browser struct {
	Name string
	Path string
	Kind Kind
}

// Candidate is one place to look for a browser. Path is either an absolute
// path or a bare command name that is looked up in PATH.
type Candidate struct {
	Name string
	Path string
}

// EnvBrowser names the environment variable that overrides browser lookup.
const EnvBrowser = "WHY_HWP_BROWSER"

// Candidates lists the default browser locations for goos in preference
// order: Chrome, Edge, Chromium, Firefox.
func Candidates(goos string, getenv func(string) string) []Candidate {
	switch goos {
	case "windows":
		roots := []string{
			envOr(getenv, "ProgramFiles", `C:\Program Files`),
			envOr(getenv, "ProgramFiles(x86)", `C:\Program Files (x86)`),
		}
		if la := getenv("LocalAppData"); la != "" {
			roots = append(roots, la)
		}
		apps := []struct{ name, rel string }{
			{"Chrome", `Google\Chrome\Application\chrome.exe`},
			{"Edge", `Microsoft\Edge\Application\msedge.exe`},
			{"Chromium", `Chromium\Application\chrome.exe`},
			{"Firefox", `Mozilla Firefox\firefox.exe`},
		}
		var c []Candidate
		for _, a := range apps {
			for _, r := range roots {
				c = append(c, Candidate{a.name, strings.TrimRight(r, `\`) + `\` + a.rel})
			}
		}
		return c
	case "darwin":
		return []Candidate{
			{"Chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
			{"Edge", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
			{"Chromium", "/Applications/Chromium.app/Contents/MacOS/Chromium"},
			{"Firefox", "/Applications/Firefox.app/Contents/MacOS/firefox"},
		}
	default:
		return []Candidate{
			{"Chrome", "google-chrome"},
			{"Chrome", "google-chrome-stable"},
			{"Edge", "microsoft-edge"},
			{"Edge", "microsoft-edge-stable"},
			{"Chromium", "chromium"},
			{"Chromium", "chromium-browser"},
			{"Firefox", "firefox"},
		}
	}
}

func envOr(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

// KindOf guesses the engine from the executable name.
func KindOf(path string) Kind {
	if strings.Contains(strings.ToLower(filepath.Base(path)), "firefox") {
		return Firefox
	}
	return Chromium
}

// Finder holds the OS hooks used by Find, so tests can fake them.
type Finder struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	IsFile   func(string) bool
}

// DefaultFinder uses the real environment and file system.
func DefaultFinder(goos string) Finder {
	return Finder{
		GOOS:     goos,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		IsFile: func(p string) bool {
			fi, err := os.Stat(p)
			return err == nil && !fi.IsDir()
		},
	}
}

// NotFoundError lists every place that was searched.
type NotFoundError struct {
	Tried []string
}

func (e *NotFoundError) Error() string {
	var b strings.Builder
	b.WriteString("no browser found; install Chrome, Edge, Chromium or Firefox, or pass -browser / set " + EnvBrowser + ". Searched:")
	for _, t := range e.Tried {
		b.WriteString("\n  " + t)
	}
	return b.String()
}

// Find picks the browser: explicit (the -browser flag) first, then
// $WHY_HWP_BROWSER, then the default candidates. An explicit or environment
// value that does not resolve is an error rather than a silent fallback.
func (f Finder) Find(explicit string) (Browser, error) {
	if explicit != "" {
		return f.resolve(explicit, "-browser")
	}
	if env := f.Getenv(EnvBrowser); env != "" {
		return f.resolve(env, EnvBrowser)
	}
	var tried []string
	for _, c := range Candidates(f.GOOS, f.Getenv) {
		if p, ok := f.locate(c.Path); ok {
			return Browser{Name: c.Name, Path: p, Kind: KindOf(p)}, nil
		}
		if isBare(c.Path) {
			tried = append(tried, c.Path+" (in PATH)")
		} else {
			tried = append(tried, c.Path)
		}
	}
	return Browser{}, &NotFoundError{Tried: tried}
}

func (f Finder) resolve(p, source string) (Browser, error) {
	got, ok := f.locate(p)
	if !ok {
		return Browser{}, fmt.Errorf("browser %q from %s not found", p, source)
	}
	return Browser{Name: filepath.Base(got), Path: got, Kind: KindOf(got)}, nil
}

func (f Finder) locate(p string) (string, bool) {
	if isBare(p) {
		got, err := f.LookPath(p)
		return got, err == nil
	}
	return p, f.IsFile(p)
}

func isBare(p string) bool {
	return !strings.ContainsAny(p, `/\`)
}
