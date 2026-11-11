// Package svgtool prepares SVG figures for documents: it embeds a subset of
// a Korean TrueType font into the SVG (so the figure looks the same where
// the font is not installed) and checks the layout by computation (text
// that leaves its box, boxes that overlap, arrows that end in mid-air or
// cross other boxes).
package svgtool

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// node is one SVG element with the byte ranges needed to edit the source
// text in place.
type node struct {
	name     string
	attrs    map[string]string // attributes without a namespace
	seq      int               // 1-based position among elements of the same name
	parent   *node
	children []*node
	items    []item // character data and child elements in document order

	tagStart, tagEnd int // the start tag
	contentEnd       int // where the end tag starts
	end              int // after the end tag
	selfClosing      bool
}

type item struct {
	text  string
	child *node
}

// parseTree reads the SVG into a tree. The root is the outermost element.
func parseTree(data []byte) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Entity = xml.HTMLEntity
	var root, cur *node
	counts := map[string]int{}
	for {
		start := int(d.InputOffset())
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse SVG: %w", err)
		}
		pos := int(d.InputOffset())
		switch t := tok.(type) {
		case xml.StartElement:
			counts[t.Name.Local]++
			n := &node{
				name:     t.Name.Local,
				attrs:    map[string]string{},
				seq:      counts[t.Name.Local],
				parent:   cur,
				tagStart: start,
				tagEnd:   pos,
			}
			for _, a := range t.Attr {
				if a.Name.Space == "" {
					n.attrs[a.Name.Local] = a.Value
				}
			}
			n.selfClosing = bytes.HasSuffix(bytes.TrimRight(data[start:pos], " \t\r\n"), []byte("/>"))
			if cur == nil {
				if root != nil {
					return nil, errors.New("parse SVG: more than one root element")
				}
				root = n
			} else {
				cur.children = append(cur.children, n)
				cur.items = append(cur.items, item{child: n})
			}
			cur = n
		case xml.EndElement:
			if cur == nil {
				return nil, errors.New("parse SVG: unbalanced end tag")
			}
			cur.contentEnd = start
			cur.end = pos
			if cur.selfClosing {
				cur.contentEnd, cur.end = cur.tagEnd, cur.tagEnd
			}
			cur = cur.parent
		case xml.CharData:
			if cur != nil {
				cur.items = append(cur.items, item{text: string(t)})
			}
		}
	}
	if root == nil {
		return nil, errors.New("parse SVG: no root element")
	}
	if root.name != "svg" {
		return nil, fmt.Errorf("parse SVG: root element is <%s>, not <svg>", root.name)
	}
	return root, nil
}

// walk calls fn for n and its descendants in document order. When fn
// returns false the children of that node are skipped.
func (n *node) walk(fn func(*node) bool) {
	if !fn(n) {
		return
	}
	for _, c := range n.children {
		c.walk(fn)
	}
}

// loc names the element for reports: its id, or name#seq.
func (n *node) loc() string {
	if id := n.attrs["id"]; id != "" {
		return id
	}
	return fmt.Sprintf("%s#%d", n.name, n.seq)
}

// prop returns a presentation property of n itself: the style attribute
// first, then the attribute of the same name.
func (n *node) prop(name string) (string, bool) {
	if v, ok := styleDecl(n.attrs["style"], name); ok {
		return v, true
	}
	v, ok := n.attrs[name]
	return strings.TrimSpace(v), ok
}

// inherited returns the property from n or its nearest ancestor that sets
// it to something other than "inherit".
func (n *node) inherited(name string) (string, bool) {
	for e := n; e != nil; e = e.parent {
		if v, ok := e.prop(name); ok && v != "inherit" {
			return v, true
		}
	}
	return "", false
}

// styleDecl finds the last declaration of name in a CSS declaration list.
func styleDecl(style, name string) (string, bool) {
	var val string
	found := false
	for _, decl := range strings.Split(style, ";") {
		k, v, ok := strings.Cut(decl, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), name) {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.TrimSpace(strings.TrimSuffix(v, "!important"))
		val, found = v, true
	}
	return val, found
}

// textContent joins the character data of n and its descendants.
func (n *node) textContent() string {
	var b strings.Builder
	var rec func(*node)
	rec = func(e *node) {
		for _, it := range e.items {
			if it.child != nil {
				rec(it.child)
			} else {
				b.WriteString(it.text)
			}
		}
	}
	rec(n)
	return b.String()
}

// hidden reports elements whose content is never drawn where it stands.
func hidden(n *node) bool {
	switch n.name {
	case "defs", "marker", "pattern", "clipPath", "mask", "symbol", "title", "desc", "metadata", "style", "script":
		return true
	}
	if v, ok := n.prop("display"); ok && v == "none" {
		return true
	}
	return false
}
