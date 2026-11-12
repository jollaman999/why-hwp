package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sumOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestVerifySHA256(t *testing.T) {
	data := []byte("engine")
	good := sumOf(data)
	tests := []struct {
		name, sum string
		ok        bool
	}{
		{"sha256sum line", good + "  engine.wasm.gz\n", true},
		{"bare", good, true},
		{"upper case", strings.ToUpper(good), true},
		{"other data", sumOf([]byte("other")), false},
		{"empty", "  \n", false},
		{"short", good[:10], false},
		{"not hex", strings.Repeat("z", 64), false},
	}
	for _, tt := range tests {
		err := verifySHA256(data, tt.sum)
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok %v", tt.name, err, tt.ok)
		}
	}
}

func TestUnpack(t *testing.T) {
	wasm := []byte("\x00asm\x01\x00\x00\x00 module body")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(wasm)
	zw.Close()
	gz := buf.Bytes()

	got, err := unpack(gz, sumOf(gz)+"  engine.wasm.gz\n")
	if err != nil || !bytes.Equal(got, wasm) {
		t.Fatalf("unpack = %q, %v; want %q", got, err, wasm)
	}
	if _, err := unpack(gz, sumOf(wasm)); err == nil {
		t.Error("checksum of the decompressed module was accepted; the sum is of the .gz file")
	}
	if _, err := unpack(nil, sumOf(nil)); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty module: err = %v", err)
	}
	bad := []byte("not gzip")
	if _, err := unpack(bad, sumOf(bad)); err == nil {
		t.Error("non-gzip data was accepted")
	}
}

func TestTZOffsetMin(t *testing.T) {
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		loc  *time.Location
		want int
	}{
		{time.UTC, 0},
		{time.FixedZone("KST", 9*3600), 540},
		{time.FixedZone("NST", -(3*3600 + 30*60)), -210},
		{time.FixedZone("NPT", 5*3600+45*60), 345},
	}
	for _, tt := range tests {
		if got := tzOffsetMin(at.In(tt.loc)); got != tt.want {
			t.Errorf("%s: %d, want %d", tt.loc, got, tt.want)
		}
	}
}

