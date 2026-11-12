[한국어](README.ko.md)

# why-hwp

A single-binary command-line tool for working with Hangul word processor documents (HWP / HWPX) on Windows, Linux and macOS, without Python, Java or any other runtime.

> **Status: early development.** The commands below work and are tested on Windows and Linux, but options and the spec format may still change before 1.0.

## Why HWP

The name is a sigh. Anyone who has had to script a Hangul document has said it at least once: *why... HWP?* Nobody gets to choose. Reports, forms and submissions arrive as HWP and have to go back as HWP. So why-hwp stops asking and just deals with it, on any OS, from a script.

Editing HWP documents from scripts usually needs one of these:

- Hancom Office on Windows, driven through COM automation
- A Python or Java library, which means installing that runtime first

why-hwp is written in Go and built into one executable per platform, so the same tool runs anywhere you can copy a file.

## Goals

- Read and edit **HWPX** documents directly (HWPX is a zip of XML files, so this works on every platform)
- Keep the original layout: reuse the document's own paragraph and character styles
- Record edits as **tracked changes**, so reviewers see them in Hancom's review view
- Insert images at the highest resolution that fits the target area
- Keep existing memos and other authors' content untouched
- Convert HWP and HWPX to HWP, HWPX, PDF or UTF-8 text on every platform with a built-in engine, without Hancom Office (on Windows, Hancom Office can be used instead)

## Platforms

| OS | Architectures |
|----|---------------|
| Windows | amd64, arm64 |
| Linux | amd64, arm64 |
| macOS | amd64, arm64 |

Every command works on every platform. Only `convert -engine hancom` needs Windows with Hancom Office installed.

## Commands

### text

```sh
why-hwp text [-format plain|json] [-view accepted|original|markup] <file>
```

Prints the text of an `.hwpx`, `.hwp` or `.docx` file, one block per line as `<loc><TAB><text>`. A loc points back into the document: `s0/p12` is paragraph 12 of section 0, `s0/p3/t0/r1/c2` is row 1, column 2 of the first table in paragraph 3. Memo bodies, headers, footers and footnotes are left out.

In a document with tracked changes, `-view` picks what you see: `accepted` (default) is the text with every change accepted, which is what Hancom Office saves as TXT apart from line breaks and spacing; `original` is the text with every change rejected; `markup` shows both, inserted text as `[+text+]` and deleted text as `[-text-]` (a changed paragraph break shows as `\n` inside the marks). Locations do not change with the view: they number the paragraphs as stored, deleted and inserted ones included, so they are the locations `apply` takes. A paragraph that only exists in the other view has no line, and a paragraph whose break the view removes is shown joined with the next one. Views work for `.hwpx` and `.docx` (`w:ins`, `w:del`, `w:moveFrom`, `w:moveTo`); `.hwp` files have no views.

### info

```sh
why-hwp info [-format plain|json] [-view accepted|original|markup] <file>
```

Shows sections, paragraphs, tables and their size, owner marks such as `(A)` or `(A/B)` with where they occur, memos, tracked changes, styles and pictures. Owner marks are counted in the text of `-view` (default `accepted`); with tracked changes the view is shown under the count.

`text` and `info` also read `.hwp` (HWP 5.0 binary) files and give the same locations as the `.hwpx` copy of the same document. Password-protected, DRM and distribution documents are refused with an error (exit code 1); they are not decrypted.

### apply

```sh
why-hwp apply -spec edits.json -o out.hwpx -author NAME [-date RFC3339] [-no-track] [-force] in.hwpx|in.hwp
```

Writes a copy of an HWPX document with the edits in the spec recorded as tracked changes (use `-no-track` to edit without them). Locations are the ones `why-hwp text` prints for the input, and every op uses the input's numbering. Ops: `set_text` {loc, text}, `replace` {loc, find, with, all}, `insert_after` / `insert_before` {loc, paras:[{text, like}]}, `delete` {loc}, `image` {after or loc, src (PNG), width_mm, like}. New paragraphs copy the paragraph, style and character shapes of `like` (or of `loc`). Paragraphs that are not edited keep their bytes; memos are kept; PNGs are embedded unchanged. An `.hwp` input is first converted to HWPX in a temporary folder with the built-in engine, and the edits are applied to that copy; the output is always `.hwpx`, and locations are the ones `why-hwp text` prints for the `.hwp` file. The conversion can lose parts of the document, so apply says so on stderr; check the result with `why-hwp verify -orig in.hwp out.hwpx`. Password-protected, DRM and distribution documents are refused before conversion, as in `text`. Example: [`testdata/apply/spec.json`](testdata/apply/spec.json).

