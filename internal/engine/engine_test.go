package engine

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"strings"
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
