package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// EnvOffline names the environment variable that, when "1", stops font
// downloads; conversion then uses only the fonts already in the cache.
const EnvOffline = "WHY_HWP_OFFLINE"

// maxFontSize caps one font download.
const maxFontSize = 32 << 20

// httpClient downloads fonts. Redirects are not followed: the font ledger
// lists final URLs, so a redirect means the source moved.
var httpClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// fontReq is one entry of the font-plan output.
type fontReq struct {
	URL    string  `json:"url"`
	SHA256 *string `json:"sha256"`
}

// parseFontPlan reads the JSON array printed by font-plan, drops duplicate
// URLs and rejects entries that are not http(s) URLs.
func parseFontPlan(b []byte) ([]fontReq, error) {
	var reqs []fontReq
	if err := json.Unmarshal(b, &reqs); err != nil {
		return nil, fmt.Errorf("engine font-plan: bad output: %w", err)
	}
	if reqs == nil {
		return nil, errors.New("engine font-plan: bad output: not a JSON array")
	}
	seen := map[string]bool{}
	out := reqs[:0]
	for _, r := range reqs {
		u, err := url.Parse(r.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("engine font-plan: bad URL %q", r.URL)
		}
		if r.SHA256 != nil {
			s := strings.ToLower(*r.SHA256)
			if _, err := hex.DecodeString(s); err != nil || len(s) != 2*sha256.Size {
				return nil, fmt.Errorf("engine font-plan: bad sha256 %q for %s", *r.SHA256, r.URL)
			}
			r.SHA256 = &s
		}
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		out = append(out, r)
	}
	return out, nil
}

// download fetches rawURL with c, refusing redirects, statuses other than
// 200 and bodies over limit bytes. Errors name the URL.
func download(ctx context.Context, c *http.Client, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, &unreachableError{fmt.Errorf("download %s: %w", rawURL, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("download %s: redirect to %q refused", rawURL, resp.Header.Get("Location"))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", rawURL, resp.Status)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("download %s: %d bytes is over the %d byte limit", rawURL, resp.ContentLength, limit)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download %s: body is over the %d byte limit", rawURL, limit)
	}
	return b, nil
}

// unreachableError is a download that got no response, so that the next one
// is likely to fail the same way.
type unreachableError struct{ err error }

func (e *unreachableError) Error() string { return e.err.Error() }
func (e *unreachableError) Unwrap() error { return e.err }

// fetchFont downloads r and checks its SHA-256 when the plan gives one.
func fetchFont(ctx context.Context, c *http.Client, r fontReq) ([]byte, error) {
	b, err := download(ctx, c, r.URL, maxFontSize)
	if err != nil {
		return nil, err
	}
	if r.SHA256 != nil {
		h := sha256.Sum256(b)
		if got := hex.EncodeToString(h[:]); got != *r.SHA256 {
			return nil, fmt.Errorf("download %s: SHA-256 %s, want %s", r.URL, got, *r.SHA256)
		}
	}
	return b, nil
}

// dlName is the file name under /dl for the i-th download. It keeps a known
// font extension from the URL path so the engine can tell the format apart.
func dlName(i int, rawURL string) string {
	name := fmt.Sprintf("font-%d", i)
	u, err := url.Parse(rawURL)
	if err != nil {
		return name
	}
	switch ext := strings.ToLower(path.Ext(u.Path)); ext {
	case ".ttf", ".otf", ".ttc", ".woff", ".woff2":
		return name + ext
	}
	return name
}

// FontCachePath returns the font cache directory, why-hwp/fonts in the user
// cache folder, without creating it. Fonts installed by Convert are kept
// there in rhwp's cache layout.
func FontCachePath() (string, error) {
	base, err := userCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "why-hwp", "fonts"), nil
}

