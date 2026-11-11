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
