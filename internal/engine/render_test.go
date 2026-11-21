package engine

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseRendered(t *testing.T) {
	dir := filepath.Join("work", "out")
	r, err := parseRendered([]byte(`{"pages":12,"images":[{"page":3,"width":1240,"height":1754},{"page":12,"width":10,"height":20}]}`+"\n"), dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []PageImage{
		{Page: 3, Width: 1240, Height: 1754, Path: filepath.Join(dir, "3.png")},
		{Page: 12, Width: 10, Height: 20, Path: filepath.Join(dir, "12.png")},
	}
	if r.count != 12 || !reflect.DeepEqual(r.images, want) {
		t.Errorf("got %d pages, %+v; want 12, %+v", r.count, r.images, want)
	}
	for _, bad := range []string{
		`not json`,
		`{"pages":2,"images":[{"page":3,"width":1,"height":1}]}`,
		`{"pages":2,"images":[{"page":0,"width":1,"height":1}]}`,
	} {
		if _, err := parseRendered([]byte(bad), dir); err == nil {
			t.Errorf("parseRendered(%s) succeeded", bad)
		}
	}
}

// Render asks the engine for the pages given, with the fonts mounted as for
// pdf output, and hands keep the images while they still exist.
func TestRenderArgs(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(EnvOffline, "1")
	t.Setenv(EnvSystemFonts, "0")
	t.Setenv(EnvFontDirs, t.TempDir())
	rec := &renderRunner{}
	old := loadRunner
	loadRunner = func(context.Context) (runner, error) { return rec, nil }
	defer func() { loadRunner = old }()

	in := filepath.Join(t.TempDir(), "doc.HWP")
	if err := os.WriteFile(in, []byte("doc"), 0o644); err != nil {
		t.Fatal(err)
	}
	var kept []string
	err := Render(context.Background(), in, 150, []int{2, 5}, func(count int, images []PageImage) error {
		if count != 7 {
			t.Errorf("count = %d, want 7", count)
		}
		for _, img := range images {
			b, err := os.ReadFile(img.Path)
			if err != nil {
				return err
			}
			kept = append(kept, string(b))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"png 2", "png 5"}; !reflect.DeepEqual(kept, want) {
		t.Errorf("kept %q, want %q", kept, want)
	}
	if want := "render --in /in/input.hwp --out /out --dpi 150 --page 2 --page 5"; rec.render != want {
		t.Errorf("engine args %q, want %q", rec.render, want)
	}
	if !rec.fonts {
		t.Error("render ran without the font cache mounted")
	}
	if err := Render(context.Background(), in, 0, nil, nil); err == nil {
		t.Error("dpi 0 accepted")
	}
}

// renderRunner plays the engine: an empty font plan, and for render a PNG
// stand-in per page and a report of a 7-page document.
type renderRunner struct {
	render string
	fonts  bool
}

func (r *renderRunner) run(_ context.Context, args []string, mounts []mount) ([]byte, error) {
	if args[0] == "font-plan" {
		return []byte("[]"), nil
	}
	r.render = strings.Join(args, " ")
	var out string
	for _, m := range mounts {
		switch m.guest {
		case "/out":
			out = m.host
		case "/fonts":
			r.fonts = true
		}
	}
	for _, p := range []string{"2", "5"} {
		if err := os.WriteFile(filepath.Join(out, p+".png"), []byte("png "+p), 0o644); err != nil {
			return nil, err
		}
	}
	return []byte(`{"pages":7,"images":[{"page":2,"width":1,"height":1},{"page":5,"width":1,"height":1}]}`), nil
}
