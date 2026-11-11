package svg2png

import (
	"math"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestParseLength(t *testing.T) {
	tests := []struct {
		in     string
		want   float64
		ok     bool
		hasErr bool
	}{
		{"100", 100, true, false},
		{" 100px ", 100, true, false},
		{"25.4mm", 96, true, false},
		{"2.54cm", 96, true, false},
		{"1in", 96, true, false},
		{"72pt", 96, true, false},
		{"6pc", 96, true, false},
		{"", 0, false, false},
		{"100%", 0, false, false},
		{"10em", 0, false, true},
		{"abc", 0, false, true},
		{"-5", 0, false, true},
		{"0", 0, false, true},
	}
	for _, tc := range tests {
		got, ok, err := ParseLength(tc.in)
		if (err != nil) != tc.hasErr || ok != tc.ok || !near(got, tc.want) {
			t.Errorf("ParseLength(%q) = %v, %v, %v; want %v, %v, err=%v", tc.in, got, ok, err, tc.want, tc.ok, tc.hasErr)
		}
	}
}

func TestParseViewBox(t *testing.T) {
	w, h, ok, err := ParseViewBox("0,0  160 90")
	if err != nil || !ok || w != 160 || h != 90 {
		t.Errorf("got %v %v %v %v", w, h, ok, err)
	}
	if _, _, ok, err := ParseViewBox(""); ok || err != nil {
		t.Errorf("empty: ok=%v err=%v", ok, err)
	}
	for _, bad := range []string{"0 0 160", "0 0 0 90", "a b c d"} {
		if _, _, _, err := ParseViewBox(bad); err == nil {
			t.Errorf("ParseViewBox(%q) succeeded", bad)
		}
	}
}

func TestSize(t *testing.T) {
	tests := []struct {
		name   string
		svg    string
		w, h   float64
		errSub string
	}{
		{"mm", `<svg xmlns="http://www.w3.org/2000/svg" width="25.4mm" height="12.7mm"/>`, 96, 48, ""},
		{"pt", `<svg width="72pt" height="36pt" viewBox="0 0 10 10"/>`, 96, 48, ""},
		{"unitless", `<?xml version="1.0"?><!-- c --><svg width="300" height="200"/>`, 300, 200, ""},
		{"viewBox only", `<svg viewBox="0 0 640 480"/>`, 640, 480, ""},
		{"percent", `<svg width="100%" height="100%" viewBox="0 0 640 480"/>`, 640, 480, ""},
		{"width only", `<svg width="320" viewBox="0 0 640 480"/>`, 320, 240, ""},
		{"height only", `<svg height="240" viewBox="0 0 640 480"/>`, 320, 240, ""},
		{"nothing", `<svg/>`, 0, 0, "no usable"},
		{"not svg", `<html/>`, 0, 0, "not <svg>"},
		{"bad unit", `<svg width="3em" height="2em"/>`, 0, 0, "unsupported"},
		{"empty", ``, 0, 0, "no root"},
	}
	for _, tc := range tests {
		w, h, err := Size([]byte(tc.svg))
		if tc.errSub != "" {
			if err == nil || !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("%s: err = %v, want %q", tc.name, err, tc.errSub)
			}
			continue
		}
		if err != nil || !near(w, tc.w) || !near(h, tc.h) {
			t.Errorf("%s: got %v x %v, %v; want %v x %v", tc.name, w, h, err, tc.w, tc.h)
		}
	}
}

func TestTargetSize(t *testing.T) {
	// A 160 mm x 90 mm drawing.
	sw, sh := 160*96/25.4, 90*96/25.4
	tests := []struct {
		name    string
		mm      float64
		px      int
		dpi     float64
		w, h    int
		wantErr bool
	}{
		{"160mm at 600dpi", 160, 0, 600, 3780, 2126, false},
		{"80mm at 300dpi", 80, 0, 300, 945, 532, false},
		{"pixels win", 0, 1000, 600, 1000, 563, false},
		{"intrinsic at 96dpi", 0, 0, 96, 605, 340, false},
		{"intrinsic at 600dpi", 0, 0, 600, 3780, 2126, false},
		{"too wide", 2000, 0, 600, 0, 0, true},
		{"too many pixels", 0, 16000, 600, 0, 0, true},
		{"largest allowed", 0, 14000, 600, 14000, 7875, false},
		{"bad dpi", 160, 0, 0, 0, 0, true},
	}
	for _, tc := range tests {
		w, h, err := TargetSize(sw, sh, tc.mm, tc.px, tc.dpi)
		if (err != nil) != tc.wantErr || w != tc.w || h != tc.h {
			t.Errorf("%s: got %dx%d, %v; want %dx%d err=%v", tc.name, w, h, err, tc.w, tc.h, tc.wantErr)
		}
	}
}
