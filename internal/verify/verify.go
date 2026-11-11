// Package verify checks a document before it is handed over. Against an
// original it lists what changed, flags changes outside an allow list and
// flags memos, tables, pictures and tracked changes that were lost. On its
// own it finds banned words and dash characters, and against an original
// it warns about numbers the original did not have.
package verify

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/jollaman999/why-hwp/internal/model"
)

// Severity grades a finding. Violations fail the check, warnings do not,
// and info entries only describe.
type Severity string

const (
	Violation Severity = "violation"
	Warning   Severity = "warning"
	Info      Severity = "info"
)

// AllowList is the set of locations a writer may change. A pattern that
// ends in "*" matches every location starting with the part before it; any
// other pattern matches one location exactly.
type AllowList struct {
	patterns []string
}

// ParseAllow reads one pattern per line. Blank lines and lines starting
// with "#" are skipped.
func ParseAllow(r io.Reader) (*AllowList, error) {
	a := &AllowList{patterns: []string{}}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), utf8BOM))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		a.patterns = append(a.patterns, line)
	}
	return a, sc.Err()
}

// Match reports whether loc is allowed.
func (a *AllowList) Match(loc string) bool {
	if loc == "" {
		return false
	}
	for _, p := range a.patterns {
		if prefix, ok := strings.CutSuffix(p, "*"); ok {
			if strings.HasPrefix(loc, prefix) {
				return true
			}
		} else if p == loc {
			return true
		}
	}
	return false
}

// Pattern is one banned word or expression.
type Pattern struct {
	Source string // the line as written in the file
	re     *regexp.Regexp
}

// ParseBanned reads one banned word per line. Blank lines and lines
// starting with "#" are skipped. A line starting with "re:" is a Go regular
// expression; any other line is matched literally. With ignoreCase, letter
// case is ignored for both.
func ParseBanned(r io.Reader, ignoreCase bool) ([]Pattern, error) {
	var out []Pattern
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), utf8BOM))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		expr := regexp.QuoteMeta(line)
		if rest, ok := strings.CutPrefix(line, "re:"); ok {
			expr = rest
		}
		if ignoreCase {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if re.MatchString("") {
			return nil, fmt.Errorf("line %d: %q matches empty text", n, line)
		}
		out = append(out, Pattern{Source: line, re: re})
	}
	return out, sc.Err()
}

// Options selects the checks. Allow nil means no allow list was given:
// changes are then listed but are not violations.
type Options struct {
	Allow  *AllowList
	Banned []Pattern
}

// Report is the result of Check.
type Report struct {
	Format     string   `json:"format"`
	OrigFormat string   `json:"origFormat,omitempty"`
	Violations int      `json:"violations"`
	Warnings   int      `json:"warnings"`
	Compared   bool     `json:"compared"` // an original was given
	AllowList  bool     `json:"allowList"`
	Changes    []Change `json:"changes"`
	// PreservationChecked is false without an original and when either
	// file is md or txt.
	PreservationChecked bool      `json:"preservationChecked"`
	Preserved           []Finding `json:"preservation"`
	Banned              []Hit     `json:"banned"`
	Dashes              []Hit     `json:"dashes"`
	NewNumbers          []Numbers `json:"newNumbers"`
	Notes               []string  `json:"notes"`
}

// Change is one block that differs from the original.
type Change struct {
	Op       string   `json:"op"`                // "changed", "added" or "removed"
	Loc      string   `json:"loc,omitempty"`     // in the new document (changed, added)
	OrigLoc  string   `json:"origLoc,omitempty"` // in the original (changed, removed)
	After    string   `json:"after,omitempty"`   // added: original block it follows, empty at the start
	Old      string   `json:"old,omitempty"`
	New      string   `json:"new,omitempty"`
	Severity Severity `json:"severity"`
}

// Finding is one preservation result.
type Finding struct {
	What     string   `json:"what"` // memo, tables, table, pictures, trackChanges
	Loc      string   `json:"loc,omitempty"`
	Orig     string   `json:"orig"`
	New      string   `json:"new"`
	Severity Severity `json:"severity"`
}

// Hit is one banned word or dash found in the new document.
type Hit struct {
	Loc       string   `json:"loc"`
	Match     string   `json:"match"`
	Pattern   string   `json:"pattern,omitempty"`
	Before    string   `json:"before"`
	After     string   `json:"after"`
	Unchanged bool     `json:"unchanged,omitempty"` // the block equals its original
	Severity  Severity `json:"severity"`
}

// Numbers lists numbers in one block that appear nowhere in the original.
type Numbers struct {
	Loc      string   `json:"loc"`
	Numbers  []string `json:"numbers"`
	Severity Severity `json:"severity"`
}

const (
	utf8BOM = "\xef\xbb\xbf"
	emDash  = rune(0x2014)
	enDash  = rune(0x2013)
	// contextRunes is how much text is shown on each side of a hit.
	contextRunes = 15
)

