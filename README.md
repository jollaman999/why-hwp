[한국어](README.ko.md)

# why-hwp

A single-binary command-line tool for working with Hangul word processor documents (HWP / HWPX) on Windows, Linux and macOS, without Python, Java or any other runtime.

> **Status: early development.** Nothing is usable yet. This README describes the goal, not finished features.

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
- On Windows with Hancom Office installed, convert between HWP and HWPX

## Platforms

| OS | Architectures |
|----|---------------|
| Windows | amd64, arm64 |
| Linux | amd64, arm64 |
| macOS | amd64, arm64 |

## Commands

### text

```sh
why-hwp text [-format plain|json] <file>
```

Prints the text of an `.hwpx` or `.docx` file, one block per line as `<loc><TAB><text>`. A loc points back into the document: `s0/p12` is paragraph 12 of section 0, `s0/p3/t0/r1/c2` is row 1, column 2 of the first table in paragraph 3. Memo bodies, headers, footers and footnotes are left out.

### info

```sh
why-hwp info [-format plain|json] <file>
```

Shows sections, paragraphs, tables and their size, owner marks such as `(A)` or `(A/B)` with where they occur, memos, tracked changes, styles and pictures.

`.hwp` files are not supported by `text` and `info` yet.

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

## License

[Apache License 2.0](LICENSE)
