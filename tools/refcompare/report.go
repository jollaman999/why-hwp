package main

import (
	"fmt"
	"strings"
)

func renderReport(rows []row, o cmpOptions, candDir string, reconvert bool) string {
	var b strings.Builder
	pass, byPicture, oneUps := 0, 0, 0
	for _, r := range rows {
		if r.PDF != nil && r.PDF.OneUp != nil {
			oneUps++
		}
		if r.Pass {
			pass++
			if r.judgedByPicture() {
				byPicture++
			}
		}
	}
	fmt.Fprintf(&b, "# refcompare report\n\n")
	fmt.Fprintf(&b, "Candidate: `%s`  \n", candDir)
	fmt.Fprintf(&b, "Pass: same page count, no word on another page, no unmatched word, max word-edge deviation <= %.2f pt", o.Tol)
	if reconvert {
		b.WriteString(", and the same for the candidate HWP/HWPX printed by Hancom Office")
	}
	fmt.Fprintf(&b, ".  \nA page where the reference has no word is judged by its picture: at most %.2f %% of the outline\n", 100*o.InkLimit)
	fmt.Fprintf(&b, "of one page may be more than %.2f pt from the outline of the other. Such a verdict is marked (picture).  \n", o.Tol)
	fmt.Fprintf(&b, "A candidate word without a partner is not unmatched when the reference draws its shape within %.2f pt of the place\n", o.Tol)
	fmt.Fprintf(&b, "(chart text, which Hancom Office draws as outlines): at most %.2f %% of the outline in the word box may be more than\n", 100*o.InkLimit)
	fmt.Fprintf(&b, "%.2f pt off once the shapes are laid on each other. The drawn column counts these words.  \n", float64(drawnSlack)/drawnPxPerPt)
	if oneUps > 0 {
		b.WriteString("A document marked one-up ref prints two pages to a sheet. Its words are compared with Hancom Office's print of\n")
		b.WriteString("the document one page to a sheet (ref/<name>.oneup.pdf), because Hancom Office's two-up print can leave out parts\n")
		b.WriteString("of a page. Each half of a candidate sheet is compared with one page of it. The two-up reference decides the\n")
		fmt.Fprintf(&b, "number of sheets and their size (to within %.2f pt).  \n", o.Tol)
	}
	fmt.Fprintf(&b, "Fit: %s. Memo words skipped: %v. Substituted characters (same place, different code point,\n", o.Fit, o.SkipMemo)
	fmt.Fprintf(&b, "usually symbol fonts) and TXT are shown but do not decide the verdict.\n\n")
	fmt.Fprintf(&b, "%d of %d documents pass", pass, len(rows))
	if byPicture > 0 {
		fmt.Fprintf(&b, " (%d of them by picture)", byPicture)
	}
	b.WriteString(".\n\n")

	b.WriteString("| document | pages ref/cand | max dev pt | unmatched ref/cand | moved | subst | scale | memo skipped | txt lines ref-only/cand-only | round trip | type 3 | picture | drawn | verdict |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		verdict := "FAIL"
		if r.Pass {
			verdict = "pass"
		}
		if r.judgedByPicture() {
			verdict += " (picture)"
		}
		rt := "-"
		switch {
		case r.RT != nil:
			rt = shortResult(*r.RT, o)
		case r.RTNote != "":
			rt = r.RTNote
		}
		if r.PDF == nil {
			fmt.Fprintf(&b, "| %s | %s | | | | | | | %s | %s | | | | %s |\n", cell(r.Name), cell(r.Err), txtCell(r), cell(rt), verdict)
			continue
		}
		p := r.PDF
		fmt.Fprintf(&b, "| %s | %s | %.2f | %d/%d | %d | %d | %s | %d | %s | %s | %s | %s | %s | %s |\n",
			cell(r.Name), cell(pagesCell(*p, o)), p.MaxDev, p.RefOnly, p.CandOnly, p.PageMoves,
			p.Substituted, scaleCell(*p), p.MemoSkipped, txtCell(r), cell(rt), cell(type3Cell(r)), cell(pictureCell(*p)), cell(drawnCell(*p)), verdict)
	}
	if o.InkAll {
		inkTable(&b, rows, o)
	}

	var details strings.Builder
	for _, r := range rows {
		if r.Pass {
			continue
		}
		fmt.Fprintf(&details, "\n### %s\n\n", r.Name)
		if r.PDF == nil {
			fmt.Fprintf(&details, "- PDF: %s\n", r.Err)
		} else {
			detailLines(&details, "PDF", *r.PDF, o)
		}
		if r.BaseNote != "" {
			fmt.Fprintf(&details, "- Type 3 words are measured at the box middle: %s\n", r.BaseNote)
		}
		if r.RT != nil && !r.RT.pass(o) {
			detailLines(&details, "round trip", *r.RT, o)
		} else if r.RTNote != "" {
			fmt.Fprintf(&details, "- round trip: %s\n", r.RTNote)
		}
		if r.Txt != nil && !r.Txt.same() {
			fmt.Fprintf(&details, "- TXT: first ref-only line %q, first cand-only line %q\n",
				clip(r.Txt.FirstRefOnly), clip(r.Txt.FirstCandOnly))
		}
	}
	if details.Len() > 0 {
		b.WriteString("\n## Differences\n")
		b.WriteString(details.String())
	}
	return b.String()
}

