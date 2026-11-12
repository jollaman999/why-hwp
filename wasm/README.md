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
| `patch-rhwp.sh` | Changes rhwp needs to build and run under WASI, then applies `patches/` |
| `patches/` | Diffs against rhwp: layout fixes found by comparing with Hancom Office 2024 |
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
| `font-plan --in <file>` | Prints the fonts the document needs that neither `/fonts` nor the `RHWP_FONT_PATH` folders have, plus the PDF fallback font below when it is needed: `[{"url":..., "sha256":... or null}]` |
| `font-install --url <url> --file <file>` | Checks a downloaded font against rhwp's font ledger and stores it in `/fonts` |
| `svg2png --in <svg> --out <png> --width <px> --height <px> [--font <file> ...]` | Draws the SVG on white at that size, with only the given fonts |
| `version` | `why-hwp-engine <version> rhwp <commit> resvg <version>` |

rhwp downloads fonts from a fixed list of public URLs (its font ledger), but its
HTTP client cannot run inside WebAssembly. So the host does the download:
`font-plan` lists the URLs, the host fetches them into `/dl`, and `font-install`
verifies and caches each one. Some fonts are only asked for once another one is
present, so the host repeats plan and install until the plan comes back empty.

rhwp's PDF stage leaves out a text run when no family in its `font-family`
list is in the font database. The lists end in `serif`, `sans-serif` or
`monospace`, which rhwp points at Noto Serif CJK KR, Noto Sans CJK KR and
Noto Sans Mono CJK KR, so on a computer without those fonts a run whose named
fonts are all missing disappears. When one of the three is not available,
`font-plan` also asks for rhwp's public Noto Sans KR (its generic webfont), and
`convert` points the missing generic families at it. Its cache folder is added
at the end of `RHWP_FONT_PATH`, so it only supplies characters no other font
has.

## What the patch changes in rhwp

rhwp is pinned to commit `7213ed1f96266b95e22cd5645a07851c9c18298e`.
`patch-rhwp.sh` changes it in five ways:

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
5. Every `patches/*.patch` is applied with `git apply --unidiff-zero`, in name
   order (below).

The first four are a script rather than a diff because the first one rewrites
313 lines in 49 files: a pattern says what was done in one line and still
applies when rhwp moves those lines. Each step checks whether it is already
applied and fails if it finds neither the old nor the new text, so running the
script twice gives the same tree and an rhwp change cannot slip past it. Step 5
does the same with `git apply --reverse --check`: a patch that reverses cleanly
is skipped, and one that neither applies nor reverses stops the build.

## Layout patches

`patches/rhwp-layout.patch` changes how rhwp lays out and measures text. Each
change was found by comparing word positions in rhwp's PDF with the PDF Hancom
Office 2024 prints from the same document, on a computer that has the
document's fonts, so that font substitution is not the cause. With it, 11 of 18
test documents have every word within 1 pt of Hancom Office's position, up from 6.

| Change | Before |
|--------|--------|
| HCR Batang (함초롬바탕) box-drawing characters (U+2500-U+257F) advance 970/1000 em, as the font's `hmtx` gives for all 128 of them and Hancom Office prints them. Hancom's own line-drawing private-use characters, which display as these, keep their half-em advance | rhwp's verified symbol widths had no entry for them |
| Bold text measures a character with the regular face's advance when the bold width table lacks it and the glyph is not known to be missing | Such characters fell through to the bold face's missing-width path |
| A table or picture caption paragraph with a bullet head draws its bullet before the text | The caption text was laid out without the bullet |
| The first line of an hwpx single column, when its stored line position equals the paragraph's spacing-before, does not add that spacing again | The spacing-before was counted twice at the top of the column |
| Text output puts a space after a list bullet | The bullet and the first word were joined in `txt` output |
| PDF font fallback looks for a missing character in the rest of the SVG `font-family` list before the font database order | The database order alone chose the substitute |
| "한컴 윤고딕 250" is measured with the Haan YGodic 250 widths | The Korean name matched no width table |
| Glyphs under negative letter spacing are fitted to their natural advance | Only positive spacing was taken out of the glyph fit |
| An ASCII bullet (`-`) takes a half-em cell, other bullets a full em | ASCII bullets took the glyph's own width |
| A space after Latin text advances with the Hangul em and width ratio when they differ from the Latin ones | It used the Latin em and ratio |
| Equation arrows are drawn as glyphs of the equation font list (HyhwpEQ, Latin Modern Math, ...); HYhwpEQ equations in the current format keep a thin space after a function name such as `sin` | Arrows were drawn as generic math symbols, and the thin space was dropped |

