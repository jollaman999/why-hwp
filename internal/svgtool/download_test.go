package svgtool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jollaman999/why-hwp/internal/engine"
)

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fontServer serves files by path and counts the requests.
func fontServer(t *testing.T, files map[string][]byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/moved.ttf" {
			http.Redirect(w, r, "/r.ttf", http.StatusFound)
			return
		}
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// downloadFinder returns a finder with no OS fonts, an empty cache and the
// environment env, downloading fonts from the server.
func downloadFinder(t *testing.T, env map[string]string, fonts map[Weight]RemoteFont, log *bytes.Buffer) FontFinder {
	t.Helper()
	return FontFinder{
		Getenv:   func(k string) string { return env[k] },
		Dirs:     []string{t.TempDir()},
		CacheDir: t.TempDir(),
		Download: &Downloader{Fonts: fonts, Log: log},
	}
}

func TestEnvOfflineMatchesEngine(t *testing.T) {
	if EnvOffline != engine.EnvOffline {
		t.Errorf("EnvOffline = %q, engine.EnvOffline = %q", EnvOffline, engine.EnvOffline)
	}
}

func TestFontFinderDownload(t *testing.T) {
	regular := namedCopy(t, "NanumGothic", false)
	bold := namedCopy(t, "NanumGothic", true)
	srv, hits := fontServer(t, map[string][]byte{"/r.ttf": regular, "/b.ttf": bold})
	fonts := map[Weight]RemoteFont{
		Regular: {Name: "Test Regular", License: "OFL", URL: srv.URL + "/r.ttf", SHA256: sha(regular), File: "T-Regular.ttf"},
		Bold:    {Name: "Test Bold", License: "OFL", URL: srv.URL + "/b.ttf", SHA256: sha(bold), File: "T-Bold.ttf"},
	}
	var log bytes.Buffer
	ff := downloadFinder(t, map[string]string{}, fonts, &log)

	for _, c := range []struct {
		w    Weight
		file string
		data []byte
	}{{Regular, "T-Regular.ttf", regular}, {Bold, "T-Bold.ttf", bold}} {
		want := filepath.Join(ff.CacheDir, DownloadDir, c.file)
		lf, err := ff.Find(c.w, "")
		if err != nil {
			t.Fatalf("%s: %v", c.w, err)
		}
		if lf.Path != want || lf.Source != "download" {
			t.Errorf("%s: found %s (%s), want %s from the download", c.w, lf.Path, lf.Source, want)
		}
		got, err := os.ReadFile(want)
		if err != nil || !bytes.Equal(got, c.data) {
			t.Errorf("%s: cached file differs from the served one (%v)", c.w, err)
		}
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("%d requests for two fonts, want 2", n)
	}
	for _, want := range []string{"regular: downloaded Test Regular (OFL) from " + srv.URL + "/r.ttf", "bold: downloaded Test Bold (OFL)"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}

	// The next run finds the downloaded fonts in the cache.
	log.Reset()
	for _, w := range []Weight{Regular, Bold} {
		lf, err := ff.Find(w, "")
		if err != nil || lf.Source != "cache" || filepath.Dir(lf.Path) != filepath.Join(ff.CacheDir, DownloadDir) {
			t.Errorf("%s again: %v, %+v; want the downloaded file from the cache", w, err, lf)
		}
	}
	if n := hits.Load(); n != 2 || log.Len() != 0 {
		t.Errorf("second run made %d requests in all and logged %q, want no download", n, log.String())
	}
}

