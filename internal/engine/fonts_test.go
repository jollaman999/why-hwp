package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseFontPlan(t *testing.T) {
	sum := strings.Repeat("Ab", 32)
	in := `[{"url":"https://a.example/f/NanumGothic.ttf","sha256":"` + sum + `"},
		{"url":"https://a.example/f/Other.woff2","sha256":null},
		{"url":"https://a.example/f/NanumGothic.ttf","sha256":null}]`
	got, err := parseFontPlan([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (duplicate URL dropped): %+v", len(got), got)
	}
	if got[0].URL != "https://a.example/f/NanumGothic.ttf" || got[0].SHA256 == nil || *got[0].SHA256 != strings.ToLower(sum) {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].SHA256 != nil {
		t.Errorf("entry 1 sha256 = %v, want nil", *got[1].SHA256)
	}

	if got, err := parseFontPlan([]byte("[]\n")); err != nil || len(got) != 0 {
		t.Errorf("empty plan = %v, %v", got, err)
	}
	for _, bad := range []string{
		``, `null`, `{}`, `not json`,
		`[{"url":"file:///etc/passwd","sha256":null}]`,
		`[{"url":"ftp://a.example/x.ttf","sha256":null}]`,
		`[{"url":"","sha256":null}]`,
		`[{"url":"https://a.example/x.ttf","sha256":"abc"}]`,
	} {
		if _, err := parseFontPlan([]byte(bad)); err == nil {
			t.Errorf("parseFontPlan(%q) accepted", bad)
		}
	}
}

func TestDownload(t *testing.T) {
	var hitTarget atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0123456789"))
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 11))
	})
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 4; i++ {
			w.Write([]byte("abcd"))
			w.(http.Flusher).Flush()
		}
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/target", http.StatusFound)
	})
	mux.HandleFunc("/target", func(w http.ResponseWriter, r *http.Request) {
		hitTarget.Add(1)
		w.Write([]byte("x"))
	})
	mux.HandleFunc("/missing", http.NotFound)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	const limit = 10
	b, err := download(ctx, httpClient, srv.URL+"/ok", limit)
	if err != nil || string(b) != "0123456789" {
		t.Errorf("ok: %q, %v", b, err)
	}
	for _, p := range []string{"/big", "/chunked", "/redirect", "/missing"} {
		u := srv.URL + p
		_, err := download(ctx, httpClient, u, limit)
		if err == nil {
			t.Errorf("%s: accepted", p)
			continue
		}
		if !strings.Contains(err.Error(), u) {
			t.Errorf("%s: error %q does not name the URL", p, err)
		}
	}
	if n := hitTarget.Load(); n != 0 {
		t.Errorf("redirect was followed %d time(s)", n)
	}
}

