package verify

import (
	"fmt"
	"io"
	"strings"
)

var lineEscaper = strings.NewReplacer("\r", `\r`, "\n", `\n`, "\t", `\t`)

// WritePlain writes the report for a person: a summary first, then one
// section per check. Lines that fail the check start with "!" and warnings
// with "?".
func (r *Report) WritePlain(w io.Writer) error {
	ew := &errWriter{w: w}
	verdict := "PASS"
	if r.Violations > 0 {
		verdict = "FAIL"
	}
	ew.printf("%s: %s, %s\n", verdict, plural(r.Violations, "violation"), plural(r.Warnings, "warning"))
	if r.Compared {
		outside := 0
		ops := map[string]int{}
		for _, c := range r.Changes {
			ops[c.Op]++
			if c.Severity == Violation {
				outside++
			}
		}
		line := fmt.Sprintf("%d changed, %d added, %d removed", ops["changed"], ops["added"], ops["removed"])
		if r.AllowList {
			line += fmt.Sprintf(", %d outside -allow", outside)
		}
		ew.printf("  changes       %s\n", line)
		if r.PreservationChecked {
			ew.printf("  preservation  %s\n", plural(len(r.Preserved), "violation"))
		} else {
			ew.printf("  preservation  not checked (md or txt)\n")
		}
	} else {
		ew.printf("  changes       not checked (no -orig)\n")
		ew.printf("  preservation  not checked (no -orig)\n")
	}
	ew.printf("  banned words  %s\n", plural(len(r.Banned), "hit"))
	ew.printf("  dashes        %s\n", plural(len(r.Dashes), "hit"))
	if r.Compared {
		ew.printf("  new numbers   %s\n", plural(len(r.NewNumbers), "block"))
	} else {
		ew.printf("  new numbers   not checked (no -orig)\n")
	}

	if len(r.Changes) > 0 {
		ew.printf("\nchanges\n")
		for i := 0; i < len(r.Changes); i++ {
			c := r.Changes[i]
			switch c.Op {
			case "changed":
				loc := c.Loc
				if c.OrigLoc != c.Loc {
					loc += " (original " + c.OrigLoc + ")"
				}
				ew.printf("%s changed  %s%s\n", mark(c.Severity), loc, allowNote(c.Severity))
				ew.printf("             - %s\n", lineEscaper.Replace(c.Old))
				ew.printf("             + %s\n", lineEscaper.Replace(c.New))
			case "removed":
				ew.printf("%s removed  %s%s\n", mark(c.Severity), c.OrigLoc, allowNote(c.Severity))
				ew.printf("             - %s\n", lineEscaper.Replace(c.Old))
			case "added":
				j := i
				sev := Info
				for j < len(r.Changes) && r.Changes[j].Op == "added" && r.Changes[j].After == c.After {
					if r.Changes[j].Severity == Violation {
						sev = Violation
					}
					j++
				}
				where := "at the start"
				if c.After != "" {
					where = "after original " + c.After
				}
				ew.printf("%s added    %d %s%s\n", mark(sev), j-i, where, allowNote(sev))
				for _, a := range r.Changes[i:j] {
					ew.printf("           %s + %s\t%s\n", mark(a.Severity), a.Loc, lineEscaper.Replace(a.New))
				}
				i = j - 1
			}
		}
	}

	if len(r.Preserved) > 0 {
		ew.printf("\npreservation\n")
		for _, f := range r.Preserved {
			switch f.What {
			case "memo":
				ew.printf("%s memo at %s is gone or changed: %s\n", mark(f.Severity), f.Loc, lineEscaper.Replace(f.Orig))
			case "table":
				ew.printf("%s table %s %s -> %s\n", mark(f.Severity), f.Loc, f.Orig, f.New)
			default:
				ew.printf("%s %s %s -> %s\n", mark(f.Severity), f.What, f.Orig, f.New)
			}
		}
	}

	writeHits := func(title string, hits []Hit) {
		if len(hits) == 0 {
			return
		}
		ew.printf("\n%s\n", title)
		for _, h := range hits {
			label := h.Match
			if h.Pattern != "" && h.Pattern != h.Match {
				label += " (" + h.Pattern + ")"
			}
			if h.Match == string(emDash) {
				label = "em dash"
			} else if h.Match == string(enDash) {
				label = "en dash"
			}
			note := ""
			if h.Unchanged {
				note = "  [unchanged from original]"
			}
			ew.printf("%s %s  %s  %s[%s]%s%s\n", mark(h.Severity), h.Loc, label,
				lineEscaper.Replace(h.Before), h.Match, lineEscaper.Replace(h.After), note)
		}
	}
	writeHits("banned words", r.Banned)
	writeHits("dashes", r.Dashes)

	if len(r.NewNumbers) > 0 {
		ew.printf("\nnumbers not in the original\n")
		for _, n := range r.NewNumbers {
			ew.printf("%s %s  %s\n", mark(n.Severity), n.Loc, strings.Join(n.Numbers, " "))
		}
	}

	if len(r.Notes) > 0 {
		ew.printf("\nnotes\n")
		for _, n := range r.Notes {
			ew.printf("  %s\n", n)
		}
	}
	return ew.err
}

func mark(s Severity) string {
	switch s {
	case Violation:
		return "!"
	case Warning:
		return "?"
	}
	return " "
}

func allowNote(s Severity) string {
	if s == Violation {
		return "  [not in -allow]"
	}
	return ""
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, args...)
	}
}