// Check runs the checks on cur. orig may be nil.
func Check(orig, cur *model.Document, opt Options) *Report {
	r := &Report{
		Format:     cur.Format,
		AllowList:  opt.Allow != nil,
		Changes:    []Change{},
		Preserved:  []Finding{},
		Banned:     []Hit{},
		Dashes:     []Hit{},
		NewNumbers: []Numbers{},
		Notes:      []string{},
	}
	unchanged := map[int]bool{}
	if orig != nil {
		r.Compared = true
		r.OrigFormat = orig.Format
		pairs, approx := align(orig.Blocks, cur.Blocks)
		if approx {
			r.Notes = append(r.Notes, "the documents differ too much to align every block; the differing middle part was paired by position")
		}
		r.changes(orig, cur, pairs, opt.Allow, unchanged)
		r.preservation(orig, cur, pairs)
		r.numbers(orig, cur)
		r.trackNotes(orig, cur)
	} else {
		r.trackNotes(nil, cur)
	}
	r.banned(cur, opt.Banned, unchanged)
	r.dashes(cur, orig != nil, unchanged)

	count := func(s Severity) {
		switch s {
		case Violation:
			r.Violations++
		case Warning:
			r.Warnings++
		}
	}
	for _, c := range r.Changes {
		count(c.Severity)
	}
	for _, f := range r.Preserved {
		count(f.Severity)
	}
	for _, h := range r.Banned {
		count(h.Severity)
	}
	for _, h := range r.Dashes {
		count(h.Severity)
	}
	for _, n := range r.NewNumbers {
		count(n.Severity)
	}
	return r
}

// changes fills r.Changes from the alignment and marks the new blocks that
// equal their original in unchanged.
func (r *Report) changes(orig, cur *model.Document, pairs []pair, allow *AllowList, unchanged map[int]bool) {
	sev := func(allowed bool) Severity {
		if allow == nil || allowed {
			return Info
		}
		return Violation
	}
	after := ""
	for _, p := range pairs {
		switch {
		case p.o >= 0 && p.n >= 0:
			ob, nb := orig.Blocks[p.o], cur.Blocks[p.n]
			after = ob.Loc
			if ob.Text == nb.Text {
				unchanged[p.n] = true
				continue
			}
			r.Changes = append(r.Changes, Change{
				Op: "changed", Loc: nb.Loc, OrigLoc: ob.Loc, Old: ob.Text, New: nb.Text,
				Severity: sev(allow != nil && allow.Match(ob.Loc)),
			})
		case p.o >= 0:
			ob := orig.Blocks[p.o]
			after = ob.Loc
			r.Changes = append(r.Changes, Change{
				Op: "removed", OrigLoc: ob.Loc, Old: ob.Text,
				Severity: sev(allow != nil && allow.Match(ob.Loc)),
			})
		default:
			nb := cur.Blocks[p.n]
			r.Changes = append(r.Changes, Change{
				Op: "added", Loc: nb.Loc, After: after, New: nb.Text,
				Severity: sev(allow != nil && (allow.Match(after) || allow.Match(nb.Loc))),
			})
		}
	}
}

func isTextFormat(f string) bool { return f == "md" || f == "txt" }

// preservation checks that memos, tables, pictures and tracked changes of
// the original are still there.
func (r *Report) preservation(orig, cur *model.Document, pairs []pair) {
	if isTextFormat(orig.Format) || isTextFormat(cur.Format) {
		r.Notes = append(r.Notes, "md and txt files carry no memos, tables, pictures or tracked changes; preservation checks skipped")
		return
	}
	r.PreservationChecked = true
	osum, nsum := orig.Summary, cur.Summary
	add := func(what, loc, o, n string, sev Severity) {
		r.Preserved = append(r.Preserved, Finding{What: what, Loc: loc, Orig: o, New: n, Severity: sev})
	}
	countCheck := func(what string, o, n int) bool {
		if n < o {
			add(what, "", fmt.Sprint(o), fmt.Sprint(n), Violation)
			return false
		}
		return true
	}

	// Every memo the new document lost leaves an original body unmatched,
	// so the per-memo findings also cover a lower memo count.
	left := map[string]int{}
	for _, m := range nsum.Memos {
		left[m.Text]++
	}
	for _, m := range osum.Memos {
		if left[m.Text] > 0 {
			left[m.Text]--
			continue
		}
		add("memo", m.Loc, m.Text, "", Violation)
	}

	tablesKept := countCheck("tables", len(osum.Tables), len(nsum.Tables))
	locMap := map[string]string{}
	for _, p := range pairs {
		if p.o >= 0 && p.n >= 0 {
			locMap[orig.Blocks[p.o].Loc] = cur.Blocks[p.n].Loc
		}
	}
	newTables := map[string]model.Table{}
	for _, t := range nsum.Tables {
		newTables[t.Loc] = t
	}
	for _, t := range osum.Tables {
		nt, ok := newTables[mapLoc(t.Loc, locMap)]
		if !ok {
			if tablesKept {
				add("table", t.Loc, fmt.Sprintf("%dx%d", t.Rows, t.Cols), "not found", Violation)
			}
			continue
		}
		if nt.Rows < t.Rows || nt.Cols < t.Cols {
			add("table", t.Loc, fmt.Sprintf("%dx%d", t.Rows, t.Cols), fmt.Sprintf("%s %dx%d", nt.Loc, nt.Rows, nt.Cols), Violation)
		}
	}

	countCheck("pictures", len(osum.Pictures), len(nsum.Pictures))
	countCheck("trackChanges", osum.TrackChanges, nsum.TrackChanges)
}

