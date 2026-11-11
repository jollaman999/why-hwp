package main

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// charRef matches a numeric character reference. Hancom Office writes
// characters outside its code page as &#NNNN; in text output.
var charRef = regexp.MustCompile(`&#([0-9]{1,7});`)

func decodeCharRefs(s string) string {
	return charRef.ReplaceAllStringFunc(s, func(m string) string {
		n, err := strconv.Atoi(m[2 : len(m)-1])
		if err != nil || n <= 0 || n > 0x10ffff {
			return m
		}
		return string(rune(n))
	})
}

// txtResult is a line comparison of two text outputs. Blank lines and
// trailing spaces are ignored because the two converters lay out empty
// paragraphs and table cells differently, and numeric character references
// are decoded.
type txtResult struct {
	RefLines, CandLines int
	RefOnly, CandOnly   int
	FirstRefOnly        string
	FirstCandOnly       string
}

func (r txtResult) same() bool { return r.RefOnly == 0 && r.CandOnly == 0 }

func textLines(b []byte) []string {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	var out []string
	for _, l := range strings.Split(decodeCharRefs(string(b)), "\n") {
		l = strings.TrimRight(l, " \t\r\u00a0\u3000")
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func compareText(ref, cand []byte) txtResult {
	a, b := textLines(ref), textLines(cand)
	pairs := lcsPairs(a, b)
	r := txtResult{RefLines: len(a), CandLines: len(b), RefOnly: len(a) - len(pairs), CandOnly: len(b) - len(pairs)}
	inA := make([]bool, len(a))
	inB := make([]bool, len(b))
	for _, p := range pairs {
		inA[p[0]] = true
		inB[p[1]] = true
	}
	for i, ok := range inA {
		if !ok {
			r.FirstRefOnly = a[i]
			break
		}
	}
	for i, ok := range inB {
		if !ok {
			r.FirstCandOnly = b[i]
			break
		}
	}
	return r
}