func detailLines(b *strings.Builder, what string, p pdfResult, o cmpOptions) {
	unit := "pages"
	if p.TwoUp {
		unit = "sheets"
	}
	fmt.Fprintf(b, "- %s: %d/%d %s, %d/%d words, %d matched (%d out of reading order), %d on another page, %d edges measured\n",
		what, p.RefPages, p.CandPages, unit, p.RefWords, p.CandWords, p.Matched, p.Reordered, p.PageMoves, p.Edges)
	if u := p.OneUp; u != nil {
		fmt.Fprintf(b, "- %s: compared with the one-up reference (Hancom Office's print of the document one page to a sheet), because the document prints two pages to a sheet and Hancom Office's two-up print can leave out parts of a page\n", what)
		if u.Halved {
			h := p.halfScales()
			fmt.Fprintf(b, "- %s: the candidate prints two pages to a sheet; each half is compared with a page of the one-up reference: scale of the left halves %s, of the right halves %s\n",
				what, scaleRange(h.LeftMin, h.LeftMax, h.Left), scaleRange(h.RightMin, h.RightMax, h.Right))
		} else {
			fmt.Fprintf(b, "- %s: the candidate does not print two pages to a sheet; its pages are compared with the pages of the one-up reference\n", what)
		}
		fmt.Fprintf(b, "- %s: %s\n", what, sheetsLine(u, o))
		if u.Trimmed {
			if u.HalfErr == "" {
				fmt.Fprintf(b, "- %s: the right half of the candidate's last sheet is empty and is not a page\n", what)
			} else {
				fmt.Fprintf(b, "- %s: the right half of the candidate's last sheet is left out as empty, but %s\n", what, u.HalfErr)
			}
		}
	}
	if p.TwoUp {
		h := p.halfScales()
		fmt.Fprintf(b, "- %s: two pages to a sheet, compared half by half (L left, R right): scale of the left halves %s, of the right halves %s\n",
			what, scaleRange(h.LeftMin, h.LeftMax, h.Left), scaleRange(h.RightMin, h.RightMax, h.Right))
		for _, d := range h.Differ {
			fmt.Fprintf(b, "- %s: the halves of sheet %d differ in scale: left %.4f from %d edges, right %.4f from %d edges\n",
				what, d.Sheet, d.Left, d.LeftEdges, d.Right, d.RightEdges)
		}
	}
	if p.MaxDev > o.Tol {
		fmt.Fprintf(b, "- %s: largest deviation %.2f pt at %q (ref page %s)\n", what, p.MaxDev, p.Worst.Text, pageLabel(p.TwoUp, p.Worst.Page-1))
	}
	if p.BaseEdges > 0 || p.BoxEdges > 0 {
		fmt.Fprintf(b, "- %s: %d edges measured at the baseline", what, p.BaseEdges)
		if p.BoxEdges > 0 {
			fmt.Fprintf(b, ", %d edges of Type 3 words at the box middle (baseline not found)", p.BoxEdges)
		}
		b.WriteString("\n")
	}
	for i, pg := range p.Page {
		switch {
		case !pg.Picture:
		case !pg.InkDone:
			fmt.Fprintf(b, "- %s: page %s is judged by its picture (%s) and its picture could not be compared: %s\n", what, pageLabel(p.TwoUp, i), wordSide(pg), pg.InkErr)
		default:
			fmt.Fprintf(b, "- %s: page %s is judged by its picture (%s): %.2f %% of the outline is more than %.2f pt off, limit %.2f %%\n",
				what, pageLabel(p.TwoUp, i), wordSide(pg), 100*pg.Ink, o.Tol, 100*o.InkLimit)
		}
	}
	if p.DrawnWords > 0 {
		fmt.Fprintf(b, "- %s: %s without a partner in cand drawn in the reference and not counted: %s\n",
			what, plural(p.DrawnWords, "word"), drawnPages(p))
	}
	for i, pg := range p.Page {
		if pg.DrawnErr != "" {
			fmt.Fprintf(b, "- %s: page %s: the words without a partner could not be looked for in the reference picture: %s\n", what, pageLabel(p.TwoUp, i), pg.DrawnErr)
		}
	}
	if len(p.RefOnlySample) > 0 {
		fmt.Fprintf(b, "- %s: only in ref: %s\n", what, sample(p.RefOnlySample, p.RefOnly, p.TwoUp))
	}
	if len(p.CandOnlySample) > 0 {
		fmt.Fprintf(b, "- %s: only in cand: %s\n", what, sample(p.CandOnlySample, p.CandOnly, p.TwoUp))
	}
	if len(p.SubstSample) > 0 {
		s := strings.Join(p.SubstSample, ", ")
		if p.Substituted > len(p.SubstSample) {
			s += fmt.Sprintf(" (+%d more)", p.Substituted-len(p.SubstSample))
		}
		fmt.Fprintf(b, "- %s: substituted ref>cand: %s\n", what, s)
	}
}

