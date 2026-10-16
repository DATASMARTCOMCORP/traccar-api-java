package kit

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

// XMLNode is an element of a DOM parsed without namespace awareness (as Java's default
// DocumentBuilderFactory): names are the qualified names as written.
type XMLNode struct {
	name     string
	attrs    []xml.Attr
	children []*XMLNode
	text     strings.Builder // all descendant text, in order: the XPath string value
}

func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// ParseXML returns the document element, or panics where the Java parser throws.
func ParseXML(data []byte) *XMLNode {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var root *XMLNode
	var stack []*XMLNode
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &XMLNode{name: qname(t.Name), attrs: t.Attr}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			} else if root != nil {
				Throw("SAXParseException: The markup in the document following the root element must be well-formed.")
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != qname(t.Name) {
				Throw("SAXParseException: The element type must be terminated by the matching end-tag.")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			for _, n := range stack {
				n.text.Write(t)
			}
		case xml.Directive:
			if bytes.HasPrefix(t, []byte("DOCTYPE")) {
				Throw("SAXParseException: DOCTYPE is disallowed when the feature \"http://apache.org/xml/features/disallow-doctype-decl\" set to true.")
			}
		}
	}
	if root == nil || len(stack) > 0 {
		Throw("SAXParseException: Premature end of file.")
	}
	return root
}

// Child is xPath.evaluate(name, n): the string value of the first child element name.
func (n *XMLNode) Child(name string) string {
	for _, c := range n.children {
		if c.name == name {
			return c.text.String()
		}
	}
	return ""
}

// Attr is Element.getAttribute ("" when missing).
func (n *XMLNode) Attr(name string) (string, bool) {
	for _, a := range n.attrs {
		if qname(a.Name) == name {
			return a.Value, true
		}
	}
	return "", false
}

// Find is the XPath //a/b/c from the document element n: every element named path's last
// step whose ancestors match the previous steps, in document order.
func (n *XMLNode) Find(path ...string) []*XMLNode {
	var out []*XMLNode
	var walk func(e *XMLNode)
	match := func(e *XMLNode) {
		cur := []*XMLNode{e}
		for _, step := range path[1:] {
			var next []*XMLNode
			for _, c := range cur {
				for _, cc := range c.children {
					if cc.name == step {
						next = append(next, cc)
					}
				}
			}
			cur = next
		}
		out = append(out, cur...)
	}
	walk = func(e *XMLNode) {
		if e.name == path[0] {
			match(e)
		}
		for _, c := range e.children {
			walk(c)
		}
	}
	walk(n)
	return out
}
