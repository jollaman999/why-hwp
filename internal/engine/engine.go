// Package engine runs the embedded why-hwp-engine WebAssembly module (rhwp
// and resvg built for wasm32-wasip1) with wazero, so conversion and SVG
// rendering need no external program.
package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// engineGz is the gzip-compressed engine module.
//
//go:embed engine.wasm.gz
var engineGz []byte

// engineSum is the SHA-256 of engineGz, the compressed file as committed,
// in sha256sum format ("<hex>  engine.wasm.gz"); only the first field is
// read. The decompressed module is covered by the gzip CRC-32.
//
//go:embed engine.wasm.gz.sha256
var engineSum string

// Exit codes of the engine module.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// engine is a compiled module ready to be instantiated any number of
// times, concurrently.
type engine struct {
	rt  wazero.Runtime
	mod wazero.CompiledModule
}

// userCacheDir is the root of the compilation and font caches. Tests point
// it at a temporary directory.
var userCacheDir = os.UserCacheDir

// loader compiles the engine once, in the background, so that a caller
// whose ctx ends first can return while the compilation goes on and fills
// the compilation cache for the next caller.
type loader struct {
	compile func() (*engine, error)
	once    sync.Once
	done    chan struct{}
	e       *engine
	err     error
}

func newLoader(compile func() (*engine, error)) *loader {
	return &loader{compile: compile, done: make(chan struct{})}
}

// get starts the compilation on first use and waits for it or for ctx,
// whichever comes first. Every caller gets the result of the same single
// compilation.
func (l *loader) get(ctx context.Context) (*engine, error) {
	l.once.Do(func() {
		go func() {
			defer close(l.done)
			l.e, l.err = l.compile()
		}()
	})
	select {
	case <-l.done:
		return l.e, l.err
	case <-ctx.Done():
		select {
		case <-l.done:
			return l.e, l.err
		default:
		}
		return nil, fmt.Errorf("engine: compile: %w", ctx.Err())
	}
}

// compileEmbedded unpacks and compiles the embedded module.
func compileEmbedded() (*engine, error) {
	wasm, err := unpack(engineGz, engineSum)
	if err != nil {
		return nil, err
	}
	dir := compileCacheDir()
	if dir != "" {
		removeStaleTemp(dir, time.Now())
	}
	return newEngine(context.Background(), wasm, dir)
}

var engineLoader = newLoader(compileEmbedded)

// get returns the process-wide engine, compiling it on first use. A done
// ctx returns ctx's error without waiting for the compilation to finish.
func get(ctx context.Context) (*engine, error) {
	return engineLoader.get(ctx)
}

// unpack checks gz against the sha256sum line sum and decompresses it.
func unpack(gz []byte, sum string) ([]byte, error) {
	if len(gz) == 0 {
		return nil, errors.New("engine: embedded engine.wasm.gz is empty")
	}
	if err := verifySHA256(gz, sum); err != nil {
		return nil, fmt.Errorf("engine: engine.wasm.gz: %w", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("engine: engine.wasm.gz: %w", err)
	}
	wasm, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("engine: engine.wasm.gz: %w", err)
	}
	return wasm, nil
}

// verifySHA256 compares the SHA-256 of data with the first field of sum.
func verifySHA256(data []byte, sum string) error {
	fields := strings.Fields(sum)
	if len(fields) == 0 {
		return errors.New("checksum file is empty")
	}
	want := strings.ToLower(fields[0])
	if _, err := hex.DecodeString(want); err != nil || len(want) != 2*sha256.Size {
		return fmt.Errorf("checksum %q is not a SHA-256 hex digest", fields[0])
	}
	h := sha256.Sum256(data)
	if got := hex.EncodeToString(h[:]); got != want {
		return fmt.Errorf("SHA-256 %s does not match checksum %s", got, want)
	}
	return nil
}

