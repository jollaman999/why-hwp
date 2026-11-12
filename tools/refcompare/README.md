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
| This machine | Go, `ssh` and `scp` with key login to the remote (`BatchMode=yes` is used, so no password prompt), `pdftotext`, `pdffonts` and `pdftoppm` from poppler-utils |
| This machine, optional | `mutool` from MuPDF (checked with 1.27), used only for documents with Type 3 fonts (see below) |

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
- The conversion is `why-hwp convert -engine hancom` (Hancom COM) built for Windows. By default
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
- A document whose print setting is two pages to a sheet also gets
  `OUT/ref/<name>.oneup.pdf`: the document printed one page to a sheet (see
  *One-up reference*). The setting is `PrintMethod` 4 in `settings.xml` of the
  HWPX form of the document, which is the input itself or, for an hwp input,
  `OUT/ref/<name>.hwpx`. `ref` copies that file with the one value changed to
  0 (every other zip entry is copied as it is stored, in the same order) and
  has Hancom Office print the copy. The copy is not kept, and the PDF is
  cached like every other output.

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
/tmp/refcompare cmp -out OUT -cand CAND [-tol 1.0] [-fit scale] [-skip-memo] [-ink 1.0] [-ink-all] [-reconvert]
```

The table goes to stdout and `OUT/report.md`. Exit code 0 when every document
passes, 1 when one fails or cannot be compared, 2 for bad usage.

| Flag | Meaning |
|------|---------|
| `-tol` | largest allowed word-edge deviation in points (default 1.0) |
| `-fit` | how each reference page is fitted to the candidate page before measuring: `scale` (uniform scale and translation, default), `shift` (translation only), `none` |
| `-skip-memo` | ignore reference words in Hancom's memo area (see below) |
| `-ink` | largest allowed picture difference, in percent, of a page that is judged by its picture and of a word that is looked for in the reference's picture (default 1.0, see *Picture pages* and *Text that the reference draws*) |
| `-ink-all` | also measure the picture difference of every other page and list it under `## Picture difference of every page`; it does not change a verdict |
| `-reconvert` | also print the candidate HWP/HWPX to PDF with Hancom Office on the remote PC (`OUT/reconv/`, cached like `ref`) and compare that PDF with the reference PDF; needs the same environment as `ref`. `-exe`, `-force` and `-timeout` work as in `ref` |

### How PDFs are compared

1. `pdftotext -bbox` gives every word with its box.
2. Words are put in reading order (page, line from the top, left to right).
   The two PDFs write text in different orders, so the content stream order is
   not used.
3. Every page gets a first fit of the `-fit` kind, used only to pair words.
   It is made from the words whose text occurs once on the reference page and
   once on the candidate page, because no repetition can pair those wrongly.
   A page with fewer than 3 such words takes it from the words paired in
   reading order (see the last step of 4).
4. Words are paired one to one, in these steps. Each step takes the words
   that the steps before left without a partner, and the first four pair
   words on the same page number only.
   1. Same text at the same place, nearest first. Two words are at the same
      place when, after the first fit, their boxes overlap sideways and their
      vertical positions are less than half the height of the shorter box
      apart.
   2. Different text at the same place, when it is one word split differently
      (`온·오` against `온·` + `오`): the words that overlap are paired
      character by character, provided every character of the shorter side
      pairs and that side is at least half as long as the other. `5)` on top
      of `7)` is not paired.
   3. Same text anywhere on the page, so that the sum of the distances is
      smallest. These are words that moved, and the distance to their own new
      place is what is measured.
   4. Reading order: same words are paired in order (longest common
      subsequence), and between two paired words the remaining text is paired
      character by character. This is done for the whole document and kept
      where both characters are still free. It pairs a split word that also
      moved.
   5. Words left over on both sides with the same text are paired by nearest
      position on any page; these are words that moved to another page.

   Reading order alone is not enough. It pairs a repeated word (`-` in
   hundreds of table cells, `x` in a formula) with whichever instance comes
   next in the other PDF's order, and the two orders differ wherever a line is
   grouped differently or table cells interleave differently. Such a pair
   measures the distance between two different words: 20 pt in a formula
   whose word edges are all within 1.5 pt, and 169 pt on a page whose picture
   differed by 0.07 %. The report tells how many reference words were paired
   with other words than reading order alone gives (`out of reading order`).

   A word that moved does not hide behind another instance of its text,
   because that instance has its own partner. When a block of lines moves
   by exactly the distance between two instances, an instance may be paired
   with the one that now sits at its place; the instance left over at the end
   of the block is then paired far away, so the document still fails, with
   the largest deviation at another word than the one that moved first.
