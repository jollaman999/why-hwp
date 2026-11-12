package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		loadRunner = func() (runner, error) { return r, nil }
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
