package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jollaman999/why-hwp/internal/model"
)

func init() {
	register("info", "Show document structure", runInfo)
}

func runInfo(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "plain", "output format: plain or json")
	view := viewFlag(fs)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp info [-format plain|json] [-view accepted|original|markup] <file.hwpx|file.hwp|file.docx>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Shows sections, paragraphs, tables, owner marks such as (A) or (A/B),")
		fmt.Fprintln(stderr, "memos, tracked changes, styles and pictures. Owner marks are counted in")
		fmt.Fprintln(stderr, "the text of -view (see why-hwp text -h); .hwp files have no views.")
		fmt.Fprintln(stderr)
		fs.PrintDefaults()
	}
	name, code := parseFileArgs(fs, args, format, view)
	if name == "" {
		return code
	}
	doc, code := loadDocument("info", name, model.View(*view), stderr)
	if doc == nil {
		return code
	}

	s := doc.Summary
	if *format == "json" {
		return writeJSON(stdout, stderr, nonNil(s))
	}

	fmt.Fprintf(stdout, "format         %s\n", s.Format)
	fmt.Fprintf(stdout, "sections       %d\n", s.Sections)
	fmt.Fprintf(stdout, "paragraphs     %d\n", s.Paragraphs)
	fmt.Fprintf(stdout, "tables         %d\n", len(s.Tables))
	for _, t := range s.Tables {
		fmt.Fprintf(stdout, "  %s\t%dx%d\n", t.Loc, t.Rows, t.Cols)
	}
	fmt.Fprintf(stdout, "owner marks    %d\n", len(s.OwnerMarks))
	for _, m := range s.OwnerMarks {
		fmt.Fprintf(stdout, "  %s\t%d\t%s\n", m.Mark, m.Count, strings.Join(m.Locs, " "))
	}
	fmt.Fprintf(stdout, "memos          %d\n", len(s.Memos))
	for _, m := range s.Memos {
		fmt.Fprintf(stdout, "  %s\t%s\n", m.Loc, lineEscaper.Replace(m.Text))
	}
	fmt.Fprintf(stdout, "track changes  %d\n", s.TrackChanges)
	if s.TrackChanges > 0 {
		if s.View != "" {
			fmt.Fprintf(stdout, "  view\t%s\n", s.View)
		} else {
			fmt.Fprintf(stdout, "  view\tnone: %s tracked insertions and deletions are not told apart\n", s.Format)
		}
	}
	if s.Format == "hwpx" || s.Format == "hwp" {
		fmt.Fprintf(stdout, "para shapes    %d\n", s.ParaShapes)
		fmt.Fprintf(stdout, "char shapes    %d\n", s.CharShapes)
	}
	fmt.Fprintf(stdout, "styles         %d\n", len(s.Styles))
	for _, st := range s.Styles {
		name := st.Name
		if st.EngName != "" {
			name += " (" + st.EngName + ")"
		}
		fmt.Fprintf(stdout, "  %s\t%s\t%s\n", st.ID, st.Type, name)
	}
	fmt.Fprintf(stdout, "pictures       %d\n", len(s.Pictures))
	for _, p := range s.Pictures {
		fmt.Fprintf(stdout, "  %s\t%s\n", p.Loc, p.Ref)
	}
	fmt.Fprintf(stdout, "bin data       %d\n", len(s.BinData))
	for _, b := range s.BinData {
		fmt.Fprintf(stdout, "  %s\t%s\t%s\t%d bytes\n", b.ID, b.Path, b.MediaType, b.Size)
	}
	return 0
}

// nonNil makes empty lists encode as [] rather than null.
func nonNil(s model.Summary) model.Summary {
	if s.Tables == nil {
		s.Tables = []model.Table{}
	}
	if s.Memos == nil {
		s.Memos = []model.Memo{}
	}
	if s.Styles == nil {
		s.Styles = []model.Style{}
	}
	if s.Pictures == nil {
		s.Pictures = []model.Picture{}
	}
	if s.BinData == nil {
		s.BinData = []model.BinItem{}
	}
	if s.OwnerMarks == nil {
		s.OwnerMarks = []model.OwnerMark{}
	}
	return s
}