5. A short run (up to 2) of different characters at the same place is a
   *substitution* when the reference character is a symbol-font code (private
   use area, or a code page 1252 byte such as `Ÿ` for a Wingdings bullet) and
   the candidate character is not a letter or digit. A changed letter or digit
   is never a substitution.
6. Word edges are measured where both PDFs have a word boundary at the same
   character: left edges of words that start with paired characters, right
   edges of words that end with them. An edge is the point (x, vertical middle
   of the word box), or (x, baseline) for the words told under *Type 3 fonts*.
7. For each pair of pages a fit of the `-fit` kind is made from the edges of
   the pairs (those of step 4.5 left out; least squares, then once more
   without edges far from the first result). Pages with fewer than 6 edges get
   translation only. Hancom Office shrinks the body to make room for memo
   balloons, so the scale is needed there.
8. The deviation of an edge is the larger of its horizontal and vertical
   distance after the fit of 7, in candidate points.

With `-skip-memo` a reference word with no partner is ignored when its page was
shrunk (fit scale above 1.01) and, after the fit, it lands right of every
candidate word on the page. That is where Hancom Office prints memos.

### Two pages to a sheet

Hancom Office follows the print settings of a document, and one of them prints
two pages side by side on a landscape sheet. When both PDFs shrink the pages
by the same amount, one fit of the sheet holds both pages and nothing else is
needed. When they do not, each page is scaled about the corner of its own half
of the sheet and no single fit holds both. Hancom Office shrinks every page
once more for memo balloons: in one document of 20 sheets the candidate's
pages were 1.29 times the reference's, so the two halves needed fits 122 pt
apart, and compared sheet by sheet the document read 609 pt off with 88 words
on another page.

Such a document is compared half by half. The left and the right half of
every sheet are two pages: each has its own reading order, its own fit of the
`-fit` kind (the same kinds as for any page), its own picture test, and a word
that went to the other half is a word on another page (`moved`).

A document is compared this way only when all of this holds:

1. Every page of both PDFs is landscape.
2. In both PDFs no word lies across the vertical centre line of its page, and
   there are words on both sides of it.
3. The halves are fitted with scale and translation, and on more than half of
   the sheets whose two halves both have a scale (6 edges or more each) the
   two fits
   - put the top left corners of the two halves half a candidate sheet apart,
     to within 2 pt, which is what printing two pages to a sheet does whatever
     the pages are scaled by, and
   - that is more than 4 pt from where one fit of the sheet, with the mean of
     the two scales, would put them (4 pt is 1 % of half an A4 sheet, the
     same share that marks a memo shrink).

The two distances in 3 are the same when the scale is 1, so both hold only
when the halves are scaled and their corners stay in place. Everything else
is compared as before, one page to a sheet, which is the stricter test:

- A landscape page with two columns whose right column the candidate moves
  (by 5 pt or by 80 pt) has scale 1 in both halves. It fails as before.
- A landscape page that Hancom Office shrank as a whole is held by one fit.
- A two-up document whose pages both PDFs shrink alike is held by one fit of
  the sheet too. A word that goes to the other half is then not `moved`; it
  is hundreds of points off.

In the document above 19 of the 20 sheets met 3 (corners 0.4 to 0.8 pt from
half a sheet apart, 122 to 123 pt from one fit). The other one has a left
half of three words, and its corners are 2.8 pt off.

In the report such a document has `two-up: left <scales>, right <scales>` in
the `scale` column, `pages` counts sheets, and a page is named by its sheet
and half: `p8L` is the left half of sheet 8. The two halves of a sheet are
pages of one document and should have one scale. Where they differ by more
than 0.5 % (the pages of the documents that are not two-up differ by up to
0.33 %), the cell adds `halves differ on sheets ...`, and `## Differences`
gives both scales and the number of edges each was made from. Like every fit
scale this is shown and does not decide the verdict.

For the picture test of a half the sheet is drawn and the other half is
painted white.

A document that has a one-up reference is not compared this way (see the next
section), except for the round trip of `-reconvert`.

### One-up reference

Hancom Office's two-up print is not always the whole document. In the
document of 20 sheets above, its PDF lacks 885 table cells: on ten pages the
right columns of a wide table are cut off in the right half of the sheet. A
candidate that draws those pages in full has 885 words without a partner and
cannot pass, however right it is. Printed one page to a sheet, the same
document has 40 pages and every cell.

