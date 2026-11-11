package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// doc is one input document of the corpus. Name is the file name, which is
// also the prefix of every output made from it: <Name>.pdf, <Name>.txt and
// <Name>.<Other()>.
type doc struct {
	Name string
	Path string
	Ext  string // "hwp" or "hwpx"
}

// Other is the format the document is saved to for the round trip: hwpx for
// an hwp input and hwp for an hwpx input.
func (d doc) Other() string {
	return otherFormat(d.Ext)
}

func otherFormat(ext string) string {
	if ext == "hwp" {
		return "hwpx"
	}
	return "hwp"
}

// Targets lists the output formats made for the document.
func (d doc) Targets() []string {
	return []string{"pdf", "txt", d.Other()}
}

// inputExt returns "hwp" or "hwpx" for a document file name, or "".
func inputExt(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".hwp":
		return "hwp"
	case ".hwpx":
		return "hwpx"
	}
	return ""
}

// listCorpus returns the .hwp and .hwpx files directly inside dir, sorted by
// name. Subdirectories are not read.
func listCorpus(dir string) ([]doc, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var docs []doc
	for _, e := range ents {
		if !e.Type().IsRegular() {
			continue
		}
		ext := inputExt(e.Name())
		if ext == "" {
			continue
		}
		docs = append(docs, doc{Name: e.Name(), Path: filepath.Join(dir, e.Name()), Ext: ext})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	if len(docs) == 0 {
		return nil, fmt.Errorf("%s: no .hwp or .hwpx files", dir)
	}
	return docs, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// manifest records, for each output file name in a directory, the SHA-256 of
// the input it was made from, so an output is made again when its input
// changes.
type manifest struct {
	path    string
	Outputs map[string]string `json:"outputs"`
}

func loadManifest(dir string) (*manifest, error) {
	m := &manifest{path: filepath.Join(dir, "manifest.json"), Outputs: map[string]string{}}
	b, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("%s: %w", m.path, err)
	}
	if m.Outputs == nil {
		m.Outputs = map[string]string{}
	}
	return m, nil
}

// fresh reports whether out (a file in the manifest's directory) exists and
// was made from an input with the given hash. Only a .txt output may be
// empty: a document without text has an empty text output.
func (m *manifest) fresh(out, inputSHA string) bool {
	if m.Outputs[out] != inputSHA {
		return false
	}
	st, err := os.Stat(filepath.Join(filepath.Dir(m.path), out))
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	return st.Size() > 0 || strings.HasSuffix(out, ".txt")
}

func (m *manifest) record(out, inputSHA string) error {
	m.Outputs[out] = inputSHA
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
