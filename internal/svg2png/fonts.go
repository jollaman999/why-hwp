package svg2png

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// FontScan is what ScanFonts found in an SVG document.
type FontScan struct {
	// Fonts are the TrueType or OpenType files from data: URIs in
	// @font-face rules, in document order.
	Fonts [][]byte
	// Skipped explains every data: URI font that could not be used.
	Skipped []string
	// HasText is true when the document has a <text> element.
	HasText bool
}

var (
	fontFaceRule = regexp.MustCompile(`(?is)@font-face\s*\{[^}]*\}`)
	urlFunc      = regexp.MustCompile(`(?is)url\(\s*("[^"]*"|'[^']*'|[^)'"]*)\s*\)`)
)

// ScanFonts collects the fonts embedded as data: URIs in the @font-face
// rules of the document's <style> elements. Fonts referenced any other way
// (system fonts, files, web addresses) are not looked up.
func ScanFonts(svg []byte) (FontScan, error) {
	var res FontScan
	d := xml.NewDecoder(bytes.NewReader(svg))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	var css strings.Builder
	inStyle := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return FontScan{}, fmt.Errorf("parse SVG: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "style":
				inStyle++
			case "text":
				res.HasText = true
			}
		case xml.EndElement:
			if t.Name.Local == "style" && inStyle > 0 {
				inStyle--
				css.WriteString("\n")
			}
		case xml.CharData:
			if inStyle > 0 {
				css.Write(t)
			}
		}
	}
	for _, rule := range fontFaceRule.FindAllString(css.String(), -1) {
		for _, m := range urlFunc.FindAllStringSubmatch(rule, -1) {
			ref := cssString(m[1])
			if !strings.HasPrefix(strings.ToLower(ref), "data:") {
				continue
			}
			font, err := decodeDataURI(ref)
			if err != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("font %d: %v", len(res.Fonts)+len(res.Skipped)+1, err))
				continue
			}
			res.Fonts = append(res.Fonts, font)
		}
	}
	return res, nil
}

// cssString removes the quotes and line continuations (a backslash before a
// line break) from a url() argument.
func cssString(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	s = strings.ReplaceAll(s, "\\\r\n", "")
	s = strings.ReplaceAll(s, "\\\n", "")
	s = strings.ReplaceAll(s, "\\\r", "")
	return strings.TrimSpace(s)
}

// decodeDataURI returns the payload of a data: URI holding a TrueType or
// OpenType font.
func decodeDataURI(ref string) ([]byte, error) {
	comma := strings.IndexByte(ref, ',')
	if comma < 0 {
		return nil, errors.New("data: URI has no comma")
	}
	meta, payload := strings.ToLower(ref[len("data:"):comma]), ref[comma+1:]
	var data []byte
	if strings.HasSuffix(meta, ";base64") {
		clean := strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
				return -1
			}
			return r
		}, payload)
		var err error
		data, err = base64.StdEncoding.DecodeString(clean)
		if err != nil {
			if data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "=")); err != nil {
				return nil, fmt.Errorf("bad base64: %v", err)
			}
		}
	} else {
		s, err := url.PathUnescape(payload)
		if err != nil {
			return nil, fmt.Errorf("bad data: URI: %v", err)
		}
		data = []byte(s)
	}
	if !isSFNT(data) {
		mime := strings.TrimSuffix(meta, ";base64")
		if mime == "" {
			mime = "no media type"
		}
		return nil, fmt.Errorf("not a TrueType or OpenType font (%s)", mime)
	}
	return data, nil
}

// isSFNT reports whether data starts like a TrueType, OpenType or TrueType
// collection file. WOFF and WOFF2 are not read by the renderer.
func isSFNT(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	switch string(data[:4]) {
	case "\x00\x01\x00\x00", "OTTO", "true", "ttcf":
		return true
	}
	return false
}