So when `OUT/ref/<name>.oneup.pdf` exists (see *ref*), and `manifest.json`
records it for the same input as `<name>.pdf`, the candidate PDF is compared
with it:

- A candidate that prints two pages to a sheet (every page is landscape, no
  word lies across the vertical centre line of its page, and there are words
  on both sides of it) is cut into halves. Half *n* is compared with page *n*
  of the one-up reference just as two pages are compared: pairing, a fit of
  the `-fit` kind for each page, deviations in candidate points, drawn text.
  The fit takes up what the candidate shrank the page by to put two on a sheet
  (0.7085 to 0.7103 in the document above) and what Hancom Office shrank a
  reference page by for memos. When the reference has one page less than the
  candidate has halves and the last half has no word, that half is not a
  page; the sheet is drawn, and the document fails when the half has ink.
- Any other candidate is compared with the one-up reference page by page.
- The two-up reference `<name>.pdf` decides only the sheets: the candidate
  must have as many, each of the same width and height to within `-tol`
  points. A candidate that does not follow the print setting fails here even
  when all its words are in place.

In the report such a document has `one-up ref; sheets <reference>/<candidate>`
after the page counts, which are those of the one-up reference and of the
candidate's halves, and `halves: left <scales>, right <scales>` in the `scale`
column. A page is named by its number in the document. `## Differences` says
which reference was used and why.

The round trip of `-reconvert` is compared with the two-up reference as
before: both PDFs are then printed two-up by Hancom Office and are cut alike.

### Type 3 fonts

Hancom Office writes some fonts into the PDF as Type 3 fonts. Such a font
states no ascent and descent, and `pdftotext` makes up a word box from a font
size it guesses: for one 11 pt line it gave boxes 30 pt and 7 pt high. The
middle of such a box is not where the glyphs are, although the left and right
edges are right: words whose baselines are within 1 pt were reported 3.4 to
4.7 pt off.

When `pdffonts` lists a Type 3 font in the reference or the candidate PDF,
`mutool draw -F stext` is run on both PDFs. It gives the origin of every
glyph, which is a point on the baseline, and the font of every glyph. A word
takes the baseline of its first glyph: the glyph with the word's first
character at the left edge of the word box. A tall Type 3 box can hold that
character on the line above too, so the place of the baseline in the box is
learned for each font from the words with one such glyph, and the other
words take the glyph nearest to it.

Two kinds of pairs are then compared at the baselines and not at the box
middles. The horizontal position and the fit are as for every other pair.

- One of the two words holds a Type 3 glyph.
- The two words are not the same run of characters. `pdftotext` starts a new
  word where a Type 3 font begins, so the reference has `(` and `abc` where
  the candidate has `(abc`; the two boxes then come from different fonts.

Every other pair is compared at the box middles. In a document without Type 3
fonts that is every pair, and `mutool` is not run. The `type 3` column of the
report tells how many edges were measured at the baseline.

Without `mutool`, or when `mutool` shows no text in the Type 3 fonts that
`pdffonts` lists, the Type 3 words are compared at the box middles too and
the `type 3` column says `box middle:` and why. Expect vertical deviations of
several points in such a document that are not real.

### Picture pages

Hancom Office draws the text of a chart as outlines, so its PDF has no words
there. A page where the reference PDF has no word at all (a chart, a picture,
an empty page) is a *picture page*. The candidate's words on it cannot be
paired; they are not counted as unmatched, and the page is judged by drawing
it. A page where only the candidate has no word is not a picture page: the
candidate lost the text, and the reference words are unmatched.

1. `pdftoppm` draws the page of both PDFs in color at 4 pixels per point.
2. The *outline* of a drawn page is the darker side of every edge: the pixels
   that differ from a pixel up to 2 pixels to the left, right, top or bottom
   by 64 or more (of 255) in one color channel. Color is used because two pie
   slices can differ in hue and hardly in brightness.
3. An outline pixel is *alone* when the other page has no outline pixel within
   `-tol` points of it, horizontally and vertically (as for word edges; 4
   pixels at the default). For the other page a step of 32 is enough, so an
   edge near the limit is not alone because it is 65 here and 63 there.
4. The picture difference is the share of alone pixels in the outline, taken
   for both pages; the larger share counts. Both pages blank is 0, ink on one
   page only is 100 %.
5. The page passes when the difference is at most `-ink` percent (default 1).
   A picture page has no paired words and so no fit: the two pages are
   compared as they are.

