package svg2png

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func fakeFont(tag string) []byte {
	return append([]byte(tag), bytes.Repeat([]byte{byte(len(tag))}, 20)...)
}

func TestScanFonts(t *testing.T) {
	ttf, otf, woff := fakeFont("\x00\x01\x00\x00"), fakeFont("OTTO"), fakeFont("wOFF")
	b64 := base64.StdEncoding.EncodeToString
	continued := b64(otf)[:10] + "\\\n" + b64(otf)[10:]
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">
<defs><style id="a">
@font-face{font-family:'A';src:url("data:font/ttf;base64,` + b64(ttf) + `") format('truetype')}
@font-face{font-family:'B';src:url('data:application/font-sfnt;base64,` + continued + `')}
.x{background:url("data:image/png;base64,iVBORw0KGgo=")}
</style></defs>
<style><![CDATA[
@font-face{font-family:C;src:url(data:font/otf;base64,` + b64(otf) + `)}
@font-face{font-family:D;src:url(data:font/ttf;base64,!!!not-base64!!!)}
@font-face{font-family:E;src:url(data:font/woff;base64,` + b64(woff) + `)}
@font-face{font-family:F;src:url(fonts/f.ttf), local(Arial)}
]]></style>
<rect width="10" height="10"/></svg>`
	got, err := ScanFonts([]byte(svg))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{ttf, otf, otf}
	if len(got.Fonts) != len(want) {
		t.Fatalf("got %d fonts, want %d (skipped %q)", len(got.Fonts), len(want), got.Skipped)
	}
	for i := range want {
		if !bytes.Equal(got.Fonts[i], want[i]) {
			t.Errorf("font %d = % x, want % x", i, got.Fonts[i], want[i])
		}
	}
	if len(got.Skipped) != 2 || !strings.Contains(got.Skipped[0], "bad base64") || !strings.Contains(got.Skipped[1], "font/woff") {
		t.Errorf("skipped = %q, want bad base64 then font/woff", got.Skipped)
	}
	if got.HasText {
		t.Error("HasText is true for an SVG without <text>")
	}
}

func TestScanFontsText(t *testing.T) {
	got, err := ScanFonts([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><g><text x="1" y="1">a</text></g></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasText || len(got.Fonts) != 0 || len(got.Skipped) != 0 {
		t.Errorf("got %+v, want text and no fonts", got)
	}
	if _, err := ScanFonts([]byte(`<svg><style>`)); err == nil {
		t.Error("truncated SVG: want a parse error")
	}
}
