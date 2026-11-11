# refcompare

[한국어](README.ko.md)

A development tool that measures how close a converter's output is to
Hancom Office (Hwp). It is not part of the `why-hwp` binary.

- `ref` makes reference outputs with Hancom Office on a remote Windows PC.
- `cmp` compares candidate outputs (made by any converter) with the references
  and writes a table.

## What you need

| Where | What |
|-------|------|
| Remote Windows PC | Hancom Office (checked with Hwp 2024), WSL with Windows interop on (`/proc/sys/fs/binfmt_misc/WSLInterop`), an ssh server in WSL |
| This machine | Go, `ssh` and `scp` with key login to the remote (`BatchMode=yes` is used, so no password prompt), `pdftotext` from poppler-utils |

The remote is given only through environment variables:

| Variable | Meaning |
|----------|---------|
| `WHY_HWP_REF_HOST` | ssh target, such as `user@host` |
| `WHY_HWP_REF_DIR` | a WSL folder under `/mnt/<drive>/` that Windows can see, such as `/mnt/c/Users/<you>/why-hwp-ref`. The tool works only inside `<dir>/refcompare` and does not touch anything else there |

## ref: make the references

```sh
go build -o /tmp/refcompare ./tools/refcompare
WHY_HWP_REF_HOST=user@host WHY_HWP_REF_DIR=/mnt/c/Users/me/why-hwp-ref \
  /tmp/refcompare ref -corpus CORPUS -out OUT
```

- `CORPUS` is a folder of `.hwp` and `.hwpx` files (subfolders are not read).
- For each `<name>` (the file name with its extension) Hancom Office makes
  `OUT/ref/<name>.pdf`, `<name>.txt` and the other format: `<name>.hwpx` for an
  hwp input, `<name>.hwp` for an hwpx input.
- The conversion is `why-hwp convert` (Hancom COM) built for Windows. By default
  `ref` builds it from the module of the current folder, uncommitted changes
  included. To use a committed tree, build it yourself and pass `-exe`:
  `git archive HEAD | tar -x -C /tmp/w && (cd /tmp/w && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o why-hwp.exe ./cmd/why-hwp)`.
- The binary is copied to the remote only when its SHA-256 differs from the copy
  there. Inputs are copied under names made from their hash, so file names never
  reach the remote shell.
- `OUT/ref/manifest.json` records the input hash of every output. An output that
  exists and was made from the same input is not made again; `-force` makes all
  of them again.
- Every ssh call uses `-n` and `BatchMode=yes` and has a time limit; `-timeout`
  (default 120s) is Hancom Office's limit for one conversion.
- When Hancom Office saves an empty text file (a document with no text, such as
  a chart only), the reference `.txt` is kept empty.

## Candidates

`cmp` does not make candidates. It reads a folder with
`<name>.pdf`, `<name>.txt` and `<name>.hwpx` (hwp input) or `<name>.hwp` (hwpx
input). Missing files are reported, not made.

With the rhwp command line tool (built from the rhwp repository), a candidate
folder can be made like this:

```sh
for f in CORPUS/*.hwp CORPUS/*.hwpx; do
  n=$(basename "$f")
  rhwp export-pdf "$f" -o "CAND/$n.pdf"
  rm -rf /tmp/txt && rhwp export-text "$f" -o /tmp/txt && cat /tmp/txt/*.txt > "CAND/$n.txt"
  case "$n" in
    *.hwp)  rhwp export-hwpx "$f" "CAND/$n.hwpx" ;;
    *.hwpx) rhwp convert "$f" "CAND/$n.hwp" ;;
  esac
done
```

`export-text` writes one file per page (`<base>_001.txt`, ...); they are joined
in name order.

## cmp: compare

```sh
/tmp/refcompare cmp -out OUT -cand CAND [-tol 1.0] [-fit scale] [-skip-memo] [-reconvert]
```

The table goes to stdout and `OUT/report.md`. Exit code 0 when every document
passes, 1 when one fails or cannot be compared, 2 for bad usage.

| Flag | Meaning |
|------|---------|
| `-tol` | largest allowed word-edge deviation in points (default 1.0) |
| `-fit` | how each reference page is fitted to the candidate page before measuring: `scale` (uniform scale and translation, default), `shift` (translation only), `none` |
| `-skip-memo` | ignore reference words in Hancom's memo area (see below) |
| `-reconvert` | also print the candidate HWP/HWPX to PDF with Hancom Office on the remote PC (`OUT/reconv/`, cached like `ref`) and compare that PDF with the reference PDF; needs the same environment as `ref`. `-exe`, `-force` and `-timeout` work as in `ref` |

### How PDFs are compared

1. `pdftotext -bbox` gives every word with its box.
2. Words are put in reading order (page, line from the top, left to right).
   The two PDFs write text in different orders, so the content stream order is
   not used.
3. Same words are paired in order (longest common subsequence). Between two
   paired words the remaining text is paired character by character, so a word
   that one PDF splits (`온·오` against `온·` + `오`) still pairs. Words left over
   on both sides with the same text are then paired by nearest position; these
   are words whose reading order differs (table cells) or that moved to another
   page.
4. A short run (up to 2) of different characters at the same place is a
   *substitution* when the reference character is a symbol-font code (private
   use area, or a code page 1252 byte such as `Ÿ` for a Wingdings bullet) and
   the candidate character is not a letter or digit. A changed letter or digit
   is never a substitution.
5. Word edges are measured where both PDFs have a word boundary at the same
   character: left edges of words that start with paired characters, right
   edges of words that end with them. An edge is the point (x, vertical middle
   of the word box).
6. For each pair of pages a fit of the `-fit` kind is made from the edges
   (least squares, then once more without edges far from the first fit). Pages
   with fewer than 6 edges get translation only. Hancom Office shrinks the body
   to make room for memo balloons, so the scale is needed there.
7. The deviation of an edge is the larger of its horizontal and vertical
   distance after the fit, in candidate points.

With `-skip-memo` a reference word with no partner is ignored when its page was
shrunk (fit scale above 1.01) and, after the fit, it lands right of every
candidate word on the page. That is where Hancom Office prints memos.

### Verdict

A document passes when

- the page counts are the same,
- no paired word is on another page number,
- no word is left without a partner (on either side), and
- the largest edge deviation is at most `-tol`.

With `-reconvert`, the candidate HWP/HWPX printed by Hancom Office must pass the
same test against the reference PDF.

Shown but not part of the verdict: substitutions (`subst`), the fit scale, and
the TXT comparison. TXT is compared line by line after dropping blank lines and
trailing spaces and decoding `&#NNNN;` (Hancom writes characters outside its
code page that way); the two converters lay out tables and spaces in text
differently, so it is a hint, not a test.

`## Differences` in the report lists, for each failed document, the word with
the largest deviation, the first words without a partner and the first
substitutions.

### Limits

- The vertical middle comes from the font's box. When the two PDFs draw a word
  with different fonts, its middle moves even if the baseline does not.
- Text drawn as outlines (Hancom draws chart labels as paths) has no words;
  the candidate's chart labels then show up as words without a partner.
- Hancom Office follows the document's print settings, for example printing
  two pages on one sheet. The candidate does not, so page counts differ.
