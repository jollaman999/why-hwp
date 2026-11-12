package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordRunner stands in for the engine in Convert: it answers font-plan
// with an empty plan, writes a placeholder for convert and records every
// command with its guest mount points.
type recordRunner struct {
	cmds   []string
	guests [][]string
}

func (r *recordRunner) run(_ context.Context, args []string, mounts []mount) ([]byte, error) {
	r.cmds = append(r.cmds, args[0])
	var guests []string
	for _, m := range mounts {
		guests = append(guests, m.guest)
	}
	r.guests = append(r.guests, guests)
	switch args[0] {
	case "font-plan":
		return []byte("[]"), nil
	case "convert":
		for _, m := range mounts {
			if m.guest == "/out" {
				return nil, os.WriteFile(filepath.Join(m.host, "output."+args[2]), []byte("out"), 0o644)
			}
		}
	}
	return nil, nil
}

func TestConvertFontsOnlyForPDF(t *testing.T) {
	old := loadRunner
	t.Cleanup(func() { loadRunner = old })
	t.Setenv(EnvOffline, "")
	t.Setenv(EnvSystemFonts, "0")
	t.Setenv(EnvFontDirs, t.TempDir())

	dir := t.TempDir()
	src := filepath.Join(dir, "a.hwp")
	if err := os.WriteFile(src, []byte("doc"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		to         string
		wantCmds   string
		wantGuests string
	}{
		{"hwpx", "convert", "/in /out"},
		{"hwp", "convert", "/in /out"},
		{"txt", "convert", "/in /out"},
		{"pdf", "font-plan convert", "/in /out /fonts /hostfonts-0"},
	}
	for _, tt := range tests {
		r := &recordRunner{}
		loadRunner = func(context.Context) (runner, error) { return r, nil }
		out := filepath.Join(dir, "out."+tt.to)
		if err := Convert(context.Background(), src, out, tt.to); err != nil {
			t.Fatalf("to %s: %v", tt.to, err)
		}
		if got := strings.Join(r.cmds, " "); got != tt.wantCmds {
			t.Errorf("to %s: engine commands %q, want %q", tt.to, got, tt.wantCmds)
		}
		if got := strings.Join(r.guests[len(r.guests)-1], " "); got != tt.wantGuests {
			t.Errorf("to %s: convert mounts %q, want %q", tt.to, got, tt.wantGuests)
		}
		if b, err := os.ReadFile(out); err != nil || string(b) != "out" {
			t.Errorf("to %s: output %q, %v", tt.to, b, err)
		}
	}
}

// slowRunner stands in for the engine in Convert: it answers font-plan with
// an empty plan, and for convert it reports its /out host directory on
// started, waits for release, writes a placeholder and closes finished.
type slowRunner struct {
	started  chan string
	release  chan struct{}
	finished chan struct{}
}

func (r *slowRunner) run(_ context.Context, args []string, mounts []mount) ([]byte, error) {
	if args[0] == "font-plan" {
		return []byte("[]"), nil
	}
	defer close(r.finished)
	var outDir string
	for _, m := range mounts {
		if m.guest == "/out" {
			outDir = m.host
		}
	}
	r.started <- outDir
	<-r.release
	return nil, os.WriteFile(filepath.Join(outDir, "output."+args[2]), []byte("late"), 0o644)
}

// TestConvertCtxDoneFirst checks that a Convert whose ctx ends while the
// engine runs returns at once, writes nothing and removes its work
// directory, that the run left behind ends without a warning, and that it
// does not hold up the next Convert.
func TestConvertCtxDoneFirst(t *testing.T) {
	old := loadRunner
	t.Cleanup(func() { loadRunner = old })
	t.Setenv(EnvOffline, "")
	t.Setenv(EnvSystemFonts, "0")
	t.Setenv(EnvFontDirs, t.TempDir())

	dir := t.TempDir()
	src := filepath.Join(dir, "a.hwp")
	if err := os.WriteFile(src, []byte("doc"), 0o644); err != nil {
		t.Fatal(err)
	}
	slow := &slowRunner{started: make(chan string), release: make(chan struct{}), finished: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-slow.release:
		default:
			close(slow.release)
		}
	})
	loadRunner = func(context.Context) (runner, error) { return slow, nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := filepath.Join(dir, "slow.pdf")
	errc := make(chan error, 1)
	go func() { errc <- Convert(ctx, src, out, "pdf") }()
	var outDir string
	select {
	case outDir = <-slow.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the engine run did not start")
	}
	work := filepath.Dir(outDir)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "engine convert") {
			t.Errorf("err = %v, want engine convert: %v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Convert waited for the engine run after its ctx ended")
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Errorf("work directory %s kept after Convert returned: %v", work, err)
	}
	oldWarn := Warn
	t.Cleanup(func() { Warn = oldWarn })
	Warn = func(msg string) { t.Errorf("warning from the run left behind: %s", msg) }

	r := &recordRunner{}
	loadRunner = func(context.Context) (runner, error) { return r, nil }
	next := filepath.Join(dir, "next.pdf")
	nextc := make(chan error, 1)
	go func() { nextc <- Convert(context.Background(), src, next, "pdf") }()
	select {
	case err := <-nextc:
		if err != nil {
			t.Errorf("next Convert: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("next Convert is held up by the run left behind")
	}
	if b, err := os.ReadFile(next); err != nil || string(b) != "out" {
		t.Errorf("next output %q, %v", b, err)
	}

	close(slow.release)
	<-slow.finished
	detached.Wait()
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Errorf("work directory %s left after the engine run ended: %v", work, err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output written after Convert returned: %v", err)
	}
}

// TestDetachDoneCtx checks that a ctx done before detach starts neither job
// nor keep and still cleans up.
func TestDetachDoneCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ran, kept, cleaned bool
	_, err := detach(ctx, "x", func() (int, error) { ran = true; return 1, nil },
		func(int) error { kept = true; return nil }, func() { cleaned = true })
	if !errors.Is(err, context.Canceled) || ran || kept || !cleaned {
		t.Errorf("err %v, ran %v, kept %v, cleaned %v", err, ran, kept, cleaned)
	}
}

// TestRemoveStaleWork checks that only work directories unmodified for more
// than staleWorkAge are deleted, and that symbolic links, files and other
// names are left alone.
func TestRemoveStaleWork(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()
	old := now.Add(-staleWorkAge - time.Minute)
	young := now.Add(-staleWorkAge + time.Minute)
	target := t.TempDir()
	keepFile := filepath.Join(target, "keep")
	if err := os.WriteFile(keepFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mk := func(name string, mtime time.Time) string {
		p := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Join(p, "out"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	gone := mk(workDirPrefix+"old", old)
	kept := []string{mk(workDirPrefix+"young", young), mk("other-old", old)}
	file := filepath.Join(tmp, workDirPrefix+"file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	kept = append(kept, file)
	link := filepath.Join(tmp, workDirPrefix+"link")
	if err := os.Symlink(target, link); err == nil {
		kept = append(kept, link)
	}

	removeStaleWork(tmp, now)
	if _, err := os.Lstat(gone); !os.IsNotExist(err) {
		t.Errorf("%s not removed: %v", gone, err)
	}
	for _, p := range kept {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s removed: %v", p, err)
		}
	}
	if _, err := os.Stat(keepFile); err != nil {
		t.Errorf("file behind a symbolic link removed: %v", err)
	}
	removeStaleWork(filepath.Join(tmp, "missing"), now)
}
