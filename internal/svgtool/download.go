package svgtool

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jollaman999/why-hwp/internal/sfnt"
)

// EnvOffline names the environment variable that, when "1", stops the
// public font download. It is the same variable that stops the font
// downloads of why-hwp convert.
const EnvOffline = "WHY_HWP_OFFLINE"

// RemoteFont is a font file downloaded when no other font is found.
type RemoteFont struct {
	Name    string // shown in messages
	License string // shown in messages
	URL     string
	SHA256  string // lower-case hex SHA-256 of the file at URL
	File    string // file name in DownloadDir
}

// publicFontBase is the NanumGothic folder of google/fonts at a pinned
// commit. Its OFL.txt gives the licence.
const publicFontBase = "https://cdn.jsdelivr.net/gh/google/fonts@2fe1e8c6b137fb13041a7cfca6fdcd8a910ea4dc/ofl/nanumgothic/"

// PublicFonts are the fonts downloaded when none is found: NanumGothic
// Regular and Bold, static TrueType fonts that allow embedding, under the
// SIL Open Font License 1.1.
var PublicFonts = map[Weight]RemoteFont{
	Regular: {
		Name:    "NanumGothic Regular",
		License: "SIL OFL 1.1",
		URL:     publicFontBase + "NanumGothic-Regular.ttf",
		SHA256:  "76f45ef4a6bcff344c837c95a7dcc26e017e38b5846d5ae0cdcb5b86be2e2d31",
		File:    "NanumGothic-Regular.ttf",
	},
	Bold: {
		Name:    "NanumGothic Bold",
		License: "SIL OFL 1.1",
		URL:     publicFontBase + "NanumGothic-Bold.ttf",
		SHA256:  "f96298f9fb18e364d2370f4c3ce948ac67a2b61af992d7234bc15c42b033c674",
		File:    "NanumGothic-Bold.ttf",
	},
}

// DownloadDir is the folder in the font cache that downloaded fonts are
// kept in. The cache search finds them there on later runs.
const DownloadDir = "svgfont"

// maxDownload caps one font download.
const maxDownload = 32 << 20

// defaultClient downloads fonts. Redirects are not followed: the URLs are
// pinned, so a redirect means the source moved.
var defaultClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Downloader fetches a public font into the font cache when FontFinder
// finds none.
type Downloader struct {
	// Client downloads the fonts. Nil uses a client with a two-minute
	// timeout that refuses redirects.
	Client *http.Client
	// Fonts are the fonts to download per weight. Nil uses PublicFonts.
	Fonts map[Weight]RemoteFont
	// Log receives one line per downloaded font. Nil discards it.
	Log io.Writer
}

func (d *Downloader) font(w Weight) (RemoteFont, bool) {
	fonts := d.Fonts
	if fonts == nil {
		fonts = PublicFonts
	}
	rf, ok := fonts[w]
	return rf, ok
}

// fetch downloads the font for w, checks its SHA-256 and that it is a
// usable TrueType font, and stores it in the DownloadDir folder of cache
// through a temporary file, so that a process reading or downloading the
// same file at the same time sees either the old or the new whole file.
func (d *Downloader) fetch(cache string, w Weight) (*LoadedFont, error) {
	rf, ok := d.font(w)
	if !ok {
		return nil, fmt.Errorf("no public %s font to download", w)
	}
	c := d.Client
	if c == nil {
		c = defaultClient
	}
	b, err := download(c, rf.URL, maxDownload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != rf.SHA256 {
		return nil, fmt.Errorf("download %s: SHA-256 %s, want %s", rf.URL, got, rf.SHA256)
	}
	f, err := sfnt.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rf.URL, err)
	}
	dir := filepath.Join(cache, DownloadDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, rf.File)
	if err := writeReplace(path, b, rf.SHA256); err != nil {
		return nil, err
	}
	if d.Log != nil {
		fmt.Fprintf(d.Log, "%s: downloaded %s (%s) from %s to %s\n", w, rf.Name, rf.License, rf.URL, path)
	}
	return &LoadedFont{Path: path, Source: "download", Font: f}, nil
}

// writeReplace writes data to a temporary file next to path and renames it
// over path. When the rename fails but path already holds a file with the
// SHA-256 want, written by another process, that file is kept.
func writeReplace(path string, data []byte, want string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		if b, rerr := os.ReadFile(path); rerr == nil {
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) == want {
				return nil
			}
		}
		return err
	}
	return nil
}

// download fetches rawURL with c, refusing redirects, statuses other than
// 200 and bodies over limit bytes. Errors name the URL.
func download(c *http.Client, rawURL string, limit int64) ([]byte, error) {
	resp, err := c.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
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
