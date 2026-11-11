[한국어](README.ko.md)

# why-hwp

A single-binary command-line tool for working with Hangul word processor documents (HWP / HWPX) on Windows, Linux and macOS, without Python, Java or any other runtime.

> **Status: early development.** The commands below work and are tested on Windows and Linux, but options and the spec format may still change before 1.0.

## Why

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
- On Windows with Hancom Office installed, convert HWP, HWPX and DOCX to HWP, HWPX, PDF or UTF-8 text

## Platforms

| OS | Architectures |
|----|---------------|
| Windows | amd64, arm64 |
| Linux | amd64, arm64 |
| macOS | amd64, arm64 |

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
why-hwp apply -spec edits.json -o out.hwpx -author NAME [-date RFC3339] [-no-track] [-force] in.hwpx
```

Writes a copy of an HWPX document with the edits in the spec recorded as tracked changes (use `-no-track` to edit without them). Locations are the ones `why-hwp text` prints for the input, and every op uses the input's numbering. Ops: `set_text` {loc, text}, `replace` {loc, find, with, all}, `insert_after` / `insert_before` {loc, paras:[{text, like}]}, `delete` {loc}, `image` {after or loc, src (PNG), width_mm, like}. New paragraphs copy the paragraph, style and character shapes of `like` (or of `loc`). Paragraphs that are not edited keep their bytes; memos are kept; PNGs are embedded unchanged. An `.hwp` input must be converted with `why-hwp convert -to hwpx` first. Example: [`testdata/apply/spec.json`](testdata/apply/spec.json).

### verify

```sh
why-hwp verify [-orig orig] [-allow locs.txt] [-banned words.txt] [-ignore-case] [-view accepted|original|markup] [-format plain|json] <file>
```

Checks a document before you hand it over. It reads `.hwpx`, `.hwp`, `.docx`, `.md` and `.txt`; md and txt are read line by line with locations `l1`, `l2`, .... With `-orig` it lists blocks changed, added (for example "added 2 after original s0/p7") and removed, and fails when a memo, table, picture or tracked change of the original is lost or a table shrank. With `-allow` (one location per line, `s0/p4/t0/*` matches a prefix) a change outside the list fails. Banned words (`-banned`, one per line, `#` comments, `re:` for a regular expression) and em dashes fail; en dashes and numbers the original does not have are warnings. Exit status is 1 when there are violations, 0 otherwise, and 2 for a usage error or an unreadable file. Both documents are read in `-view` (default `accepted`, see `text`), so in a tracked-change hwpx or docx deleted text is left out and inserted text is compared as the new text; the notes say which view was used. hwp files have no views, so in a tracked-change hwp verify cannot tell deleted text from inserted text.

### convert

```sh
why-hwp convert [-to hwp|hwpx|pdf|txt] [-o out] [-timeout 120s] [-force] <in.hwp|in.hwpx|in.docx>
```

Converts a document with the installed Hancom Office (Windows only; on other systems it exits with "convert needs Windows with Hancom Office"). The format comes from `-to`, or from the extension of `-o`. Without `-o` the output is the input path with the new extension. Text output is UTF-8. An existing output file is not overwritten unless `-force` is given. Hancom runs hidden and works on a copy in the temporary folder, so it never asks for file access. Only the Hancom instance this command starts is closed, and on timeout only that process is killed; Hancom windows you already have open are left alone.

### svg2png

```sh
why-hwp svg2png [-width-mm N | -width-px N] [-dpi 600] [-o out.png] <in.svg>
```

Renders an SVG to a PNG sized for the space it will fill in the document (600 dpi by default; the height follows the SVG aspect ratio). It drives a headless Chrome, Edge, Chromium or Firefox, so fonts, including web fonts embedded in the SVG as data URIs, come out exactly as in a browser. Pick a browser with `-browser` or `WHY_HWP_BROWSER`; `-trim` cuts white margins. Without a width, the SVG's own size (96 px per inch) is used. Output is limited to 20000 px per side and 120 million pixels.

## Build

Requires Go 1.24 or later and GNU make. On Windows, run make from Git Bash.

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
