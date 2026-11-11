// Package model is the format-independent view of a document. The hwpx,
// docx and hwp readers produce it; the text, info, apply and verify
// commands consume it.
//
// # Blocks
//
// A document is a flat list of Blocks in reading order. A Block is either
// a top-level paragraph (KindPara) or a table cell (KindCell). Every
// top-level paragraph gets a Block, empty ones included, so paragraph
// numbers stay stable. A paragraph that holds a table is followed by the
// Blocks of that table's cells. A cell's text is the text of its
// paragraphs joined with "\n"; text of tables nested in the cell is not
// repeated there but appears in the nested cells' own Blocks.
//
// Tab characters are "\t" and forced line breaks are "\n". Memo bodies,
// headers, footers, footnotes and picture captions are not part of the
// text. Text-box text is appended to the paragraph that anchors it.
//
// # Locations
//
// Loc names a piece of text so a person can find it again in the editor
// and a later apply step can address it. All indexes are 0-based.
//
//	s0/p12                       section 0, top-level paragraph 12
//	s0/p3/t0/r1/c2               table 0 anchored in paragraph 3, row 1, column 2
//	s0/p3/t0/r1/c2/p0/t0/r0/c1   table nested in paragraph 0 of that cell
//
// Rows and columns are grid addresses: a cell that spans columns keeps
// the address of its top-left grid slot. In docx a body-level table takes
// a paragraph number of its own (s0/p5/t0/...), and every block is in s0.
package model

import (
	"regexp"
	"sort"
)

// Kind tells what a Block is.
type Kind string

const (
	KindPara Kind = "para" // top-level paragraph
	KindCell Kind = "cell" // table cell
)

// Block is one addressable run of text.
type Block struct {
	Loc  string `json:"loc"`
	Kind Kind   `json:"kind"`
	Text string `json:"text"`
}

// Document is what a reader returns.
type Document struct {
	Format  string  // "hwpx", "docx" or "hwp"
	Blocks  []Block // reading order
	Summary Summary
}

// Summary describes document structure for the info command. Fields that
// a format does not have stay zero (docx has no paragraph or character
// shape tables, so ParaShapes and CharShapes are 0 there).
type Summary struct {
	Format       string      `json:"format"`
	Sections     int         `json:"sections"`
	Paragraphs   int         `json:"paragraphs"` // top-level paragraphs only
	Tables       []Table     `json:"tables"`
	Memos        []Memo      `json:"memos"`
	TrackChanges int         `json:"trackChanges"` // hwpx: hh:trackChange entries, hwp: max(ID_MAPPINGS[16], TRACK_CHANGE records), docx: w:ins + w:del
	ParaShapes   int         `json:"paraShapes"`   // hwpx: hh:paraPr entries, hwp: PARA_SHAPE records
	CharShapes   int         `json:"charShapes"`   // hwpx: hh:charPr entries, hwp: CHAR_SHAPE records
	Styles       []Style     `json:"styles"`
	Pictures     []Picture   `json:"pictures"`
	BinData      []BinItem   `json:"binData"`
	OwnerMarks   []OwnerMark `json:"ownerMarks"`
}

// Table is one table, nested ones included.
type Table struct {
	Loc  string `json:"loc"`
	Rows int    `json:"rows"`
	Cols int    `json:"cols"`
}

// Memo is a memo (hwpx, hwp) or comment (docx). Loc is the paragraph or cell
// that holds the memo anchor; for docx it is empty.
type Memo struct {
	Loc  string `json:"loc"`
	Text string `json:"text"`
}

// Style is one named style.
type Style struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	EngName string `json:"engName,omitempty"`
}

// Picture is one embedded picture. Ref is the binary item id (hwpx), BIN_DATA number counted from 1 (hwp) or
// relationship id (docx) that points into BinData.
type Picture struct {
	Loc string `json:"loc"`
	Ref string `json:"ref"`
}

// BinItem is one embedded binary part (BinData/ in hwpx and hwp, word/media/ in docx).
type BinItem struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType,omitempty"`
	Size      int64  `json:"size"`
}

// OwnerMark is a short parenthesized tag such as "(A)" or "(A/B)" that
// documents use to mark who is responsible for an item, with every place
// it occurs.
type OwnerMark struct {
	Mark  string   `json:"mark"`
	Count int      `json:"count"`
	Locs  []string `json:"locs"`
}

var ownerMarkRe = regexp.MustCompile(`\([A-Z가-힣/,\s]{1,15}\)`)

// FindOwnerMarks collects owner-mark candidates from blocks, most frequent
// first. A mark that occurs twice in one block lists that Loc twice.
func FindOwnerMarks(blocks []Block) []OwnerMark {
	idx := map[string]int{}
	var marks []OwnerMark
	for _, b := range blocks {
		for _, m := range ownerMarkRe.FindAllString(b.Text, -1) {
			i, ok := idx[m]
			if !ok {
				i = len(marks)
				idx[m] = i
				marks = append(marks, OwnerMark{Mark: m})
			}
			marks[i].Count++
			marks[i].Locs = append(marks[i].Locs, b.Loc)
		}
	}
	sort.SliceStable(marks, func(i, j int) bool {
		if marks[i].Count != marks[j].Count {
			return marks[i].Count > marks[j].Count
		}
		return marks[i].Mark < marks[j].Mark
	})
	return marks
}
