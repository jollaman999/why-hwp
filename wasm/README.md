# why-hwp-engine

`why-hwp convert` and `why-hwp svg2png` run [rhwp](https://github.com/edwardkim/rhwp)
and [resvg](https://github.com/linebender/resvg) without a word processor or a
browser. Both are Rust, so they are built here into one WebAssembly module
(WASI preview 1) that the Go program runs with
[wazero](https://github.com/tetratelabs/wazero). The module is committed as
`internal/engine/engine.wasm.gz` so that `go build` and `go install` need no Rust
toolchain.

This directory holds what is needed to rebuild that file.

| File | What |
|------|------|
| `src/` | The engine: argument parsing and the commands below, on top of rhwp's library API |
| `patch-rhwp.sh` | Changes rhwp needs to build and run under WASI |
| `vendor/fontdb/` | fontdb 0.23.0 with one change for WASI (below), used in place of the crates.io copy |
| `build.sh` | Fetches rhwp, patches it, builds, writes `engine.wasm.gz` and its SHA-256 |
| `Cargo.lock` | Exact crate versions (taken from rhwp's own lock file) |
| `list-crates.py` | Writes `LICENSES/CRATES.md` |
| `LICENSES/` | License texts (rhwp, resvg, tiny-skia, IJG, Latin Modern Math) and the list of linked crates |

## Commands

The host mounts `/in` (read), `/out` (write), `/fonts` (font cache, laid out
like rhwp's `~/.cache/rhwp`; webfonts go in `/fonts/webfonts`) and `/dl`
(downloaded fonts). It sets `HOME=/home`, `RHWP_WEBFONT_OFFLINE=1` and
`WHY_HWP_TZ_OFFSET_MIN` (local UTC offset in minutes, 540 for Korea).

For `convert` and `font-plan` the host also mounts the computer's font folders
read-only as `/hostfonts-0`, `/hostfonts-1`, ... and lists them in
`RHWP_FONT_PATH`. fontdb's `load_system_fonts` finds nothing under WASI, so
this is how installed fonts reach rhwp; `font-plan` leaves out fonts found
there.

Exit codes: 0 success, 1 failure (one line on stderr), 2 usage.

| Command | Does |
|---------|------|
| `convert --to hwp\|hwpx\|pdf\|txt --in <file> --out <file>` | Converts. PDF fonts come from `/fonts` and the `RHWP_FONT_PATH` folders |
| `font-plan --in <file>` | Prints the fonts the document needs that neither `/fonts` nor the `RHWP_FONT_PATH` folders have: `[{"url":..., "sha256":... or null}]` |
| `font-install --url <url> --file <file>` | Checks a downloaded font against rhwp's font ledger and stores it in `/fonts` |
| `svg2png --in <svg> --out <png> --width <px> --height <px> [--font <file> ...]` | Draws the SVG on white at that size, with only the given fonts |
| `version` | `why-hwp-engine <version> rhwp <commit> resvg <version>` |

rhwp downloads fonts from a fixed list of public URLs (its font ledger), but its
HTTP client cannot run inside WebAssembly. So the host does the download:
`font-plan` lists the URLs, the host fetches them into `/dl`, and `font-install`
verifies and caches each one. Some fonts are only asked for once another one is
present, so the host repeats plan and install until the plan comes back empty.

## What the patch changes in rhwp

rhwp is pinned to commit `7213ed1f96266b95e22cd5645a07851c9c18298e`.
`patch-rhwp.sh` changes it in four ways:

1. rhwp writes `target_arch = "wasm32"` to mean its browser build and switches
   off PDF export, fonts and file access there. WASI is also wasm32, so every
   such condition becomes `all(target_arch = "wasm32", target_os = "unknown")`.
2. fontdb memory-maps font files by default, which fails under WASI and gives
   PDFs without fonts. Memory mapping is turned off in `usvg` and the vendored
   `svg2pdf`.
3. Memo times are saved in local time through `chrono::Local`, which is UTC
   under WASI. The WASI build reads the offset from `WHY_HWP_TZ_OFFSET_MIN`.
4. The webfont supply gains a way to report the URLs an offline run missed and
   to accept a font downloaded by the host.

The changes are a script rather than a diff because the first one rewrites
313 lines in 49 files: a pattern says what was done in one line and still
applies when rhwp moves those lines. Each step checks whether it is already
applied and fails if it finds neither the old nor the new text, so running the
script twice gives the same tree and an rhwp change cannot slip past it.

## fontdb

Without memory mapping, fontdb 0.23 reads the whole font file from disk every
time a face's data is asked for, and usvg asks about a dozen times per glyph.
With 20-30 MB Korean fonts that made PDF export 25-35 times slower than
native rhwp. `vendor/fontdb` is fontdb 0.23.0 from crates.io (MIT, `LICENSE`
kept) with one change in `src/lib.rs`: under WASI, `Source::File` data is read
once per file and kept for the rest of the process. Other targets read as
before. `Cargo.toml` swaps it in with `[patch.crates-io]`, so usvg, resvg and
rhwp's vendored svg2pdf all use it; the only other edits are dropping the
dev-dependencies, which serve upstream tests that are not copied, and turning
an em dash in a doc comment into a hyphen for this repository's commit check.

It is a copy in the repository rather than a patch applied at build time
because cargo needs the `[patch]` path to exist before it resolves, so a
patch would have to fetch the crate outside cargo. The copy is about 55 KB.
To see the change, compare it with the registry copy:

```sh
diff -r ~/.cargo/registry/src/index.crates.io-*/fontdb-0.23.0/src vendor/fontdb/src
```

The cache has a unit test that runs natively (the test build turns the cache on
for every target):

```sh
cd wasm
cargo test -p fontdb --lib
```

## Rebuilding

Needs Rust 1.93.1 or newer with the `wasm32-wasip1` target, git, perl and gzip.

```sh
rustup target add wasm32-wasip1
wasm/build.sh
```

`build.sh` puts the rhwp source in `wasm/rhwp-src` (only the paths the build
reads, about 115 MB) and reuses it on the next run. The rhwp repository is large;
if you already have a clone, `RHWP_GIT=/path/to/rhwp wasm/build.sh` takes the
pinned commit from it with `git archive` instead of fetching.
`RHWP_SRC` puts the source somewhere else and `CARGO_TARGET_DIR` moves the
build directory. A clean build takes about 10 minutes.

gzip runs with `-n`, so the same `.wasm` always gives the same `.gz`.

To move to a newer rhwp, change `RHWP_COMMIT` in `build.sh`, delete
`wasm/rhwp-src`, and copy rhwp's `Cargo.lock` over `wasm/Cargo.lock` before
building once without `--locked`. Then regenerate the crate list:

```sh
cd wasm
cargo metadata --locked --format-version 1 --filter-platform wasm32-wasip1 \
  | python3 list-crates.py > LICENSES/CRATES.md
```

## Licenses

The engine links rhwp (MIT, `LICENSES/rhwp-LICENSE`), resvg (Apache-2.0 or MIT,
`LICENSES/resvg-LICENSE-MIT`), tiny-skia (BSD-3-Clause,
`LICENSES/tiny-skia-LICENSE`) and the other crates listed with their licenses in
`LICENSES/CRATES.md`. rhwp embeds a subset of the Latin Modern Math font for
equations (GUST Font License, `LICENSES/LatinModernMath-GUST-FONT-LICENSE.txt`).
Fonts downloaded at run time keep their own licenses; rhwp's ledger records them.

This software is based in part on the work of the Independent JPEG Group.
rhwp's vendored JPEG decoder (`rhwp-zune-jpeg`) and encoder
(`rhwp-jpeg-encoder`) contain IJG-derived code; the IJG terms are in
`LICENSES/IJG-LICENSE`.
