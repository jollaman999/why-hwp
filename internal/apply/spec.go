package apply

import (
	"encoding/json"
	"fmt"
	"io"
)

// Spec is the edit list read from JSON. Every loc in it names a block of
// the input document as 'why-hwp text' prints it, so earlier operations do
// not shift the numbers seen by later ones. A cell with several paragraphs
// is printed as one block per paragraph (.../c2/p0, .../c2/p1); the bare
// cell loc still names its first paragraph. Text-box blocks (loc ending in
// "#box") are read-only and every op refuses them.
type Spec struct {
	Ops []Op `json:"ops"`
}

// Op is one edit. Which fields are used depends on Op:
//
//	set_text       loc, text            replace all text of a paragraph or cell
//	replace        loc, find, with, all replace part of the text of a paragraph
//	insert_after   loc, paras           add paragraphs after loc
//	insert_before  loc, paras           add paragraphs before loc
//	delete         loc                  delete a paragraph
//	image          after|loc, src, width_mm, like
//	                                    add a PNG as an inline picture, in a new
//	                                    paragraph after 'after' or at the end of 'loc'
type Op struct {
	Op      string    `json:"op"`
	Loc     string    `json:"loc,omitempty"`
	After   string    `json:"after,omitempty"`
	Text    *string   `json:"text,omitempty"`
	Find    string    `json:"find,omitempty"`
	With    *string   `json:"with,omitempty"`
	All     bool      `json:"all,omitempty"`
	Paras   []NewPara `json:"paras,omitempty"`
	Like    string    `json:"like,omitempty"`
	Src     string    `json:"src,omitempty"`
	WidthMM float64   `json:"width_mm,omitempty"`
}

// NewPara is one paragraph added by insert_after or insert_before. Like
// names an existing paragraph whose paragraph, style and character shapes
// are copied; without it the shapes of the op's loc are used.
type NewPara struct {
	Text string `json:"text"`
	Like string `json:"like,omitempty"`
}

// ParseSpec reads a JSON spec. Unknown fields are rejected so a typo does
// not silently drop an edit.
func ParseSpec(r io.Reader) (*Spec, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var s Spec
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("spec: %w", err)
	}
	if len(s.Ops) == 0 {
		return nil, fmt.Errorf("spec: no ops")
	}
	for i := range s.Ops {
		if err := s.Ops[i].check(); err != nil {
			return nil, fmt.Errorf("spec: %s: %w", s.Ops[i].label(i), err)
		}
	}
	return &s, nil
}

func (o *Op) label(i int) string {
	return fmt.Sprintf("op %d (%s)", i, o.Op)
}

func (o *Op) check() error {
	need := func(ok bool, what string) error {
		if !ok {
			return fmt.Errorf("%s is required", what)
		}
		return nil
	}
	var err error
	switch o.Op {
	case "set_text":
		if err = need(o.Loc != "", "loc"); err == nil {
			if err = need(o.Text != nil, "text"); err == nil {
				err = checkText(*o.Text)
			}
		}
	case "replace":
		if err = need(o.Loc != "", "loc"); err == nil {
			if err = need(o.Find != "", "find"); err == nil {
				if err = need(o.With != nil, "with"); err == nil {
					err = checkText(*o.With)
				}
			}
		}
	case "insert_after", "insert_before":
		if err = need(o.Loc != "", "loc"); err == nil {
			err = need(len(o.Paras) > 0, "paras")
		}
		for _, p := range o.Paras {
			if err == nil {
				err = checkText(p.Text)
			}
		}
	case "delete":
		err = need(o.Loc != "", "loc")
	case "image":
		switch {
		case (o.After == "") == (o.Loc == ""):
			err = fmt.Errorf("exactly one of after and loc is required")
		case o.Src == "":
			err = fmt.Errorf("src is required")
		case !(o.WidthMM > 0):
			err = fmt.Errorf("width_mm must be positive")
		}
	case "":
		err = fmt.Errorf("op is required")
	default:
		err = fmt.Errorf("unknown op (want set_text, replace, insert_after, insert_before, delete or image)")
	}
	return err
}

// checkText rejects control characters other than tab and line break,
// which have no HWPX text form.
func checkText(s string) error {
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' {
			return fmt.Errorf("text has control character %U (only \\t and \\n are allowed)", r)
		}
	}
	return nil
}
