// Package svg2png renders SVG files to PNG with a headless browser, so that
// fonts (system fonts and @font-face web fonts embedded in the SVG) are drawn
// exactly as a browser draws them.
package svg2png

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// CSSPixelsPerInch is the CSS reference resolution. SVG user units and px
// lengths are CSS pixels.
const CSSPixelsPerInch = 96.0

// ParseLength converts an SVG length attribute to CSS pixels. Supported units
// are none, px, mm, cm, in, pt and pc. ok is false when the value is empty or
// a percentage, which means the caller has to fall back to the viewBox.
func ParseLength(s string) (px float64, ok bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, "%") {
		return 0, false, nil
	}
	num, unit := s, ""
	for i := len(s); i > 0; i-- {
		c := s[i-1]
		if (c >= '0' && c <= '9') || c == '.' {
			num, unit = s[:i], strings.ToLower(strings.TrimSpace(s[i:]))
			break
		}
	}
	v, perr := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if perr != nil {
		return 0, false, fmt.Errorf("invalid length %q", s)
	}
	var mul float64
	switch unit {
	case "", "px":
		mul = 1
	case "mm":
		mul = CSSPixelsPerInch / 25.4
	case "cm":
		mul = CSSPixelsPerInch / 2.54
	case "in":
		mul = CSSPixelsPerInch
	case "pt":
		mul = CSSPixelsPerInch / 72
	case "pc":
		mul = CSSPixelsPerInch / 6
	default:
		return 0, false, fmt.Errorf("unsupported length unit in %q", s)
	}
	if v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false, fmt.Errorf("length %q must be positive", s)
	}
	return v * mul, true, nil
}

// ParseViewBox parses "min-x min-y width height" and returns width and height.
func ParseViewBox(s string) (w, h float64, ok bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false, nil
	}
	f := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(f) != 4 {
		return 0, 0, false, fmt.Errorf("invalid viewBox %q", s)
	}
	var v [4]float64
	for i, x := range f {
		n, perr := strconv.ParseFloat(x, 64)
		if perr != nil {
			return 0, 0, false, fmt.Errorf("invalid viewBox %q", s)
		}
		v[i] = n
	}
	if v[2] <= 0 || v[3] <= 0 {
		return 0, 0, false, fmt.Errorf("viewBox %q must have a positive size", s)
	}
	return v[2], v[3], true, nil
}

// Size returns the intrinsic size of an SVG document in CSS pixels, taken
// from the root width/height attributes and, where those are missing or
// relative, from the viewBox.
func Size(data []byte) (w, h float64, err error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	var root *xml.StartElement
	for root == nil {
		tok, terr := d.Token()
		if terr == io.EOF {
			return 0, 0, errors.New("no root element")
		}
		if terr != nil {
			return 0, 0, fmt.Errorf("parse SVG: %w", terr)
		}
		if se, ok := tok.(xml.StartElement); ok {
			root = &se
		}
	}
	if root.Name.Local != "svg" {
		return 0, 0, fmt.Errorf("root element is <%s>, not <svg>", root.Name.Local)
	}
	var ws, hs, vbs string
	for _, a := range root.Attr {
		if a.Name.Space != "" && a.Name.Space != "http://www.w3.org/2000/svg" {
			continue
		}
		switch a.Name.Local {
		case "width":
			ws = a.Value
		case "height":
			hs = a.Value
		case "viewBox":
			vbs = a.Value
		}
	}
	w, wok, err := ParseLength(ws)
	if err != nil {
		return 0, 0, fmt.Errorf("width: %w", err)
	}
	h, hok, err := ParseLength(hs)
	if err != nil {
		return 0, 0, fmt.Errorf("height: %w", err)
	}
	if wok && hok {
		return w, h, nil
	}
	vw, vh, vok, err := ParseViewBox(vbs)
	if err != nil {
		return 0, 0, err
	}
	switch {
	case wok && vok:
		return w, w * vh / vw, nil
	case hok && vok:
		return h * vw / vh, h, nil
	case vok:
		return vw, vh, nil
	}
	return 0, 0, errors.New("SVG has no usable width/height and no viewBox")
}

// TargetSize computes the output size in device pixels. widthPx wins over
// widthMM; when both are zero the SVG's intrinsic width (CSS px, 96 dpi) is
// used as the physical width. The height follows the SVG aspect ratio.
func TargetSize(svgW, svgH, widthMM float64, widthPx int, dpi float64) (w, h int, err error) {
	if svgW <= 0 || svgH <= 0 {
		return 0, 0, errors.New("SVG size must be positive")
	}
	if dpi <= 0 {
		return 0, 0, errors.New("dpi must be positive")
	}
	var fw float64
	switch {
	case widthPx > 0:
		fw = float64(widthPx)
	case widthMM > 0:
		fw = widthMM / 25.4 * dpi
	default:
		fw = svgW / CSSPixelsPerInch * dpi
	}
	w = int(math.Round(fw))
	h = int(math.Round(float64(w) * svgH / svgW))
	if w < 1 || h < 1 {
		return 0, 0, fmt.Errorf("output size %dx%d is too small", w, h)
	}
	if w > MaxSide || h > MaxSide {
		return 0, 0, fmt.Errorf("output size %dx%d exceeds %d px per side", w, h, MaxSide)
	}
	if w*h > MaxArea {
		return 0, 0, fmt.Errorf("output size %dx%d exceeds %d pixels in total", w, h, MaxArea)
	}
	return w, h, nil
}

// Size limits. Headless Chrome left the bottom of a 16384x9216 render
// unpainted while still returning a screenshot of the full size, so the
// output is capped below the largest sizes that rendered completely
// (20000 px per side, about 130 million pixels in total).
const (
	MaxSide = 20000
	MaxArea = 120_000_000
)