// wordSide tells whether the candidate has words on a picture page, and
// how many of them have no partner.
func wordSide(p pageStat) string {
	if p.CandWords > 0 {
		return fmt.Sprintf("words in cand only, %d not paired", p.CandOnly)
	}
	return "no words"
}

// judgedByPicture reports whether the verdict of a document depends on a
// picture comparison.
func (r row) judgedByPicture() bool {
	return (r.PDF != nil && len(r.PDF.picturePages()) > 0) || (r.RT != nil && len(r.RT.picturePages()) > 0)
}

// type3Cell tells how the Type 3 words of a document were measured.
func type3Cell(r row) string {
	p := r.PDF
	switch {
	case r.BaseNote != "":
		return "box middle: " + r.BaseNote
	case p.BaseEdges == 0 && p.BoxEdges == 0:
		return "-"
	case p.BoxEdges == 0:
		return fmt.Sprintf("%d edges at baseline", p.BaseEdges)
	}
	return fmt.Sprintf("%d edges at baseline, %d at box middle", p.BaseEdges, p.BoxEdges)
}

// pictureCell lists the picture pages of a document with their picture
// difference.
func pictureCell(p pdfResult) string {
	var parts []string
	for i, pg := range p.Page {
		switch {
		case !pg.Picture:
		case !pg.InkDone:
			parts = append(parts, fmt.Sprintf("p%s not compared", pageLabel(p.TwoUp, i)))
		default:
			parts = append(parts, fmt.Sprintf("p%s %.2f %%", pageLabel(p.TwoUp, i), 100*pg.Ink))
		}
		if pg.Picture && pg.CandOnly > 0 {
			parts[len(parts)-1] += fmt.Sprintf(", %d words not paired", pg.CandOnly)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// drawnCell tells how many candidate words without a partner the reference
// draws, in all and on each page (see drawnShift).
func drawnCell(p pdfResult) string {
	if p.DrawnWords == 0 {
		return "-"
	}
	return fmt.Sprintf("%s: %s", plural(p.DrawnWords, "word"), drawnPages(p))
}

// drawnPages lists the pages with drawn words: the page, the number of
// words and the largest of the shifts at which they were found.
func drawnPages(p pdfResult) string {
	var parts []string
	for i, pg := range p.Page {
		if pg.Drawn > 0 {
			parts = append(parts, fmt.Sprintf("p%s %d (shift up to %.2f pt)", pageLabel(p.TwoUp, i), pg.Drawn, pg.DrawnShift))
		}
	}
	return strings.Join(parts, ", ")
}

func plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// inkTable lists the picture difference of every page (-ink-all), next to
// what the word comparison found on the page.
func inkTable(b *strings.Builder, rows []row, o cmpOptions) {
	b.WriteString("\n## Picture difference of every page\n\n")
	b.WriteString("Only the pages marked picture decide a verdict. The other pages are drawn after the fit of their words.\n\n")
	b.WriteString("| document | page | words ref/cand | unmatched ref/cand | drawn | max dev pt | picture diff % | judged by |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		if r.PDF == nil {
			continue
		}
		for i, pg := range r.PDF.Page {
			by, ink := "words", "not compared: "+pg.InkErr
			if pg.Picture {
				by = "picture"
			}
			if pg.InkDone {
				ink = fmt.Sprintf("%.2f", 100*pg.Ink)
			}
			fmt.Fprintf(b, "| %s | %s | %d/%d | %d/%d | %d | %.2f | %s | %s |\n", cell(r.Name), pageLabel(r.PDF.TwoUp, i),
				pg.RefWords, pg.CandWords, pg.RefOnly, pg.CandOnly, pg.Drawn, pg.MaxDev, cell(ink), by)
		}
	}
}

func sample(ws []located, total int, twoUp bool) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = fmt.Sprintf("%q p%s", clip(w.Text), pageLabel(twoUp, w.Page-1))
	}
	s := strings.Join(parts, ", ")
	if total > len(ws) {
		s += fmt.Sprintf(" (+%d more)", total-len(ws))
	}
	return s
}

