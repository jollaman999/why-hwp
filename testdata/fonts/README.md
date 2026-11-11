# Test fonts

`sans-subset.ttf` is a 111-glyph subset of "Sans Serif Collection" version 1.02, a Noto font that ships with Windows 11 (`C:\Windows\Fonts\SansSerifCollection.ttf`). Its name table (name ID 13) says it is licensed under the SIL Open Font License 1.1; the licence and the font's copyright line are in [OFL.txt](OFL.txt). Noto declares no Reserved Font Name, so the subset keeps the original names.

It holds printable ASCII, nine accented Latin letters that the source draws as composite glyphs (é à ç ñ ü Å Ä ö É), and U+10B00 AVESTAN LETTER A, which lies outside the BMP. It has no Hangul. The tests use it for the subset writer (outlines, composite closure, checksums, the format 12 cmap) and for measuring text in `svgcheck`.

The subset was made by `internal/sfnt` itself. To rebuild it on Windows:

```sh
WHY_HWP_MAKE_FIXTURE='C:\Windows\Fonts\SansSerifCollection.ttf' go test ./internal/sfnt -run TestMakeFixture -v
```

The test refuses a source font whose name ID 13 does not name the SIL Open Font License. Any other OFL TrueType font that has the characters above works the same way.
