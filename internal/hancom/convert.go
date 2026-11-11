// Package hancom converts documents with an installed Hancom Office (Hwp)
// through its COM automation object, HWPFrame.HwpObject. Conversion works
// only on Windows; on other systems Convert returns ErrUnsupported.
package hancom

import (
	"encoding/csv"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrUnsupported is returned when Hancom Office cannot be used: the system
// is not Windows, or the Hwp COM object cannot be created.
var ErrUnsupported = errors.New("convert needs Windows with Hancom Office")

// Format is an output format.
type Format string

const (
	HWP  Format = "hwp"
	HWPX Format = "hwpx"
	PDF  Format = "pdf"
	TXT  Format = "txt"
)

// Formats lists the output formats.
var Formats = []Format{HWP, HWPX, PDF, TXT}

// ParseFormat parses an output format name such as "hwpx" (case-insensitive,
// a leading dot is allowed).
func ParseFormat(s string) (Format, error) {
	f := Format(strings.ToLower(strings.TrimPrefix(s, ".")))
	for _, x := range Formats {
		if f == x {
			return f, nil
		}
	}
	return "", fmt.Errorf("unknown output format %q (want hwp, hwpx, pdf or txt)", s)
}

// saveFormat is the format name Hwp's SaveAs takes.
func (f Format) saveFormat() string {
	switch f {
	case HWP:
		return "HWP"
	case HWPX:
		return "HWPX"
	case PDF:
		return "PDF"
	case TXT:
		return "TEXT"
	}
	return ""
}

// openFormat returns the format name Hwp's Open takes for the input path.
// An empty name lets Hwp detect the format itself.
func openFormat(in string) (string, error) {
	switch strings.ToLower(filepath.Ext(in)) {
	case ".hwp":
		return "HWP", nil
	case ".hwpx":
		return "HWPX", nil
	case ".docx":
		return "", nil
	}
	return "", fmt.Errorf("%s: unsupported input (want .hwp, .hwpx or .docx)", in)
}

// Plan works out the output path and format from the command line values.
// to is the -to value and out the -o value; either may be empty. Without to
// the format comes from the extension of out, and without out the output is
// the input path with the format's extension.
func Plan(in, out, to string) (string, Format, error) {
	if _, err := openFormat(in); err != nil {
		return "", "", err
	}
	var f Format
	var err error
	switch {
	case to != "":
		if f, err = ParseFormat(to); err != nil {
			return "", "", err
		}
	case out != "":
		ext := filepath.Ext(out)
		if ext == "" {
			return "", "", fmt.Errorf("%s: no extension; give the format with -to", out)
		}
		if f, err = ParseFormat(ext); err != nil {
			return "", "", fmt.Errorf("%s: %v; give the format with -to", out, err)
		}
	default:
		return "", "", errors.New("give the output format with -to or an output path with -o")
	}
	if out == "" {
		out = strings.TrimSuffix(in, filepath.Ext(in)) + "." + string(f)
	}
	if samePath(in, out) {
		return "", "", fmt.Errorf("%s: output is the input file", out)
	}
	return out, f, nil
}

func samePath(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}

// Options describes one conversion.
type Options struct {
	In      string // .hwp, .hwpx or .docx
	Out     string
	To      Format
	Timeout time.Duration // whole conversion; 0 means 120 seconds
	Force   bool          // overwrite an existing output file
}

// Result describes the written output file.
type Result struct {
	Out    string
	Format Format
	Size   int64
	MD5    string // hex, checked against the converted file after copying
	// RegisterModule is what Hwp returned for the FilePathCheckerModule
	// registration. False means the security module is not installed.
	RegisterModule bool
}

// parseTasklist reads `tasklist /FO CSV /NH` output and returns the PIDs of
// the rows whose image name is image. Lines that are not CSV rows (such as
// the localized "no tasks" message) are ignored.
func parseTasklist(out []byte, image string) []int {
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"`) {
			continue
		}
		rec, err := csv.NewReader(strings.NewReader(line)).Read()
		if err != nil || len(rec) < 2 || !strings.EqualFold(rec[0], image) {
			continue
		}
		if pid, err := strconv.Atoi(rec[1]); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// newPIDs returns the PIDs in after that are not in before.
func newPIDs(before, after []int) []int {
	seen := make(map[int]bool, len(before))
	for _, p := range before {
		seen[p] = true
	}
	var n []int
	for _, p := range after {
		if !seen[p] {
			n = append(n, p)
		}
	}
	return n
}

// parsePIDLine parses the "pid 1234" line the conversion script prints. The
// script prints the Hwp PIDs that appeared while it created the COM object,
// comma separated; ok is false unless there is exactly one.
func parsePIDLine(line string) (pid int, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(line), "pid ")
	if !found || strings.Contains(rest, ",") {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(rest))
	return pid, err == nil && pid > 0
}
