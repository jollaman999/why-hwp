package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jollaman999/why-hwp/internal/model"
)

const sampleHWPX = "../../testdata/sample.hwpx"

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
		{[]string{"text", "x.hwp"}, "hwp support not yet"},
		{[]string{"info", "x.hwp"}, "hwp support not yet"},
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
	var out, errOut bytes.Buffer
	if code := run([]string{"text", "no-such-file.hwpx"}, &out, &errOut); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}