func shortResult(p pdfResult, o cmpOptions) string {
	v := "pass"
	if !p.pass(o) {
		v = "FAIL"
	}
	return fmt.Sprintf("%s: %d/%d p, %.2f pt, %d/%d unmatched, %d moved", v, p.RefPages, p.CandPages, p.MaxDev, p.RefOnly, p.CandOnly, p.PageMoves)
}

// pagesCell gives the page counts of a comparison. For a document compared
// with its one-up reference it says so and adds the sheets of the two-up
// reference and of the candidate.
func pagesCell(p pdfResult, o cmpOptions) string {
	s := fmt.Sprintf("%d/%d", p.RefPages, p.CandPages)
	u := p.OneUp
	if u == nil {
		return s
	}
	s += fmt.Sprintf(", one-up ref; sheets %d/%d", u.RefSheets, u.CandSheets)
	if u.SizeDiff > o.Tol {
		s += fmt.Sprintf(", sheet %d is %.2f pt off in size", u.SizeSheet, u.SizeDiff)
	}
	if u.HalfErr != "" {
		s += ", last half not empty"
	}
	return s
}

// sheetsLine compares the sheets of the candidate with those of the two-up
// reference.
func sheetsLine(u *oneUp, o cmpOptions) string {
	s := fmt.Sprintf("sheets of the two-up reference and of the candidate: %d/%d", u.RefSheets, u.CandSheets)
	switch {
	case u.SizeSheet == 0:
	case u.SizeDiff > o.Tol:
		s += fmt.Sprintf("; sheet %d is %.2f x %.2f pt in the reference and %.2f x %.2f pt in the candidate, more than %.2f pt apart",
			u.SizeSheet, u.RefSize.W, u.RefSize.H, u.CandSize.W, u.CandSize.H, o.Tol)
	default:
		s += fmt.Sprintf("; sizes differ by up to %.2f pt (sheet %d)", u.SizeDiff, u.SizeSheet)
	}
	if u.RefSheets != u.CandSheets {
		s += "; the candidate does not print the sheets Hancom Office prints"
	}
	return s
}

func scaleCell(p pdfResult) string {
	if p.TwoUp {
		return twoUpCell(p)
	}
	if p.OneUp != nil && p.OneUp.Halved {
		h := p.halfScales()
		return fmt.Sprintf("halves: left %s, right %s", scaleRange(h.LeftMin, h.LeftMax, h.Left), scaleRange(h.RightMin, h.RightMax, h.Right))
	}
	if p.Matched == 0 {
		return "-"
	}
	return scaleRange(p.ScaleMin, p.ScaleMax, 1)
}

// scaleRange writes the smallest and the largest of n scales.
func scaleRange(lo, hi float64, n int) string {
	if n == 0 {
		return "-"
	}
	if fmt.Sprintf("%.4f", lo) == fmt.Sprintf("%.4f", hi) {
		return fmt.Sprintf("%.4f", lo)
	}
	return fmt.Sprintf("%.4f-%.4f", lo, hi)
}

// twoUpCell tells that a document was compared half by half, the fit
// scales of its left and right halves, and the sheets whose two halves
// differ in scale.
func twoUpCell(p pdfResult) string {
	h := p.halfScales()
	s := fmt.Sprintf("two-up: left %s, right %s", scaleRange(h.LeftMin, h.LeftMax, h.Left), scaleRange(h.RightMin, h.RightMax, h.Right))
	if len(h.Differ) == 0 {
		return s
	}
	s += "; halves differ on sheet"
	if len(h.Differ) > 1 {
		s += "s"
	}
	for i, d := range h.Differ {
		if i == sampleWords {
			s += fmt.Sprintf(" (+%d more)", len(h.Differ)-i)
			break
		}
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf(" %d", d.Sheet)
	}
	return s
}

func txtCell(r row) string {
	if r.Txt == nil {
		return cell(r.TxtNote)
	}
	return fmt.Sprintf("%d/%d of %d/%d", r.Txt.RefOnly, r.Txt.CandOnly, r.Txt.RefLines, r.Txt.CandLines)
}

func clip(s string) string {
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:40]) + "..."
	}
	return s
}

// cell makes s safe inside a Markdown table cell.
func cell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}
