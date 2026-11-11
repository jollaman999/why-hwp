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

var (
	loadOnce sync.Once
	loaded   *engine
	loadErr  error
)

// get returns the process-wide engine, compiling it on first use.
func get() (*engine, error) {
	loadOnce.Do(func() {
		wasm, err := unpack(engineGz, engineSum)
		if err != nil {
			loadErr = err
			return
		}
		loaded, loadErr = newEngine(context.Background(), wasm, compileCacheDir())
	})
	return loaded, loadErr
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

// mount maps a host directory to a guest path.
type mount struct {
	host, guest string
	readOnly    bool
}

// run executes the module with args (the subcommand and its arguments) and
// returns its stdout. Exit code 1 and 2 become errors carrying the last
// stderr line; a done ctx stops the module and returns ctx.Err().
func (e *engine) run(ctx context.Context, args []string, mounts []mount) ([]byte, error) {
	fs := wazero.NewFSConfig()
	for _, m := range mounts {
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
