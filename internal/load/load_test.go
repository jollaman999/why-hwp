package load

import (
	"errors"
	"testing"

	"github.com/jollaman999/why-hwp/internal/model"
)

func TestParseText(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"\n", []string{""}},
		{"a\nb", []string{"a", "b"}},
		{"\xef\xbb\xbfa\r\n\r\nb\r\n", []string{"a", "", "b"}},
	} {
		doc := ParseText([]byte(tc.in), "md")
		if len(doc.Blocks) != len(tc.want) || doc.Summary.Paragraphs != len(tc.want) {
			t.Errorf("%q: blocks %+v, want %q", tc.in, doc.Blocks, tc.want)
			continue
		}
		for i, w := range tc.want {
			want := model.Block{Loc: "l" + string(rune('1'+i)), Kind: model.KindPara, Text: w}
			if doc.Blocks[i] != want {
				t.Errorf("%q: block %d = %+v, want %+v", tc.in, i, doc.Blocks[i], want)
			}
		}
	}
}

func TestUnsupported(t *testing.T) {
	for _, name := range []string{"x.pdf", "x.HWPX.bak"} {
		var ue *UnsupportedError
		if _, err := Any(name); !errors.As(err, &ue) {
			t.Errorf("%s: err = %v, want *UnsupportedError", name, err)
		}
	}
	if _, err := Document("x.md"); err == nil {
		t.Error("Document read an md file")
	}
	if !IsText("NOTES.MD") || IsText("a.hwpx") {
		t.Error("IsText")
	}
}