`patches/rhwp-layout2.patch` is applied after it and was found the same way.
With both, 12 of 18 test documents pass.

| Change | Before |
|--------|--------|
| Converting an HWP5 document to HWPX writes its print settings (`DOC_DATA`: print method such as 2-up, zoom, crop marks) to `settings.xml` as `PrintInfo`, the way Hancom Office saves them. The 40-page test document set to print two pages per sheet prints on 20 pages, as its HWP original does | Only crop marks were carried over, so Hancom Office printed the HWPX one page per sheet |
| A paragraph-anchored table whose host paragraph holds only spaces sits at the top of the paragraph when the stored line positions show the space line was pushed below the table (the gap before the line equals the table's offset, outer margins and height) | The space line was placed first and the table drawn one line lower |
| Bold HCI Poppy `·` (U+00B7) advances 384/1000 em, like the regular face | It was measured with the substitute font's width, pulling the next character about 2.8 pt left |
| A space inside a Latin run of a stored line advances with the Hangul em and width ratio, as one after a Latin run already did | The space kept the Latin em and ratio |
| Wingdings U+F0FC (check mark) advances 1609/2048 em, the width in the font's `hmtx` | It was measured with the full width of the substitute `✔` |
| The invisible text drawn over a vector `·`, kept so the character can be found and copied, carries the run's `font-family` | It had no `font-family` |

`patches/rhwp-layout3.patch` is applied after `rhwp-layout2.patch` and changes
which glyph rhwp draws when a symbol font is missing or present. With all
three, 13 of 18 test documents pass.

| Change | Before |
|--------|--------|
| When the PDF font database has no HyhwpEQ or HancomEQN, equation private-use characters of that face are drawn as the characters they stand for (`a`, `<`, `α`, ...) in the default equation fonts, as rhwp's Skia renderer already does | The private-use code points went to PDF font fallback, which picked whatever installed font covered them (on the Linux test computer: OpenSymbol, Font Awesome, Meslo, Adwaita) |
| A Wingdings private-use character (U+F020-U+F0FF) that the run's font lacks is drawn as itself in Wingdings when a Wingdings font is found and has the glyph, as Hancom Office prints it. Without Wingdings the Unicode substitute (such as `✔`) is kept | The Unicode substitute was always drawn, in the run's font, so its width and position differed from Hancom Office's Wingdings glyph |

`patches/rhwp-layout4.patch` is applied after `rhwp-layout3.patch`. With all
four, 15 of 18 test documents pass: 13 have every word within 1 pt, and 2
whose pages hold no words in Hancom Office's PDF (a chart and a watermark) are
judged by comparing the page pictures. Counted the same way, the first three
patches give 14.

| Change | Before |
|--------|--------|
| Superscript and subscript characters are 0.64 em in size, raised 0.44 em or lowered 0.12 em, as Hancom Office 2024 prints them, in every document except HWP 3.0 ones | These values were used only with the 2024 layout generation; other documents got 0.7 em, raised 0.3 em, which left a superscript `*` 1.8 pt from Hancom Office's position |
| A bold character of an HFT font that is not ASCII and is measured with a known original-font width advances 0.05 em more, as other bold HFT characters do (bold HCI Poppy `·`: 0.434 em). ASCII keeps its plain advance, as bold HCI Poppy `=` does in Hancom Office's print | The character had no bold advance, so the text after each bold `·` sat about 0.7 pt to the left |
| The right quote `’` (U+2019) of the 필기 font advances 500/1000 em | It was measured with the substitute font's width |
| Malgun Gothic arrows (U+2190-U+2199, U+21D2, U+21D4) advance 1946/2048 em, the width in the font's `hmtx` | They took a full em |
| A document set to print two pages per sheet (print method 4) whose pages are all A4 portrait exports to PDF as landscape sheets with two pages side by side: scaled by 0.71, the left page at the left edge, the right page at half the sheet width, both at the top. Measured on a document without memos printed two-up by Hancom Office 2024. Links and named destinations move with their page. Other print methods and paper sizes stay one page per sheet | The PDF had one page per sheet (40 pages where Hancom Office prints 20) |
| The 64 MiB limit on supplied (downloaded) fonts applies only when rhwp is built with Skia, whose font catalog it protects. Without Skia, as in this engine, every supplied font is kept | Host font folders were counted against the limit first, so with a large font folder downloaded fonts such as HCR Batang and HCR Dotum were dropped and text fell back to other fonts |
| The invisible text drawn over a vector `·` is fitted to the same glyph width as visible characters | It took the substitute font's own width, so the word box read from the PDF ended about 2 pt short |

`patches/rhwp-layout5.patch` is applied after `rhwp-layout4.patch`. The count
stays at 15 of 18, and two of the three documents that fail come closer. In the
equation document every word of Hancom Office's PDF has a partner (38 had
none), the largest deviation is 1.48 pt instead of 2.30 pt and 5 word edges are
more than 1 pt off instead of 16. In the 40-page document printed two pages per
sheet the largest deviation is 4.85 pt instead of 8.30 pt and 235 word edges are
more than 1 pt off instead of 411.