The verdict of such a document is written `pass (picture)` or `FAIL (picture)`
and the `picture` column gives the difference of each picture page, so it
cannot be taken for a word comparison. If the page cannot be drawn, the
document fails.

Where the numbers come from (18 documents, candidate made with the fonts
Hancom Office used, `-ink-all`):

- Step 64: `pdftoppm` draws things that are not in the PDF and differ between
  the two producers: seams between the tiles Hancom cuts a pie slice into
  (step 1) and a faint edge around a picture with a transparent border (step
  36; `mutool` does not draw it). A picture that one PDF holds as JPEG and the
  other without loss differs in its grain. At step 32 these made a page with
  two pictures in the right place read 2.0 %; at 64 it reads 0.0 %.
- Limit 1 %: of the 47 pages that have words and pass, 46 read 0.41 % or less
  (45 of them below 0.1 %); the other one holds memo balloons that only the
  reference draws (44 %). The two picture pages read 0.39 % and 0.00 %. Ten
  pages (those two and eight with words), moved on purpose by 1.25 pt to the
  right or down, read 1.2 % to 24 %; moved by 2 pt, 11 % to 45 %. Pages of a
  document with another page count read 57 % or more.

The share is not a maximum. A few words that are 1.8 to 3.5 pt off on a full
text page read 0.03 % to 0.49 %, below the limit. That is why pages with words
are judged by their words and never by the picture.

### Text that the reference draws

Hancom Office draws the text of a chart as outlines also on a page that has
other words, and a chart that the document holds as a picture has no text at
all. The candidate has words there, and they have no partner. Such a word may
be just what the reference shows, or it may be a word the candidate made up or
put in the wrong place. So it is not ignored: it is looked for in the picture
of the reference, and only a word whose shape is found at its place is taken
out of the unmatched words.

1. For every candidate word without a partner `pdftoppm` draws both PDFs
   around the word at 8 pixels per point. The reference is drawn where the fit
   of the word's page (step 7 of *How PDFs are compared*) puts the word box.
2. The outline is that of *Picture pages*, step 2. On both pages the outline
   pixels inside the word box are taken.
3. The reference may be moved by up to `-tol` points to either side,
   horizontally and vertically, as a word edge may. The shifts are tried in
   steps of a pixel, the smallest first. At a shift the two shapes *lie on
   each other* when both pages have outline in the box and at most `-ink`
   percent of either outline has no outline pixel of the other page within a
   quarter of a point.
4. The word is *drawn* when the shapes lie on each other at one of these
   shifts, and at none of the four shifts that are `-tol` and half a point
   further to the left, right, top or bottom. Where they do there too, the box
   is so full of ink (the middle of a radar chart, a hatched area) that any
   shape lies on it, and nothing is known about the word.
5. A word is not looked for when another word is at the same place (as in
   step 4.1 of the pairing): another candidate word, or a reference word
   without a partner. The ink there is that word's. So a candidate that
   prints a word twice still has a word without a partner, and so has one
   that prints another text than the reference (`O` where the reference PDF
   reports a code of a formula font). A word on a page that only the
   candidate has is not looked for, and a word on a picture page belongs to
   the picture test.

A drawn word is not counted as unmatched. The `drawn` column of the report
gives their number, in all and for each page, with the largest shift at which
one of them was found (the shape itself can be a quarter of a point further).
`## Differences` repeats it for a failed document, and the page table of
`-ink-all` has a `drawn` column.

These stay unmatched: a word where the reference has no ink, a shape that is
further away than `-tol` and the quarter point, another shape at the place
(another digit, a rule, a box), and a word that is too faint in one of the
PDFs to have an outline.

Where the numbers come from (one document of 20 two-up sheets with words and
charts, whose candidate has 1257 words without a partner against the two-up
reference; against its one-up reference, see *One-up reference*, the 885 table
cells have partners and all 371 chart labels are drawn):

- 885 of them are table cells that the reference PDF does not have: on ten
  of the pages that Hancom Office shrank for memos it left out the right
  columns of the tables. The reference has no ink there and all 885 stay
  unmatched.
- 371 are the labels of charts on five pages, which the reference PDF holds
  as pictures. 315 are drawn (117 of 117, 103 of 105, 41 of 41, 35 of 36 and
  19 of 72). Of the others, 53 are on
  a page whose only paired words are the three of its page number: the fit
  made from them is more than 2 pt off at the top of the page (with the scale
  of the other pages all 72 are drawn). Two are a `0` in the middle of a radar chart
  (step 4), and one is drawn bold in the reference only (1.6 %).