func TestFetchFontChecksum(t *testing.T) {
	body := []byte("font bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	good := sumOf(body)
	if b, err := fetchFont(context.Background(), httpClient, fontReq{URL: srv.URL, SHA256: &good}); err != nil || string(b) != string(body) {
		t.Errorf("good sum: %q, %v", b, err)
	}
	if _, err := fetchFont(context.Background(), httpClient, fontReq{URL: srv.URL}); err != nil {
		t.Errorf("no sum: %v", err)
	}
	bad := sumOf([]byte("other"))
	_, err := fetchFont(context.Background(), httpClient, fontReq{URL: srv.URL, SHA256: &bad})
	if err == nil || !strings.Contains(err.Error(), srv.URL) {
		t.Errorf("bad sum: err = %v, want an error naming the URL", err)
	}
}

func TestDLName(t *testing.T) {
	tests := []struct {
		i    int
		url  string
		want string
	}{
		{0, "https://a.example/f/NanumGothic.TTF", "font-0.ttf"},
		{1, "https://a.example/f/x.woff2?v=1", "font-1.woff2"},
		{2, "https://a.example/f/../../etc/passwd", "font-2"},
		{3, "https://a.example/download", "font-3"},
	}
	for _, tt := range tests {
		if got := dlName(tt.i, tt.url); got != tt.want {
			t.Errorf("dlName(%d, %q) = %q, want %q", tt.i, tt.url, got, tt.want)
		}
	}
}

// fakePlanner answers font-plan with plan(round) minus what font-install
// has stored, and records font-install calls.
type fakePlanner struct {
	plan      func(round int, installed map[string]bool) []string
	round     int
	installed map[string]bool
	installs  []string
	dlDir     string
	files     map[string]string
	planMount [][]mount
}

func (f *fakePlanner) run(_ context.Context, args []string, mounts []mount) ([]byte, error) {
	switch args[0] {
	case "font-plan":
		f.planMount = append(f.planMount, mounts)
		urls := f.plan(f.round, f.installed)
		f.round++
		var parts []string
		for _, u := range urls {
			parts = append(parts, `{"url":"`+u+`","sha256":null}`)
		}
		return []byte("[" + strings.Join(parts, ",") + "]"), nil
	case "font-install":
		u, file := args[2], args[4]
		b, err := os.ReadFile(filepath.Join(f.dlDir, strings.TrimPrefix(file, "/dl/")))
		if err != nil {
			return nil, err
		}
		f.files[u] = string(b)
		f.installs = append(f.installs, u)
		f.installed[u] = true
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected %v", args)
}

func TestEnsureFontsRounds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("font" + r.URL.Path))
	}))
	defer srv.Close()
	a, b := srv.URL+"/a.ttf", srv.URL+"/b.woff2"

	run := func(offline bool, plan func(int, map[string]bool) []string) (*fakePlanner, missingFonts, error) {
		f := &fakePlanner{plan: plan, installed: map[string]bool{}, dlDir: t.TempDir(), files: map[string]string{}}
		miss, err := ensureFonts(context.Background(), f, httpClient, t.TempDir(), "/in/x.hwp", f.dlDir, t.TempDir(), nil, offline)
		return f, miss, err
	}

	chain := func(_ int, inst map[string]bool) []string {
		switch {
		case !inst[a]:
			return []string{a}
		case !inst[b]:
			return []string{b}
		}
		return nil
	}
	f, miss, err := run(false, chain)
	if err != nil || miss.warning() != "" {
		t.Fatalf("chain: miss %v, err %v", miss, err)
	}
	if strings.Join(f.installs, " ") != a+" "+b || f.round != 3 {
		t.Errorf("chain: installs %v in %d plan rounds; want a then b in 3", f.installs, f.round)
	}
	if f.files[a] != "font/a.ttf" || f.files[b] != "font/b.woff2" {
		t.Errorf("chain: installed bytes %q", f.files)
	}

	_, _, err = run(false, func(int, map[string]bool) []string { return []string{a} })
	if err == nil || !strings.Contains(err.Error(), a) || !strings.Contains(err.Error(), "still missing") {
		t.Errorf("same URL after install: err = %v", err)
	}

	f, _, err = run(false, func(round int, _ map[string]bool) []string {
		return []string{fmt.Sprintf("%s/n%d.ttf", srv.URL, round)}
	})
	if err == nil || f.round != maxPlanRounds {
		t.Errorf("endless plan: %d rounds, err = %v; want an error after %d", f.round, err, maxPlanRounds)
	}

	before := hits.Load()
	f, miss, err = run(true, chain)
	if err != nil || len(miss.urls) != 1 || miss.urls[0] != a || miss.errs != nil {
		t.Errorf("offline: miss %v, err %v; want [%s]", miss, err, a)
	}
	if hits.Load() != before || len(f.installs) != 0 {
		t.Errorf("offline: %d download(s), installs %v; want none", hits.Load()-before, f.installs)
	}

	f, miss, err = run(false, func(int, map[string]bool) []string { return nil })
	if err != nil || miss.warning() != "" || f.round != 1 {
		t.Errorf("nothing missing: %d rounds, miss %v, err %v", f.round, miss, err)
	}
}

