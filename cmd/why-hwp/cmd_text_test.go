package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jollaman999/why-hwp/internal/model"
)

const (
	sampleHWPX = "../../testdata/sample.hwpx"
	sampleHWP  = "../../testdata/sample.hwp"
)

func TestTextPlain(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"text", sampleHWPX}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errOut.String())
	}
	for _, want := range []string{
		"s0/p0\t샘플 사업 계획서\n",
		"s0/p4/t0/r1/c2\t(A)\n",
		"s0/p4/t0/r2/c2\t(A/B)\n",
		"s0/p7\t문서 구조를 프로그램이 읽을 수 있다 (가).\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "가짜 메모") {
		t.Errorf("memo body leaked into text:\n%s", out.String())
	}
}

func TestTextJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"text", "-format", "json", sampleHWPX}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errOut.String())
	}
	var got struct {
		Format string        `json:"format"`
		Blocks []model.Block `json:"blocks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Format != "hwpx" || len(got.Blocks) != 19 {
		t.Fatalf("format %q, %d blocks; want hwpx, 19", got.Format, len(got.Blocks))
	}
	if b := got.Blocks[5]; b != (model.Block{Loc: "s0/p4/t0/r0/c0", Kind: model.KindCell, Text: "항목"}) {
		t.Errorf("block 5 = %+v", b)
	}
}

func TestInfoJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"info", "-format", "json", sampleHWPX}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errOut.String())
	}
	var s model.Summary
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Sections != 1 || s.Paragraphs != 10 || len(s.Tables) != 1 || len(s.Memos) != 1 || len(s.Pictures) != 1 {
		t.Errorf("summary = %+v", s)
	}
	if len(s.OwnerMarks) != 3 {
		t.Errorf("owner marks = %+v", s.OwnerMarks)
	}
}

func TestInfoPlain(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"info", sampleHWPX}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errOut.String())
	}
	for _, want := range []string{
		"tables         1\n  s0/p4/t0\t3x3\n",
		"  (A/B)\t1\ts0/p4/t0/r2/c2\n",
		"memos          1\n",
		"pictures       1\n  s0/p9\timage1\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestTextRejects(t *testing.T) {
	for _, tc := range []struct {
		args []string
		msg  string
	}{
		{[]string{"text", "x.pdf"}, "unsupported file type"},
		{[]string{"text", "-format", "xml", sampleHWPX}, "unknown -format"},
		{[]string{"text"}, "Usage: why-hwp text"},
	} {
		var out, errOut bytes.Buffer
		if code := run(tc.args, &out, &errOut); code != 2 {
			t.Errorf("%v: exit code = %d, want 2", tc.args, code)
		}
		if !strings.Contains(errOut.String(), tc.msg) {
			t.Errorf("%v: stderr = %q, want %q", tc.args, errOut.String(), tc.msg)
		}
	}
}

func TestTextMissingFile(t *testing.T) {
	for _, name := range []string{"no-such-file.hwpx", "no-such-file.hwp"} {
		var out, errOut bytes.Buffer
		if code := run([]string{"text", name}, &out, &errOut); code != 1 {
			t.Errorf("%s: exit code = %d, want 1", name, code)
		}
	}
}

// sample.hwp and sample.hwpx are the same document saved in both formats.
func TestHWPMatchesHWPX(t *testing.T) {
	for _, args := range [][]string{
		{"text"},
		{"text", "-format", "json"},
	} {
		var outX, outB, errOut bytes.Buffer
		if code := run(append(args, sampleHWPX), &outX, &errOut); code != 0 {
			t.Fatalf("%v hwpx: exit code = %d (stderr %q)", args, code, errOut.String())
		}
		if code := run(append(args, sampleHWP), &outB, &errOut); code != 0 {
			t.Fatalf("%v hwp: exit code = %d (stderr %q)", args, code, errOut.String())
		}
		want := strings.Replace(outX.String(), `"format": "hwpx"`, `"format": "hwp"`, 1)
		if outB.String() != want {
			t.Errorf("%v: hwp output differs\n got:\n%s\nwant:\n%s", args, outB.String(), want)
		}
	}
}

func TestInfoHWP(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"info", sampleHWP}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errOut.String())
	}
	for _, want := range []string{
		"format         hwp\n",
		"tables         1\n  s0/p4/t0\t3x3\n",
		"  (A/B)\t1\ts0/p4/t0/r2/c2\n",
		"memos          1\n  s0/p7\t가짜 메모: 표현을 다듬을 것\n",
		"track changes  0\n",
		"para shapes    20\n",
		"styles         23\n",
		"pictures       1\n  s0/p9\t1\n",
		"bin data       1\n  1\tBinData/BIN0001.PNG\timage/png\t238 bytes\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestTextBadHWP(t *testing.T) {
	dir := t.TempDir()
	sample, err := os.ReadFile(sampleHWP)
	if err != nil {
		t.Fatal(err)
	}
	// Set the password bit (bit 1 of the FileHeader properties at offset 36).
	encrypted := bytes.Clone(sample)
	i := bytes.Index(encrypted, []byte("HWP Document File"))
	if i < 0 {
		t.Fatal("signature not found in sample")
	}
	encrypted[i+36] |= 2
	for _, tc := range []struct {
		file, data, msg string
	}{
		{"garbage.hwp", strings.Repeat("not a document ", 64), "not an HWP 5.0 document"},
		{"zip.hwp", "PK\x03\x04" + strings.Repeat("\x00", 512), "needs the .hwpx extension"},
		{"encrypted.hwp", string(encrypted), "document is encrypted"},
		{"truncated.hwp", string(sample[:len(sample)/2]), "why-hwp text: "},
	} {
		name := filepath.Join(dir, tc.file)
		if err := os.WriteFile(name, []byte(tc.data), 0o644); err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		if code := run([]string{"text", name}, &out, &errOut); code != 1 {
			t.Errorf("%s: exit code = %d, want 1", tc.file, code)
		}
		if !strings.Contains(errOut.String(), tc.msg) {
			t.Errorf("%s: stderr = %q, want %q", tc.file, errOut.String(), tc.msg)
		}
	}
}