func TestFontFinderDownloadRefused(t *testing.T) {
	regular := namedCopy(t, "NanumGothic", false)
	srv, hits := fontServer(t, map[string][]byte{"/r.ttf": regular, "/text.ttf": []byte("not a font")})
	cases := []struct {
		name string
		env  map[string]string
		rf   RemoteFont
		want []string
		hits int32
	}{
		{"sha mismatch", nil, RemoteFont{URL: srv.URL + "/r.ttf", SHA256: sha([]byte("other"))},
			[]string{"downloading the public font failed", "SHA-256 " + sha(regular) + ", want " + sha([]byte("other"))}, 1},
		{"not found", nil, RemoteFont{URL: srv.URL + "/none.ttf", SHA256: sha(regular)},
			[]string{"downloading the public font failed", "404 Not Found"}, 1},
		{"redirect", nil, RemoteFont{URL: srv.URL + "/moved.ttf", SHA256: sha(regular)},
			[]string{"redirect to \"/r.ttf\" refused"}, 1},
		{"not a font", nil, RemoteFont{URL: srv.URL + "/text.ttf", SHA256: sha([]byte("not a font"))},
			[]string{"downloading the public font failed", "/text.ttf: "}, 1},
		{"offline", map[string]string{EnvOffline: "1"}, RemoteFont{Name: "Test Regular", URL: srv.URL + "/r.ttf", SHA256: sha(regular)},
			[]string{"Test Regular was not downloaded because " + EnvOffline + "=1"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := hits.Load()
			c.rf.File = "T-Regular.ttf"
			var log bytes.Buffer
			ff := downloadFinder(t, c.env, map[Weight]RemoteFont{Regular: c.rf}, &log)
			_, err := ff.Find(Regular, "")
			if err == nil {
				t.Fatal("found a font, want an error")
			}
			msg := err.Error()
			// The original advice stays in the message.
			for _, want := range append(c.want, "no regular font found", "-regular") {
				if !strings.Contains(msg, want) {
					t.Errorf("error lacks %q: %s", want, msg)
				}
			}
			if n := hits.Load() - before; n != c.hits {
				t.Errorf("%d requests, want %d", n, c.hits)
			}
			entries, _ := os.ReadDir(filepath.Join(ff.CacheDir, DownloadDir))
			if len(entries) != 0 || log.Len() != 0 {
				t.Errorf("left %d files in the cache and logged %q, want neither", len(entries), log.String())
			}
		})
	}
}

// TestDownloadConcurrent has several downloads of the same font write the
// same cache file at once: each must get the whole font, and no temporary
// file may be left.
func TestDownloadConcurrent(t *testing.T) {
	regular := namedCopy(t, "NanumGothic", false)
	srv, _ := fontServer(t, map[string][]byte{"/r.ttf": regular})
	d := &Downloader{Fonts: map[Weight]RemoteFont{Regular: {URL: srv.URL + "/r.ttf", SHA256: sha(regular), File: "T-Regular.ttf"}}}
	cache := t.TempDir()
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = d.fetch(cache, Regular)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("download %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(cache, DownloadDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "T-Regular.ttf" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("cache folder holds %q, want only T-Regular.ttf", names)
	}
	got, err := os.ReadFile(filepath.Join(cache, DownloadDir, "T-Regular.ttf"))
	if err != nil || !bytes.Equal(got, regular) {
		t.Errorf("cached file differs from the served one (%v)", err)
	}
}

// TestPublicFontsDownload downloads the real public fonts: their pinned
// SHA-256 must match, and the cache search must find them as NanumGothic.
func TestPublicFontsDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads fonts")
	}
	u, err := url.Parse(PublicFonts[Regular].URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(u.Hostname(), "443"), 5*time.Second)
	if err != nil {
		t.Skipf("no network: %v", err)
	}
	conn.Close()
	var log bytes.Buffer
	ff := downloadFinder(t, map[string]string{}, nil, &log)
	for _, w := range []Weight{Regular, Bold} {
		lf, err := ff.Find(w, "")
		if err != nil {
			t.Fatalf("%s: %v", w, err)
		}
		if lf.Source != "download" || filepath.Base(lf.Path) != PublicFonts[w].File {
			t.Errorf("%s: found %s (%s), want %s from the download", w, lf.Path, lf.Source, PublicFonts[w].File)
		}
		if lf.Font.FSType()&0x0002 != 0 {
			t.Errorf("%s: fsType %#04x restricts embedding", w, lf.Font.FSType())
		}
		again, err := ff.Find(w, "")
		if err != nil || again.Source != "cache" || again.Path != lf.Path {
			t.Errorf("%s again: %v, %+v; want %s from the cache", w, err, again, lf.Path)
		}
	}
	t.Log(strings.TrimSpace(log.String()))
}
