package main

import (
	"fmt"
	"strings"
)

func renderReport(rows []row, o cmpOptions, candDir string, reconvert bool) string {
	var b strings.Builder
	pass := 0
	for _, r := range rows {
		if r.Pass {
			pass++
		}
	}
	fmt.Fprintf(&b, "# refcompare report\n\n")
	fmt.Fprintf(&b, "Candidate: `%s`  \n", candDir)
	fmt.Fprintf(&b, "Pass: same page count, no word on another page, no unmatched word, max word-edge deviation <= %.2f pt", o.Tol)
	if reconvert {
		b.WriteString(", and the same for the candidate HWP/HWPX printed by Hancom Office")
	}
	fmt.Fprintf(&b, ".  \nFit: %s. Memo words skipped: %v. Substituted characters (same place, different code point,\n", o.Fit, o.SkipMemo)
	fmt.Fprintf(&b, "usually symbol fonts) and TXT are shown but do not decide the verdict.\n\n")
	fmt.Fprintf(&b, "%d of %d documents pass.\n\n", pass, len(rows))

	b.WriteString("| document | pages ref/cand | max dev pt | unmatched ref/cand | moved | subst | scale | memo skipped | txt lines ref-only/cand-only | round trip | verdict |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		verdict := "FAIL"
		if r.Pass {
			verdict = "pass"
		}
		rt := "-"
		switch {
		case r.RT != nil:
			rt = shortResult(*r.RT, o)
		case r.RTNote != "":
			rt = r.RTNote
		}
		if r.PDF == nil {
			fmt.Fprintf(&b, "| %s | %s | | | | | | | %s | %s | %s |\n", cell(r.Name), cell(r.Err), txtCell(r), cell(rt), verdict)
			continue
		}
		p := r.PDF
		fmt.Fprintf(&b, "| %s | %d/%d | %.2f | %d/%d | %d | %d | %s | %d | %s | %s | %s |\n",
			cell(r.Name), p.RefPages, p.CandPages, p.MaxDev, p.RefOnly, p.CandOnly, p.PageMoves,
			p.Substituted, scaleCell(*p), p.MemoSkipped, txtCell(r), cell(rt), verdict)
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