| Change | Before |
|--------|--------|
| A fraction in a superscript of an HYhwpEQ equation is placed by the line and fraction frames at each of the 15 body sizes that have a line frame (6 pt to 16 pt, 18, 20, 22 and 24 pt). The frames of sizes that were not measured follow the rule all measured ones follow: the height is the size rounded to a whole pixel at 96 dpi, the math axis is the middle of the frame and the baseline is 0.36 em below it | Only seven body sizes had a frame for it. At 11 pt the numerator and denominator sat 1.8 pt and 2.3 pt below Hancom Office's |
| The name of an HYhwpEQ `lim` shares the math axis of the body line, and the lower limit sits right under the name's line frame | The name was 1.2 pt and the lower limit 1.4 pt above Hancom Office's |
| The cells of an HYhwpEQ `cases` row are aligned by the middle of their frames | They were aligned by their baselines, which put a cell next to one with a superscript 1.2 pt to 1.6 pt below Hancom Office's |
| In a PDF, the equation parts Hancom Office paints with HyhwpEQ glyphs are painted with those glyphs when the font database holds HyhwpEQ: fraction, radical and overline rules up to 9 em long, radical signs, parentheses of text height, braces, a tall left brace without a right one (a top, a middle and a bottom part with an extender in each gap) and the upright letters of function names. Without the font they are drawn as paths, as before | They were always drawn as paths, so the 38 words Hancom Office's PDF holds for them had no partner |
| A right-aligned line leaves trailing ideographic spaces (U+3000) out of the width it aligns, as it does trailing spaces | The ideographic space was aligned with the text, which left the line about 6.5 pt to the left on the two-up sheet |
| `–` (U+2013), `≥` (U+2265), `「` and `」` (U+300C, U+300D) of an HFT font advance a full em | `–` advanced half an em, and the text after such a character sat 3.5 pt to 5 pt to the left on the two-up sheet |

`patches/rhwp-layout6.patch` is applied after `rhwp-layout5.patch`. The count
stays at 15 of 18. In the 40-page document printed two pages per sheet 115 word
edges are more than 1 pt off instead of 235, none of them vertically (50 were),
and the largest deviation is 4.82 pt instead of 4.85 pt. In the third failing
document the largest deviation is 3.47 pt instead of 3.54 pt. The PDFs of the
other 16 documents are the same byte for byte.

The first two changes bring the 40-page document to 109 such edges. The third
moves the HFT text of that document and of the third failing one, and in this
comparison it gains and loses. The scale that fits the right-hand pages of the
two-up sheets to Hancom Office's pages is 0.7100 to 0.7103 instead of 0.7093 to
0.7103, and on four of the five pages with chart labels that Hancom Office
draws as outlines the labels lie within 0.25 pt to 0.50 pt of its shapes
instead of 0.38 pt to 0.88 pt. But one line that was about 1 pt to the right
(two edges at 1.01 pt and 1.03 pt) is 1.06 pt to 1.12 pt to the right (8 more
edges), and three words have no partner where every word had one: a `」` that
was read from the PDF as a word of its own is read as part of the word around
it, at the same place, and one chart label no longer passes the outline check.
rhwp's test `issue_7445_hft_glyph_paint_frame` asserts the older rule for a
document laid out as Hangul 2020 prints it and fails on that assertion with
this patch; the patch leaves the test as it is.

