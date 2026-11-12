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