- One is a digit in a table. The reference has it as a glyph of a Type 3
  font, and `pdftotext` does not report it. It is drawn.
- Resolution and the quarter point: the labels are about 3.5 pt in the
  reference, where a digit is 8 by 10 pixels at 4 pixels per point. Tested as
  a picture page is, with `-tol` as the only tolerance and no shift, 237 of
  240 labels also lay on the reference's picture of *another* label of the
  same size (`10` on `12`), and 279 of 309 still lay on their own picture
  moved by 1.25 pt. With shifts and a quarter point at 4 pixels per point,
  264 of 300 labels were found and 13 of 231 lay on another label; at 8
  pixels per point 299 of 300 and 5 of 231. An eighth of a point found 93 of
  300: the two producers do not draw a glyph alike (Hancom Office writes it
  as a path, in another size).
- Moved by 1.5, 2 and 3 pt to each of the four sides from where it was found,
  none of the 316 words is drawn (1264 tries each). Laid on the reference's
  picture of the nearest other label of the same size and length, 5 of 245
  are (`28` on `26`, `2022년` on `2021년`, `2,000,000` on `1,000,000`).

### Verdict

A document passes when

- the page counts are the same,
- no paired word is on another page number,
- no word is left without a partner (on either side; the candidate's words on
  picture pages and those the reference draws are not counted),
- the largest edge deviation is at most `-tol`, and
- every picture page has a picture difference of at most `-ink` percent.

For a document with a one-up reference the pages are those of the one-up
reference, and the candidate must also have the sheets of the two-up
reference: as many, of the same size to within `-tol` points.

With `-reconvert`, the candidate HWP/HWPX printed by Hancom Office must pass the
same test against the reference PDF.

Shown but not part of the verdict: substitutions (`subst`), the fit scale
(also when the two halves of a two-up sheet differ in it), and the TXT
comparison. TXT is compared line by line after dropping blank lines and
trailing spaces and decoding `&#NNNN;` (Hancom writes characters outside its
code page that way); the two converters lay out tables and spaces in text
differently, so it is a hint, not a test.

`## Differences` in the report lists, for each failed document, the word with
the largest deviation, the first words without a partner and the first
substitutions.

### Limits

- The vertical middle comes from the font's box. When the two PDFs draw a word
  with different fonts, its middle moves even if the baseline does not.
  Baselines are used only in documents with Type 3 fonts.
- The picture comparison sees shapes, not text. Another font for a chart label
  changes the outline, and a wrong character with a similar shape does not.
  Edges fainter than a quarter of the color range (a light highlight, a light
  gray rule) are not measured.
- The search for drawn text sees shapes too. A label that differs from the
  reference's in one character of several can be taken for drawn (5 of 245
  above), and so is the same text in another weight: bold on regular is less
  than a quarter of a point apart in a 3.5 pt label. A label in another
  typeface is not found: of the 371 labels above, 200 were with a candidate
  made with other fonts.
- Drawn text is looked for where the fit of the page puts it, and the fit is
  made from the paired words. On a page with a few words in one line the fit
  is poor far from them, and drawn text there is not found.
- Text that the candidate draws and the reference has as words is not looked
  for. Those are reference words without a partner.
- Hancom Office follows the document's print settings, for example printing
  two pages on one sheet. A candidate that does not has another page count,
  or with a one-up reference another number of sheets.
- Only the print setting two pages to a sheet (`PrintMethod` 4) gets a
  one-up reference. A reference made before `ref` knew of it has none until
  `ref` is run again.
- A half that is compared with a page of the one-up reference is measured on
  the sheet, in candidate points, where the page is about 0.71 of its size:
  1 pt there is 1.4 pt on the page. A candidate of one page to a sheet is
  measured at full size.
- A page of the one-up reference without words has no fit, and a half of a
  candidate sheet cannot be laid on it without one. Such a page is reported
  as not compared and the document fails.
- With a one-up reference the two-up reference is not compared word by word,
  so where on the sheet the candidate puts a half is not measured: each half
  has its own fit.
- Two pages to a sheet are told from the words alone (see *Two pages to a
  sheet*). Only two portrait pages side by side on a landscape sheet are
  handled. A two-up sheet with a word across its centre line, or whose halves
  are not scaled, is compared as one page.
- A half gets its own fit like every page, so a half that is moved or scaled
  as a whole is not a deviation.
