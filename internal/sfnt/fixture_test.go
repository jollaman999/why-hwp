package sfnt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixturePath is a subset of an SIL Open Font License font made by
// TestMakeFixture. See testdata/fonts/README.md.
var fixturePath = filepath.Join("..", "..", "testdata", "fonts", "sans-subset.ttf")

// fixtureText is what the fixture holds: printable ASCII, accented letters
// that are composite glyphs, and one character outside the BMP.
const fixtureText = " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~" +
	"\u00e9\u00e0\u00e7\u00f1\u00fc\u00c5\u00c4\u00f6\u00c9" + "\U00010B00"

// TestMakeFixture writes the fixture when WHY_HWP_MAKE_FIXTURE names the
// source font; otherwise it is skipped.
func TestMakeFixture(t *testing.T) {
	src := os.Getenv("WHY_HWP_MAKE_FIXTURE")
	if src == "" {
		t.Skip("set WHY_HWP_MAKE_FIXTURE to the source font to rebuild the fixture")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if lic := f.Name(13); !strings.Contains(lic, "SIL Open Font License") {
		t.Fatalf("source font licence (name ID 13) is %q, want the SIL Open Font License", lic)
	}
	res, err := f.Subset([]rune(fixtureText))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Missing) > 0 {
		t.Fatalf("source font lacks %q", string(res.Missing))
	}
	if err := os.WriteFile(fixturePath, res.Font, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d glyphs, %d bytes, licence %q", fixturePath, res.Glyphs, len(res.Font), f.Name(13))
}

func readFixture(t *testing.T) (*Font, []byte) {
	t.Helper()
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return f, data
}
