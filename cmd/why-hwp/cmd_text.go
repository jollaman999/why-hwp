package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jollaman999/why-hwp/internal/docx"
	"github.com/jollaman999/why-hwp/internal/hwp"
	"github.com/jollaman999/why-hwp/internal/hwpx"
	"github.com/jollaman999/why-hwp/internal/model"
)

func init() {
	register("text", "Extract plain text", runText)
}

// lineEscaper keeps one block on one line in plain output.
var lineEscaper = strings.NewReplacer("\r", `\r`, "\n", `\n`, "\t", `\t`)

func runText(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("text", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "plain", "output format: plain or json")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: why-hwp text [-format plain|json] <file.hwpx|file.hwp|file.docx>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Plain output is one block per line: <loc><TAB><text>, with tab and")
		fmt.Fprintln(stderr, "line break inside the text written as \\t and \\n.")
		fmt.Fprintln(stderr)
		fs.PrintDefaults()
	}
	name, code := parseFileArgs(fs, args, format)
	if name == "" {
		return code
	}
	doc, code := loadDocument("text", name, stderr)
	if doc == nil {
		return code
	}

	if *format == "json" {
		blocks := doc.Blocks
		if blocks == nil {
			blocks = []model.Block{}
		}
		return writeJSON(stdout, stderr, struct {
			Format string        `json:"format"`
			Blocks []model.Block `json:"blocks"`
		}{doc.Format, blocks})
	}
	for _, b := range doc.Blocks {
		fmt.Fprintf(stdout, "%s\t%s\n", b.Loc, lineEscaper.Replace(b.Text))
	}
	return 0
}

// parseFileArgs parses flags and the single file argument shared by text
// and info. It returns an empty name and the exit code when parsing fails.
func parseFileArgs(fs *flag.FlagSet, args []string, format *string) (string, int) {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return "", 0
		}
		return "", 2
	}
	if *format != "plain" && *format != "json" {
		fmt.Fprintf(fs.Output(), "why-hwp %s: unknown -format %q (want plain or json)\n", fs.Name(), *format)
		return "", 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return "", 2
	}
	return fs.Arg(0), 0
}

// loadDocument picks a reader by file extension. On failure it reports to
// stderr and returns a nil document with the exit code.
func loadDocument(cmd, name string, stderr io.Writer) (*model.Document, int) {
	var (
		doc *model.Document
		err error
	)
	switch strings.ToLower(filepath.Ext(name)) {
	case ".hwpx":
		doc, err = hwpx.Read(name)
	case ".docx":
		doc, err = docx.Read(name)
	case ".hwp":
		doc, err = hwp.Read(name)
	default:
		fmt.Fprintf(stderr, "why-hwp %s: unsupported file type %q (want .hwpx, .hwp or .docx)\n", cmd, filepath.Ext(name))
		return nil, 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "why-hwp %s: %v\n", cmd, err)
		return nil, 1
	}
	return doc, 0
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(stderr, "why-hwp: %v\n", err)
		return 1
	}
	return 0
}