func TestLastLine(t *testing.T) {
	tests := map[string]string{
		"":                           "",
		"one":                        "one",
		"warn: x\nerror: bad file\n": "error: bad file",
		"a\n\n  b  \n\n":             "b",
		"a\r\nb\r\n":                 "b",
	}
	for in, want := range tests {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoaderCtxDoneFirst(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	want := &engine{}
	l := newLoader(func() (*engine, error) {
		calls.Add(1)
		<-release
		return want, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	e, err := l.get(ctx)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("get waited %v after ctx ended", took)
	}
	if e != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("get = %v, %v; want nil, deadline exceeded", e, err)
	}

	got := make(chan *engine)
	go func() {
		e, err := l.get(context.Background())
		if err != nil {
			t.Error(err)
		}
		got <- e
	}()
	select {
	case e := <-got:
		t.Fatalf("second get returned %v before the compilation finished", e)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if e := <-got; e != want {
		t.Errorf("second get = %p, want %p", e, want)
	}
	if e, err := l.get(context.Background()); e != want || err != nil {
		t.Errorf("third get = %p, %v; want %p", e, err, want)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("compiled %d times, want 1", n)
	}
}

func TestLoaderConcurrentShareCompile(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	want := &engine{}
	l := newLoader(func() (*engine, error) {
		calls.Add(1)
		<-release
		return want, nil
	})
	const n = 8
	res := make(chan *engine, n)
	for range n {
		go func() {
			e, _ := l.get(context.Background())
			res <- e
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range n {
		if e := <-res; e != want {
			t.Errorf("get = %p, want %p", e, want)
		}
	}
	if c := calls.Load(); c != 1 {
		t.Errorf("compiled %d times, want 1", c)
	}
}

func TestLoaderCompileError(t *testing.T) {
	compileErr := errors.New("engine: compile: bad module")
	var calls atomic.Int32
	l := newLoader(func() (*engine, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return nil, compileErr
	})
	for i := range 3 {
		e, err := l.get(context.Background())
		if e != nil || err != compileErr {
			t.Errorf("get %d = %v, %v; want nil, %v", i, e, err, compileErr)
		}
	}
	if c := calls.Load(); c != 1 {
		t.Errorf("compiled %d times, want 1", c)
	}
}

func TestLoaderFinishedBeatsDoneCtx(t *testing.T) {
	want := &engine{}
	l := newLoader(func() (*engine, error) { return want, nil })
	if _, err := l.get(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 20 {
		if e, err := l.get(ctx); e != want || err != nil {
			t.Fatalf("get with a done ctx after compiling = %v, %v; want the engine", e, err)
		}
	}
}

func TestRemoveStaleTemp(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "wazero-v1.12.0-amd64-linux")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-staleTempAge - time.Minute)
	fresh := now.Add(-staleTempAge + time.Minute)
	files := []struct {
		path  string
		mtime time.Time
		keep  bool
	}{
		{filepath.Join(sub, "abcd.123.tmp"), old, false},
		{filepath.Join(dir, "top.456.tmp"), old, false},
		{filepath.Join(sub, "abcd.789.tmp"), fresh, true},
		{filepath.Join(sub, "abcd"), old, true},
		{filepath.Join(sub, "abcd.tmp.bak"), old, true},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(f.path, f.mtime, f.mtime); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.tmp")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(outside, old, old)
	deep := filepath.Join(sub, "deeper")
	os.Mkdir(deep, 0o700)
	deepTmp := filepath.Join(deep, "d.tmp")
	os.WriteFile(deepTmp, []byte("x"), 0o600)
	os.Chtimes(deepTmp, old, old)

	removeStaleTemp(dir, now)

	for _, f := range files {
		_, err := os.Stat(f.path)
		if kept := err == nil; kept != f.keep {
			t.Errorf("%s: kept %v, want %v (stat err %v)", filepath.Base(f.path), kept, f.keep, err)
		}
	}
	for _, p := range []string{outside, deepTmp} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s outside the cache levels was removed: %v", p, err)
		}
	}

	removeStaleTemp(filepath.Join(dir, "missing"), now)
}

func TestCacheKey(t *testing.T) {
	a := sumOf([]byte("engine a"))
	b := sumOf([]byte("engine b"))
	ka := cacheKey(a + "  engine.wasm.gz\n")
	if len(ka) != 16 {
		t.Fatalf("key %q is not 16 characters", ka)
	}
	if _, err := hex.DecodeString(ka); err != nil {
		t.Fatalf("key %q is not hex: %v", ka, err)
	}
	if got := cacheKey(strings.ToUpper(a)); got != ka {
		t.Errorf("key of the same checksum in upper case = %q, want %q", got, ka)
	}
	if kb := cacheKey(b); kb == ka {
		t.Errorf("two engines share the key %q", ka)
	}
}

// emptyModule is the smallest valid WebAssembly module.
var emptyModule = []byte("\x00asm\x01\x00\x00\x00")

// cacheTree is a fake user cache folder holding a compilation cache root
// with the current build's directory, the caches of other builds, the
// layout of older releases, a cache being written by another process and
// symbolic links to files outside the cache.
type cacheTree struct {
	root, key       string
	gone, kept      []string
	outsideFile     string
	outsideLinkDest string
}

func newCacheTree(t *testing.T) *cacheTree {
	t.Helper()
	base := t.TempDir()
	c := &cacheTree{root: filepath.Join(base, "why-hwp", "wazero"), key: cacheKey(sumOf([]byte("now")))}
	now := time.Now()
	old := now.Add(-staleTempAge - time.Minute)
	write := func(rel string, mtime time.Time) string {
		p := filepath.Join(c.root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("entry"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	otherKey := cacheKey(sumOf([]byte("older engine")))
	c.gone = []string{
		write(filepath.Join(otherKey, "wazero-v1.12.0-amd64-linux", "aaaa"), old),
		write(filepath.Join(otherKey, "wazero-v1.12.0-amd64-linux", "aaaa.1.tmp"), old),
		write(filepath.Join("wazero-v1.12.0-amd64-linux", "bbbb"), old),
		write(filepath.Join("wazero-v0.0.0-amd64-linux", "cccc"), old),
		write("stray", old),
	}
	c.gone = append(c.gone,
		filepath.Join(c.root, otherKey),
		filepath.Join(c.root, "wazero-v1.12.0-amd64-linux"),
		filepath.Join(c.root, "wazero-v0.0.0-amd64-linux"))
	c.kept = []string{
		write(filepath.Join(c.key, "wazero-v1.12.0-amd64-linux", "dddd"), old),
		write(filepath.Join("busy", "wazero-v1.13.0-amd64-linux", "eeee.2.tmp"), now),
		write(filepath.Join("wazero-v1.11.0-amd64-linux", "ffff.3.tmp"), now),
	}

	outside := filepath.Join(base, "fonts")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	c.outsideFile = filepath.Join(outside, "font.ttf")
	if err := os.WriteFile(c.outsideFile, []byte("font"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(c.root, "link")
	inner := filepath.Join(c.root, otherKey, "wazero-v1.12.0-amd64-linux", "link")
	if os.Symlink(outside, link) == nil && os.Symlink(outside, inner) == nil {
		c.outsideLinkDest = outside
		c.gone = append(c.gone, link, inner)
	}
	return c
}

func (c *cacheTree) check(t *testing.T, removed bool) {
	t.Helper()
	for _, p := range c.gone {
		_, err := os.Lstat(p)
		if exists := err == nil; exists == removed {
			t.Errorf("%s: exists %v, want %v", p, exists, !removed)
		}
	}
	for _, p := range append(c.kept, c.outsideFile) {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	if c.outsideLinkDest == "" {
		t.Log("symbolic links not supported here; link cases skipped")
	} else if fi, err := os.Lstat(c.outsideLinkDest); err != nil || !fi.IsDir() {
		t.Errorf("link target %s was removed: %v", c.outsideLinkDest, err)
	}
}

func TestRemoveOldCaches(t *testing.T) {
	c := newCacheTree(t)
	removeOldCaches(c.root, c.key, time.Now())
	c.check(t, true)
	removeOldCaches(filepath.Join(c.root, "missing"), c.key, time.Now())
}

func TestCompileCachedRemovesOldCaches(t *testing.T) {
	c := newCacheTree(t)
	e, err := compileCached(context.Background(), emptyModule, c.root, c.key)
	if err != nil {
		t.Fatal(err)
	}
	defer e.rt.Close(context.Background())
	if !hasCacheEntry(filepath.Join(c.root, c.key)) {
		t.Fatal("no cache entry was written for the current build")
	}
	c.check(t, true)
	ents, err := os.ReadDir(c.root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, ent := range ents {
		names = append(names, ent.Name())
	}
	want := []string{"busy", c.key, "wazero-v1.11.0-amd64-linux"}
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("cache root holds %v, want %v", names, want)
	}
}

func TestCompileCachedFailureRemovesNothing(t *testing.T) {
	c := newCacheTree(t)
	if _, err := compileCached(context.Background(), []byte("not wasm"), c.root, c.key); err == nil {
		t.Fatal("compiling garbage succeeded")
	}
	c.check(t, false)
}

func TestCompileCachedWithoutEntryRemovesNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the test cannot write to")
	}
	c := newCacheTree(t)
	dir := filepath.Join(c.root, c.key)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	c.kept = c.kept[1:]
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	e, err := compileCached(context.Background(), emptyModule, c.root, c.key)
	if err != nil {
		t.Fatal(err)
	}
	e.rt.Close(context.Background())
	if hasCacheEntry(dir) {
		t.Fatal("a cache entry was written into a read-only directory")
	}
	c.check(t, false)
}