// fontCacheDir returns the font cache directory mounted as /fonts,
// creating it if needed.
func fontCacheDir() (string, error) {
	dir, err := FontCachePath()
	if err != nil {
		return "", fmt.Errorf("engine: font cache: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("engine: font cache: %w", err)
	}
	return dir, nil
}

// missingFonts lists the fonts the document needs that ensureFonts left out
// of the cache: in offline mode the URLs it did not download, otherwise the
// downloads that failed.
type missingFonts struct {
	urls []string // offline mode
	errs []error  // failed downloads
}

// warning is the text Warn gets for m, or "" when nothing is missing.
func (m missingFonts) warning() string {
	switch {
	case len(m.urls) > 0:
		return fmt.Sprintf("%s=1: at least %d font(s) not downloaded, output may use substitutes: %s",
			EnvOffline, len(m.urls), strings.Join(m.urls, ", "))
	case len(m.errs) > 0:
		msgs := make([]string, len(m.errs))
		for i, err := range m.errs {
			msgs[i] = err.Error()
		}
		return fmt.Sprintf("%d font(s) could not be downloaded, output uses substitutes: %s",
			len(m.errs), strings.Join(msgs, "; "))
	}
	return ""
}

// runner runs one engine command; *engine implements it.
type runner interface {
	run(ctx context.Context, args []string, mounts []mount) ([]byte, error)
}

// maxPlanRounds caps the font-plan, download, font-install rounds. A font
// can be asked for only once another one is installed, so one round may not
// be enough.
const maxPlanRounds = 8

// ensureFonts asks the engine which fonts guestIn needs and installs the
// missing ones into fontsDir, repeating until the plan is empty. Fonts found
// in hostFonts are not asked for. In offline mode nothing is downloaded and
// the first plan's URLs are returned instead. A font whose download fails is
// not tried again and is returned with its error, so that the engine draws
// the document with a substitute, as in offline mode. A download that gets
// no response ends the downloads, the fonts not yet downloaded being left
// out too; only a done ctx is an error.
func ensureFonts(ctx context.Context, e runner, c *http.Client, inDir, guestIn, dlDir, fontsDir string, hostFonts []mount, offline bool) (missingFonts, error) {
	installed := map[string]bool{}
	failed := map[string]bool{}
	var miss missingFonts
	n := 0
	for round := 0; round < maxPlanRounds; round++ {
		out, err := e.run(ctx, []string{"font-plan", "--in", guestIn}, append([]mount{
			{host: inDir, guest: "/in", readOnly: true},
			{host: fontsDir, guest: "/fonts", readOnly: true},
		}, hostFonts...))
		if err != nil {
			return missingFonts{}, err
		}
		reqs, err := parseFontPlan(out)
		if err != nil {
			return missingFonts{}, err
		}
		if offline {
			for _, r := range reqs {
				miss.urls = append(miss.urls, r.URL)
			}
			return miss, nil
		}
		tried := false
		for _, r := range reqs {
			if failed[r.URL] {
				continue
			}
			if installed[r.URL] {
				return missingFonts{}, fmt.Errorf("engine font-plan: %s is still missing after font-install", r.URL)
			}
			tried = true
			b, err := fetchFont(ctx, c, r)
			if err != nil {
				if ctx.Err() != nil {
					return missingFonts{}, ctx.Err()
				}
				failed[r.URL] = true
				miss.errs = append(miss.errs, err)
				var ue *unreachableError
				if errors.As(err, &ue) {
					for _, rest := range reqs {
						if !failed[rest.URL] && !installed[rest.URL] {
							failed[rest.URL] = true
							miss.errs = append(miss.errs, fmt.Errorf("download %s: not tried after the error above", rest.URL))
						}
					}
					return miss, nil
				}
				continue
			}
			name := dlName(n, r.URL)
			n++
			if err := os.WriteFile(filepath.Join(dlDir, name), b, 0o644); err != nil {
				return missingFonts{}, err
			}
			if _, err := e.run(ctx, []string{"font-install", "--url", r.URL, "--file", "/dl/" + name}, []mount{
				{host: dlDir, guest: "/dl", readOnly: true},
				{host: fontsDir, guest: "/fonts"},
			}); err != nil {
				return missingFonts{}, fmt.Errorf("%w (font %s)", err, r.URL)
			}
			installed[r.URL] = true
		}
		if !tried {
			return miss, nil
		}
	}
	return missingFonts{}, fmt.Errorf("engine font-plan: fonts still missing after %d rounds", maxPlanRounds)
}