// mapLoc moves an original table location to the new document through the
// longest leading part of it that is the location of an aligned block.
func mapLoc(loc string, m map[string]string) string {
	parts := strings.Split(loc, "/")
	for i := len(parts) - 1; i > 0; i-- {
		head := strings.Join(parts[:i], "/")
		if n, ok := m[head]; ok {
			return n + "/" + strings.Join(parts[i:], "/")
		}
	}
	return loc
}

var numberRe = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

// numbers warns about numbers in cur that appear nowhere in orig. Thousand
// separators are ignored when comparing.
func (r *Report) numbers(orig, cur *model.Document) {
	have := map[string]bool{}
	for _, b := range orig.Blocks {
		for _, n := range numberRe.FindAllString(b.Text, -1) {
			have[strings.ReplaceAll(n, ",", "")] = true
		}
	}
	for _, b := range cur.Blocks {
		var fresh []string
		seen := map[string]bool{}
		for _, n := range numberRe.FindAllString(b.Text, -1) {
			k := strings.ReplaceAll(n, ",", "")
			if have[k] || seen[k] {
				continue
			}
			seen[k] = true
			fresh = append(fresh, n)
		}
		if fresh != nil {
			r.NewNumbers = append(r.NewNumbers, Numbers{Loc: b.Loc, Numbers: fresh, Severity: Warning})
		}
	}
}

// trackNotes states which view tracked changes were read in, when a
// document has any.
func (r *Report) trackNotes(orig, cur *model.Document) {
	seen := map[string]bool{}
	for _, d := range []*model.Document{orig, cur} {
		if d == nil || d.Summary.TrackChanges == 0 || seen[d.Format] {
			continue
		}
		seen[d.Format] = true
		switch d.Summary.View {
		case model.ViewAccepted:
			r.Notes = append(r.Notes, d.Format+" has tracked changes: read in the accepted view (inserted text kept, deleted text left out)")
		case model.ViewOriginal:
			r.Notes = append(r.Notes, d.Format+" has tracked changes: read in the original view (deleted text kept, inserted text left out)")
		case model.ViewMarkup:
			r.Notes = append(r.Notes, d.Format+" has tracked changes: read in the markup view (inserted text as [+...+], deleted text as [-...-])")
		default:
			r.Notes = append(r.Notes, d.Format+" has tracked changes: views are not supported for "+d.Format+", so tracked insertions and deletions are not told apart")
		}
	}
}

func (r *Report) banned(cur *model.Document, pats []Pattern, unchanged map[int]bool) {
	for i, b := range cur.Blocks {
		for _, p := range pats {
			for _, m := range p.re.FindAllStringIndex(b.Text, -1) {
				h := hit(b, m[0], m[1])
				h.Pattern = p.Source
				h.Unchanged = unchanged[i]
				h.Severity = Violation
				r.Banned = append(r.Banned, h)
			}
		}
	}
}

// dashes reports em and en dashes. An em dash is a violation in text the
// writer touched (every block when there is no original) and a warning in
// blocks equal to the original; an en dash is always a warning.
func (r *Report) dashes(cur *model.Document, compared bool, unchanged map[int]bool) {
	for i, b := range cur.Blocks {
		for j, c := range b.Text {
			if c != emDash && c != enDash {
				continue
			}
			h := hit(b, j, j+len(string(c)))
			h.Unchanged = unchanged[i]
			h.Severity = Warning
			if c == emDash && !(compared && unchanged[i]) {
				h.Severity = Violation
			}
			r.Dashes = append(r.Dashes, h)
		}
	}
}

// hit builds a Hit for b.Text[start:end] with some text on each side.
func hit(b model.Block, start, end int) Hit {
	before := []rune(b.Text[:start])
	if len(before) > contextRunes {
		before = before[len(before)-contextRunes:]
	}
	after := []rune(b.Text[end:])
	if len(after) > contextRunes {
		after = after[:contextRunes]
	}
	return Hit{Loc: b.Loc, Match: b.Text[start:end], Before: string(before), After: string(after)}
}