### verify

```sh
why-hwp verify [-orig orig] [-allow locs.txt] [-banned words.txt] [-ignore-case] [-view accepted|original|markup] [-format plain|json] <file>
```

Checks a document before you hand it over. It reads `.hwpx`, `.hwp`, `.docx`, `.md` and `.txt`; md and txt are read line by line with locations `l1`, `l2`, .... With `-orig` it lists blocks changed, added (for example "added 2 after original s0/p7") and removed, and fails when a memo, table, picture or tracked change of the original is lost or a table shrank. With `-allow` (one location per line, `s0/p4/t0/*` matches a prefix) a change outside the list fails. Banned words (`-banned`, one per line, `#` comments, `re:` for a regular expression) and em dashes fail; en dashes and numbers the original does not have are warnings. Exit status is 1 when there are violations, 0 otherwise, and 2 for a usage error or an unreadable file. Both documents are read in `-view` (default `accepted`, see `text`), so in a tracked-change hwpx or docx deleted text is left out and inserted text is compared as the new text; the notes say which view was used. hwp files have no views, so in a tracked-change hwp verify cannot tell deleted text from inserted text.

### convert

```sh
why-hwp convert [-to hwp|hwpx|pdf|txt] [-o out] [-engine builtin|hancom] [-timeout 10m] [-force] <in.hwp|in.hwpx|in.docx>
```

Converts a document to HWP, HWPX, PDF or UTF-8 text. The format comes from `-to`, or from the extension of `-o`. Without `-o` the output is the input path with the new extension; the output cannot be the input file itself. An existing output file is not overwritten unless `-force` is given. `-timeout` limits one conversion (default 10m with the built-in engine, 120s with `-engine hancom`).