func TestEnsureFontsDownloadFails(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/gone.ttf" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("font" + r.URL.Path))
	}))
	defer srv.Close()
	gone, a, b := srv.URL+"/gone.ttf", srv.URL+"/a.ttf", srv.URL+"/b.ttf"

	// gone fails and is asked for again in every round; a is installed in
	// the first round, which lets the plan ask for b.
	f := &fakePlanner{installed: map[string]bool{}, dlDir: t.TempDir(), files: map[string]string{}}
	f.plan = func(_ int, inst map[string]bool) []string {
		urls := []string{gone}
		switch {
		case !inst[a]:
			urls = append(urls, a)
		case !inst[b]:
			urls = append(urls, b)
		}
		return urls
	}
	miss, err := ensureFonts(context.Background(), f, httpClient, t.TempDir(), "/in/x.hwp", f.dlDir, t.TempDir(), nil, false)
	if err != nil {
		t.Fatalf("err = %v; want the failed download as a warning", err)
	}
	if strings.Join(f.installs, " ") != a+" "+b || f.round != 3 {
		t.Errorf("installs %v in %d plan rounds; want a then b in 3", f.installs, f.round)
	}
	if len(miss.errs) != 1 || miss.urls != nil || !strings.Contains(miss.errs[0].Error(), gone) {
		t.Errorf("miss = %+v; want one error for %s", miss, gone)
	}
	if w := miss.warning(); !strings.Contains(w, "1 font(s) could not be downloaded") || !strings.Contains(w, "404") {
		t.Errorf("warning = %q", w)
	}
	if hits.Load() != 3 {
		t.Errorf("%d downloads; want 3 (gone once, a, b)", hits.Load())
	}

	// A download with no response ends the downloads.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	c1, c2 := deadURL+"/c1.ttf", deadURL+"/c2.ttf"
	f = &fakePlanner{plan: func(int, map[string]bool) []string { return []string{c1, c2} }, installed: map[string]bool{}, dlDir: t.TempDir(), files: map[string]string{}}
	miss, err = ensureFonts(context.Background(), f, httpClient, t.TempDir(), "/in/x.hwp", f.dlDir, t.TempDir(), nil, false)
	if err != nil || f.round != 1 || len(f.installs) != 0 {
		t.Fatalf("unreachable: %d rounds, installs %v, err %v; want 1 round, none", f.round, f.installs, err)
	}
	if len(miss.errs) != 2 || !strings.Contains(miss.errs[0].Error(), c1) || !strings.Contains(miss.errs[1].Error(), c2+": not tried") {
		t.Errorf("unreachable: miss = %v", miss.errs)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f = &fakePlanner{plan: func(int, map[string]bool) []string { return []string{a} }, installed: map[string]bool{}, dlDir: t.TempDir(), files: map[string]string{}}
	if _, err := ensureFonts(ctx, f, httpClient, t.TempDir(), "/in/x.hwp", f.dlDir, t.TempDir(), nil, false); err != context.Canceled {
		t.Errorf("done ctx: err = %v; want context.Canceled", err)
	}
}

func TestEnsureFontsPlansWithHostFonts(t *testing.T) {
	host := hostFontMounts([]string{"/a", "/b"})
	f := &fakePlanner{plan: func(int, map[string]bool) []string { return nil }, installed: map[string]bool{}, dlDir: t.TempDir(), files: map[string]string{}}
	if _, err := ensureFonts(context.Background(), f, httpClient, "/in-host", "/in/x.hwp", f.dlDir, "/fonts-host", host, false); err != nil {
		t.Fatal(err)
	}
	want := []mount{
		{host: "/in-host", guest: "/in", readOnly: true},
		{host: "/fonts-host", guest: "/fonts", readOnly: true},
		host[0], host[1],
	}
	if len(f.planMount) != 1 || !reflect.DeepEqual(f.planMount[0], want) {
		t.Errorf("font-plan mounts = %+v, want %+v", f.planMount, want)
	}
}