| Change | Before |
|--------|--------|
| The double quotes `“` and `”` (U+201C, U+201D) of the plain 한양중고딕 (Hanyang Junggothic) HFT font advance 375/1000 em, the width in the font's advance table and in the Type 3 font Hancom Office prints | They advanced half an em, so the text after each quote sat 0.125 em to the right. On two pages of the two-up document 76 word edges were more than 1 pt off |
| The first line of a paragraph that follows an empty paragraph holding one top-and-bottom picture sits at the bottom of the picture's band, when the picture takes the empty paragraph's own line and the stored first line of the next paragraph is at that place. The paragraph's spacing before is spent inside the band | The spacing before was added below the band a second time. The lines after two such pictures of the two-up document were more than 1 pt low (50 word edges) |
| In a PDF, glyphs of an HFT font are painted through the printer's precise device matrix until the document's first solid fill. What counts as that fill follows Hancom Office 2024 for every document: a table cell or paragraph background counts | It followed the document's layout generation, and outside the 2024 generation such backgrounds did not count. A document whose only fills are backgrounds kept the matrix, a 0.99946 x 0.99891 scale on each group of HFT glyphs of an A4 page, on every page |

The patches also add rhwp unit tests for these changes. They are zero-context
diffs because the surrounding rhwp lines contain em dashes, which this
repository's commit check refuses. The comment at the top of each file lists
the changes by source file. To change one, apply it to a tree that
`patch-rhwp.sh` produced up to that patch, edit, and write a new diff with
`git diff -U0`.

A later patch does not change the lines an earlier patch added, and does not
insert in the middle of them; it inserts next to them. `patch-rhwp.sh` tells
that a patch is already applied by checking that it reverses cleanly. When a
later patch has changed or split the lines of an earlier one, the earlier one
no longer reverses, so running `patch-rhwp.sh` on the same tree again takes it
for not applied and fails.

## fontdb

Without memory mapping, fontdb 0.23 reads the whole font file from disk every
time a face's data is asked for, and usvg asks about a dozen times per glyph.
With 20-30 MB Korean fonts that made PDF export 25-35 times slower than
native rhwp. Loading a font folder had the same cost: fontdb read every file
in it whole to learn each face's names and style, and one PDF export loads
the host font folders four times (once in `font-plan`, three times in
`convert`), so a 475 MB Linux font folder meant about 2 GB of reads per PDF.

`vendor/fontdb` is fontdb 0.23.0 from crates.io (MIT, `LICENSE` kept) with two
changes in `src/lib.rs`, both under WASI only; other targets work as before:

1. `Source::File` data is read once per file and kept for the rest of the
   process.
2. `load_font_file` and `load_fonts_dir` read only the start of a font file
   (its table directories) and the `name`, `OS/2` and `post` tables, which are
   all fontdb takes a face's details from, and keep the faces found per file
   for the rest of the process. A file whose table directory lies past the
   first 16 KB is read whole as before. The faces are the same as from the
   whole file; a unit test checks this for a font, a collection, a table
   running past the end of the file and files that are not fonts.

`Cargo.toml` swaps it in with `[patch.crates-io]`, so usvg, resvg and
rhwp's vendored svg2pdf all use it. For the second change, the functions that
read a face's names and style take the three tables instead of the parsed
font. The only other edits are dropping the
dev-dependencies, which serve upstream tests that are not copied, and turning
an em dash in a doc comment into a hyphen for this repository's commit check.

It is a copy in the repository rather than a patch applied at build time
because cargo needs the `[patch]` path to exist before it resolves, so a
patch would have to fetch the crate outside cargo. The copy is about 65 KB.
To see the change, compare it with the registry copy:

```sh
diff -r ~/.cargo/registry/src/index.crates.io-*/fontdb-0.23.0/src vendor/fontdb/src
```

Both changes have unit tests that run natively (the test build turns them on
for every target):

```sh
cd wasm
cargo test -p fontdb --lib
```

## Rebuilding

Needs Rust 1.93.1 or newer with the `wasm32-wasip1` target, clang, git, perl
and gzip. clang is the C compiler: the `ring` crate compiles C for
`wasm32-wasip1`, and the usual system gcc cannot target WebAssembly. Set
`CC_wasm32_wasip1` to use another clang.

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