By default (`-engine builtin`) the conversion is done by the engine built into why-hwp: [rhwp](https://github.com/edwardkim/rhwp) and resvg compiled to WebAssembly (WASI) and run with [wazero](https://wazero.io), so it works on every OS and starts no other program. It reads `.hwp` and `.hwpx`; `.docx` is refused with an error. The first run compiles the engine, which takes several seconds (about 10 seconds on an 8-core PC), and keeps the result in the user cache folder (`why-hwp/wazero`), so later runs start quickly. Only the current version's compiled engine is kept there: once it is in place, those of other why-hwp versions are deleted. `-timeout` covers the conversion and, for PDF output, font downloads. The first compile counts against `-timeout` too: a `-timeout` shorter than the compile fails when the limit is reached, and since the compile did not finish, the next run compiles again.

The PDF is an approximation of what Hancom Office prints, not a copy: rhwp lays out the pages itself, so line breaks and positions can differ, most of all when the document's fonts are not available. It comes closer when the fonts the document uses are installed: compared with what Hancom Office 2024 prints on a computer with the same fonts, 15 of 18 test documents matched: 13 had every word on the same page and within 1 pt of its position, and 2 whose pages hold no text in Hancom Office's PDF (a chart and a watermark, drawn as pictures) matched when the page pictures were compared. A document set to print two pages per sheet becomes a PDF with two pages side by side on each landscape sheet, as Hancom Office prints it, when all its pages are A4 portrait. In the same comparison, the HWP and HWPX files written by the engine printed the same as the originals in Hancom Office for all 18.

Fonts are used only for PDF output; HWP, HWPX and text output read no fonts and download nothing, so they are the same with or without fonts. For PDF, fonts installed on your computer are used first and are never downloaded or copied. why-hwp reads the usual font folders (Windows: `%WINDIR%\Fonts`, `%LOCALAPPDATA%\Microsoft\Windows\Fonts` and Hancom Office's `Shared\TTF\All` and `Shared\TTF\Hwp`; macOS: `/System/Library/Fonts`, `/Library/Fonts`, `~/Library/Fonts`; Linux: `$XDG_DATA_HOME/fonts` (`~/.local/share/fonts` when it is not set), `~/.fonts`, `/usr/share/fonts`, `/usr/local/share/fonts`). Fonts the document needs that are not installed are downloaded from rhwp's list of public fonts. Text whose fonts are all missing is drawn with the fallback fonts rhwp names (Noto Serif CJK KR, Noto Sans CJK KR and Noto Sans Mono CJK KR); when one of them is not installed, rhwp's public Noto Sans KR is downloaded too and used in its place, so that text is not left out of the PDF. `WHY_HWP_FONT_DIRS` adds folders that are searched first (separated by `;` on Windows, `:` elsewhere), and `WHY_HWP_SYSTEM_FONTS=0` stops reading the system folders, so the output depends only on downloaded fonts and `WHY_HWP_FONT_DIRS`. Downloaded fonts are kept in the user cache folder (`why-hwp/fonts`) and are not downloaded again. With `WHY_HWP_OFFLINE=1` nothing is downloaded: the fonts that are missing are listed in a warning on stderr and the PDF uses substitutes. Which fonts are downloaded and from where follows rhwp's list, and each font keeps its own maker's license. The user cache folder is `~/.cache` (or `$XDG_CACHE_HOME`) on Linux, `~/Library/Caches` on macOS and `%LOCALAPPDATA%` on Windows.

With `-engine hancom` the installed Hancom Office does the conversion (Windows only; on other systems it exits with "convert needs Windows with Hancom Office"). It also reads `.docx`, and its PDF is what Hancom Office prints. Hancom runs hidden and works on a copy in the temporary folder, so it never asks for file access. Only the Hancom instance this command starts is closed, and on timeout only that process is killed; Hancom windows you already have open are left alone.

### svg2png

```sh
why-hwp svg2png [-width-mm N | -width-px N] [-dpi 600] [-trim] [-timeout 2m] [-o out.png] <in.svg>
```

Renders an SVG to a PNG sized for the space it will fill in the document (600 dpi by default; the height follows the SVG aspect ratio). Rendering is done by resvg built into why-hwp, so no browser or other program is needed and the PNG is the same on every machine. Text is drawn only with the fonts embedded in the SVG: the TrueType or OpenType files in `data:` URIs of `@font-face` rules in `<style>`, which `svgfont` writes. Fonts installed on the system are not used, so run `svgfont` first; an SVG with `<text>` but no embedded font is rendered without its text and a warning is printed to stderr. Embedded fonts that cannot be decoded, or that are not TrueType or OpenType (such as WOFF), are skipped with a warning. `-trim` cuts white margins. Without a width, the SVG's own size (96 px per inch) is used. Output is limited to 20000 px per side and 120 million pixels. The first run compiles the engine, which takes several seconds, and keeps the result in the user cache folder (`why-hwp/wazero`, only the current version's), so later runs start quickly. `-timeout` (default 2m) limits the render. The first compile counts against `-timeout` too: a `-timeout` shorter than the compile fails when the limit is reached, and the next run compiles again.

### svgfont

```sh
why-hwp svgfont [-regular font.ttf] [-bold font.ttf] [-family NAME] (-o out.svg | -w) <in.svg>
```

Embeds a Korean font into an SVG figure, so it looks the same on a PC that does not have the font. Only the characters of the `<text>` elements (and their `<tspan>`s) go in: per weight, a TrueType subset is written as base64 into `<style id="embedded-font">` at the start of `<defs>`, and `'NAME'` (default `WhyHwpEmbed`) is put first in every `font-family` attribute and declaration. Text with `font-weight` bold or 600 and more, on the element or its nearest ancestor that sets it, uses the bold font. Each subset's own name table is also set to `NAME` with style Regular or Bold (weight 400 or 700), so renderers that ignore `@font-face` and pick fonts by their names, such as resvg, find it; the copyright and licence names are kept. An existing `<style id="embedded-font">` is replaced, so running the command again on its output gives the same file. A run of the base64 that looks like a cloud access key (`AKIA` and 16 letters or digits), which commit hooks take for a secret, is split with a CSS line continuation (backslash and line break); the font data stays the same. stderr shows, per weight, the font file, the number of characters and the subset size, and lists characters the font lacks or draws blank.

Fonts must be TrueType (`.ttf` with glyf outlines). CFF (`.otf`), variable fonts (an `fvar` table, such as `NotoSansKR-VF.ttf`), collections (`.ttc`) and fonts whose licence restricts embedding (OS/2 fsType bit 0x0002) are refused. Each weight is taken from the flag, then `WHY_HWP_FONT_REGULAR` / `WHY_HWP_FONT_BOLD`, then a search of the OS font folders (Windows `%WINDIR%\Fonts` and `%LOCALAPPDATA%\Microsoft\Windows\Fonts`; Linux `/usr/share/fonts`, `/usr/local/share/fonts`, `~/.local/share/fonts`, `~/.fonts` and their subfolders; macOS `/Library/Fonts`, `~/Library/Fonts`) for `NotoSansKR-Regular.ttf` / `NotoSansKR-Bold.ttf`, `NanumGothic.ttf` / `NanumGothicBold.ttf`, `NanumSquareR.ttf` / `NanumSquareB.ttf` and `malgun.ttf` / `malgunbd.ttf`, in that order, and last the why-hwp font cache (`why-hwp/fonts` in the user cache folder, where `convert` keeps the fonts it downloads). Cached files are not named after the font, so there the fonts are matched by the family in their name table, in the same order (Noto Sans KR, NanumGothic, NanumSquare, Malgun Gothic), and by weight (400, or 700 for bold). rhwp's Noto Sans KR Bold calls its family Noto Sans KR Thin and is not matched. Variable, CFF and restricted fonts found by the search are skipped. If nothing is found, NanumGothic Regular or Bold is downloaded from Google Fonts' repository at a pinned commit (`https://cdn.jsdelivr.net/gh/google/fonts@2fe1e8c6b137fb13041a7cfca6fdcd8a910ea4dc/ofl/nanumgothic/`), checked against a SHA-256 fixed in why-hwp and kept in the font cache as `svgfont/NanumGothic-Regular.ttf` / `svgfont/NanumGothic-Bold.ttf`, where later runs find it without downloading; a line on stderr names what was downloaded. NanumGothic is a static TrueType font that allows embedding, under the SIL Open Font License 1.1 (the `OFL.txt` in that folder), which permits embedding the font and redistributing it with software; "Nanum" and "NanumGothic" are Reserved Font Names, which the embedded subset does not use since it is renamed to `NAME`. With `WHY_HWP_OFFLINE=1` nothing is downloaded and the error says so; a failed download adds its reason to the error. To use other fonts, install the static Noto Sans KR TrueType fonts or pass `-regular` (and `-bold` when the figure has bold text).

### svgcheck

```sh
why-hwp svgcheck [-regular font.ttf] [-bold font.ttf] [-format plain|json] [-tol 2] <in.svg>
```

Checks the layout of an SVG figure by computation. Text is measured with the advance widths of the fonts (found as in `svgfont`), with `font-size`, `text-anchor` and `tspan` lines applied. Violations: text that leaves the inside of its box (the smallest `<rect>` holding the text's `x`, `y`; a stroke narrows the inside), text of different `<text>` elements that overlaps, an arrow end that is more than `-tol` px from every box edge (an arrow is a `line`, `polyline` or `path` with `marker-start` or `marker-end`; both ends are checked), and an arrow that passes through a box holding neither of its ends. Three or more arrow ends within 3 x `-tol` px of each other are a warning, because the heads look like a star. Paths with curves or arcs are not checked, and only `translate` transforms are followed; anything skipped is listed under notes. Locations are the element id, or the element name and its number in document order (`text#3`, `line#7`), with the start of the text. Exit status: 0 no violations (warnings allowed), 1 violations, 2 usage error, a file that cannot be read or no usable font.

## Build

Requires Go 1.25 or later and GNU make. On Windows, run make from Git Bash.

```sh
make            # build for the current OS into bin/
make dist       # build all six platforms into dist/why-hwp-<os>-<arch>[.exe]
make checksums  # build dist/ and write dist/SHA256SUMS
make test       # go test ./...
```

The version printed by `why-hwp version` comes from `git describe --tags --always --dirty`. Override it with `make VERSION=v1.0.0`.

Pushing an annotated `v*` tag runs `.github/workflows/release.yml`, which tests, builds all six platforms and publishes a GitHub release with the binaries, `SHA256SUMS` and the tag message as the notes.

## License

[Apache License 2.0](LICENSE)

The built-in engine (`internal/engine/engine.wasm.gz`) contains rhwp (MIT), resvg (Apache-2.0 or MIT), tiny-skia (BSD-3-Clause), code derived from the Independent JPEG Group's work (IJG License), a subset of the Latin Modern Math font (GUST Font License) and other Rust crates. Their license texts and the list of crates are in [`wasm/LICENSES/`](wasm/LICENSES/). Fonts downloaded at run time are not part of why-hwp and keep their own licenses.
