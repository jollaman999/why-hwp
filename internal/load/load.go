// Package load picks a reader by file extension and returns the model view
// of a document.
package load

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jollaman999/why-hwp/internal/docx"
	"github.com/jollaman999/why-hwp/internal/hwp"
	"github.com/jollaman999/why-hwp/internal/hwpx"
	"github.com/jollaman999/why-hwp/internal/model"
)

// UnsupportedError reports a file extension no reader handles.
type UnsupportedError struct {
	Ext string // as written in the file name, dot included
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("unsupported file type %q", e.Ext)
}

// Document reads an .hwpx, .hwp or .docx file. Other extensions give an
// *UnsupportedError.
func Document(name string) (*model.Document, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".hwpx":
		return hwpx.Read(name)
	case ".docx":
		return docx.Read(name)
	case ".hwp":
		return hwp.Read(name)
	}
	return nil, &UnsupportedError{Ext: filepath.Ext(name)}
}

// IsText reports whether name is a plain text file that Text reads.
func IsText(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".txt":
		return true
	}
	return false
}

// Text reads an .md or .txt file as UTF-8, one KindPara block per line.
// Locs are l1, l2, ... counted from 1 like an editor's line numbers. A
// final line break does not start another line. Other extensions give an
// *UnsupportedError.
func Text(name string) (*model.Document, error) {
	if !IsText(name) {
		return nil, &UnsupportedError{Ext: filepath.Ext(name)}
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return ParseText(data, strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")), nil
}

// ParseText splits data into line blocks as Text does. format is stored in
// the document and its summary.
func ParseText(data []byte, format string) *model.Document {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	doc := &model.Document{Format: format}
	doc.Summary.Format = format
	if len(data) > 0 {
		for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			doc.Blocks = append(doc.Blocks, model.Block{
				Loc:  fmt.Sprintf("l%d", i+1),
				Kind: model.KindPara,
				Text: strings.TrimSuffix(line, "\r"),
			})
		}
	}
	n := len(doc.Blocks)
	doc.Summary.Paragraphs = n
	if n > 0 {
		doc.Summary.Sections = 1
	}
	doc.Summary.OwnerMarks = model.FindOwnerMarks(doc.Blocks)
	return doc
}

// Any reads every format Document and Text read.
func Any(name string) (*model.Document, error) {
	if IsText(name) {
		return Text(name)
	}
	return Document(name)
}