// compileCacheDir returns the wazero compilation cache directory, or "" when
// it cannot be created, in which case the module is compiled without a cache.
func compileCacheDir() string {
	base, err := userCacheDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(base, "why-hwp", "wazero")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// staleTempAge is how old a "*.tmp" file in the compilation cache must be
// before removeStaleTemp deletes it. Younger ones may still be written by
// another process.
const staleTempAge = time.Hour

// removeStaleTemp deletes "*.tmp" regular files older than staleTempAge in
// dir and in its direct subdirectories (wazero writes its cache entries to
// dir/wazero-<version>-<arch>-<os>/ through a temporary file and a rename,
// and a process that exits mid-write leaves the temporary file behind).
// Errors are ignored.
func removeStaleTemp(dir string, now time.Time) {
	dirs := []string{dir}
	ents, _ := os.ReadDir(dir)
	for _, ent := range ents {
		if ent.IsDir() {
			dirs = append(dirs, filepath.Join(dir, ent.Name()))
		}
	}
	for _, d := range dirs {
		ents, _ := os.ReadDir(d)
		for _, ent := range ents {
			if !ent.Type().IsRegular() || !strings.HasSuffix(ent.Name(), ".tmp") {
				continue
			}
			info, err := ent.Info()
			if err != nil || now.Sub(info.ModTime()) <= staleTempAge {
				continue
			}
			os.Remove(filepath.Join(d, ent.Name()))
		}
	}
}

// newEngine compiles wasm once. cacheDir "" disables the compilation cache.
func newEngine(ctx context.Context, wasm []byte, cacheDir string) (*engine, error) {
	cfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	if cacheDir != "" {
		if cache, err := wazero.NewCompilationCacheWithDir(cacheDir); err == nil {
			cfg = cfg.WithCompilationCache(cache)
		}
	}
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("engine: wasi: %w", err)
	}
	mod, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("engine: compile: %w", err)
	}
	return &engine{rt: rt, mod: mod}, nil
}

// mount maps a host directory to a guest path. Mounts with fontPath set are
// passed to rhwp as font directories.
type mount struct {
	host, guest string
	readOnly    bool
	fontPath    bool
}

// run executes the module with args (the subcommand and its arguments) and
// returns its stdout. Exit code 1 and 2 become errors carrying the last
// stderr line; a done ctx stops the module and returns ctx.Err().
func (e *engine) run(ctx context.Context, args []string, mounts []mount) ([]byte, error) {
	fs := wazero.NewFSConfig()
	var fontPath []string
	for _, m := range mounts {
		if m.fontPath {
			fontPath = append(fontPath, m.guest)
		}
		if m.readOnly {
			fs = fs.WithReadOnlyDirMount(m.host, m.guest)
		} else {
			fs = fs.WithDirMount(m.host, m.guest)
		}
	}
	var stdout, stderr bytes.Buffer
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithArgs(append([]string{"why-hwp-engine"}, args...)...).
		WithEnv("HOME", "/home").
		WithEnv("RHWP_WEBFONT_OFFLINE", "1").
		WithEnv("WHY_HWP_TZ_OFFSET_MIN", strconv.Itoa(tzOffsetMin(time.Now()))).
		WithFSConfig(fs).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep().
		WithRandSource(rand.Reader)

	if len(fontPath) > 0 {
		cfg = cfg.WithEnv(hostFontEnv, strings.Join(fontPath, ":"))
	}

	mod, err := e.rt.InstantiateModule(ctx, e.mod, cfg)
	if mod != nil {
		mod.Close(context.Background())
	}
	sub := "module"
	if len(args) > 0 {
		sub = args[0]
	}
	if err == nil {
		return stdout.Bytes(), nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("engine %s: %w", sub, ctx.Err())
	}
	msg := lastLine(stderr.String())
	var exit *sys.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case exitOK:
			return stdout.Bytes(), nil
		case exitFail:
			if msg == "" {
				msg = "failed"
			}
			return nil, fmt.Errorf("engine %s: %s", sub, msg)
		case exitUsage:
			return nil, fmt.Errorf("engine %s: usage error: %s", sub, msg)
		}
		return nil, fmt.Errorf("engine %s: exit code %d: %s", sub, exit.ExitCode(), msg)
	}
	if msg != "" {
		return nil, fmt.Errorf("engine %s: %v: %s", sub, err, msg)
	}
	return nil, fmt.Errorf("engine %s: %w", sub, err)
}

// lastLine returns the last non-blank line of s, trimmed.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// tzOffsetMin returns the UTC offset of t's zone in minutes (KST is 540).
func tzOffsetMin(t time.Time) int {
	_, off := t.Zone()
	return off / 60
}
