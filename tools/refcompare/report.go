package main

import (
	"fmt"
	"strings"
)

func renderReport(rows []row, o cmpOptions, candDir string, reconvert bool) string {
	var b strings.Builder
	pass, byPicture := 0, 0
	for _, r := range rows {
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
	fmt.Fprintf(&b, "Fit: %s. Memo words skipped: %v. Substituted characters (same place, different code point,\n", o.Fit, o.SkipMemo)
	fmt.Fprintf(&b, "usually symbol fonts) and TXT are shown but do not decide the verdict.\n\n")
	fmt.Fprintf(&b, "%d of %d documents pass", pass, len(rows))
	if byPicture > 0 {
		fmt.Fprintf(&b, " (%d of them by picture)", byPicture)
	}
	b.WriteString(".\n\n")

	b.WriteString("| document | pages ref/cand | max dev pt | unmatched ref/cand | moved | subst | scale | memo skipped | txt lines ref-only/cand-only | round trip | type 3 | picture | verdict |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
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
			fmt.Fprintf(&b, "| %s | %s | | | | | | | %s | %s | | | %s |\n", cell(r.Name), cell(r.Err), txtCell(r), cell(rt), verdict)
			continue
		}
		p := r.PDF
		fmt.Fprintf(&b, "| %s | %d/%d | %.2f | %d/%d | %d | %d | %s | %d | %s | %s | %s | %s | %s |\n",
			cell(r.Name), p.RefPages, p.CandPages, p.MaxDev, p.RefOnly, p.CandOnly, p.PageMoves,
			p.Substituted, scaleCell(*p), p.MemoSkipped, txtCell(r), cell(rt), cell(type3Cell(r)), cell(pictureCell(*p)), verdict)
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
	fmt.Fprintf(b, "- %s: %d/%d pages, %d/%d words, %d matched (%d out of reading order), %d on another page, %d edges measured\n",
		what, p.RefPages, p.CandPages, p.RefWords, p.CandWords, p.Matched, p.Reordered, p.PageMoves, p.Edges)
	if p.MaxDev > o.Tol {
		fmt.Fprintf(b, "- %s: largest deviation %.2f pt at %q (ref page %d)\n", what, p.MaxDev, p.Worst.Text, p.Worst.Page)
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
			fmt.Fprintf(b, "- %s: page %d is judged by its picture (%s) and its picture could not be compared: %s\n", what, i+1, wordSide(pg), pg.InkErr)
		default:
			fmt.Fprintf(b, "- %s: page %d is judged by its picture (%s): %.2f %% of the outline is more than %.2f pt off, limit %.2f %%\n",
				what, i+1, wordSide(pg), 100*pg.Ink, o.Tol, 100*o.InkLimit)
		}
	}
	if len(p.RefOnlySample) > 0 {
		fmt.Fprintf(b, "- %s: only in ref: %s\n", what, sample(p.RefOnlySample, p.RefOnly))
	}
	if len(p.CandOnlySample) > 0 {
		fmt.Fprintf(b, "- %s: only in cand: %s\n", what, sample(p.CandOnlySample, p.CandOnly))
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
			parts = append(parts, fmt.Sprintf("p%d not compared", i+1))
		default:
			parts = append(parts, fmt.Sprintf("p%d %.2f %%", i+1, 100*pg.Ink))
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

// inkTable lists the picture difference of every page (-ink-all), next to
// what the word comparison found on the page.
func inkTable(b *strings.Builder, rows []row, o cmpOptions) {
	b.WriteString("\n## Picture difference of every page\n\n")
	b.WriteString("Only the pages marked picture decide a verdict. The other pages are drawn after the fit of their words.\n\n")
	b.WriteString("| document | page | words ref/cand | unmatched ref/cand | max dev pt | picture diff % | judged by |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
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
			fmt.Fprintf(b, "| %s | %d | %d/%d | %d/%d | %.2f | %s | %s |\n", cell(r.Name), i+1,
				pg.RefWords, pg.CandWords, pg.RefOnly, pg.CandOnly, pg.MaxDev, cell(ink), by)
		}
	}
}

func sample(ws []located, total int) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = fmt.Sprintf("%q p%d", clip(w.Text), w.Page)
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

func scaleCell(p pdfResult) string {
	if p.Matched == 0 {
		return "-"
	}
	if fmt.Sprintf("%.4f", p.ScaleMin) == fmt.Sprintf("%.4f", p.ScaleMax) {
		return fmt.Sprintf("%.4f", p.ScaleMin)
	}
	return fmt.Sprintf("%.4f-%.4f", p.ScaleMin, p.ScaleMax)
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
