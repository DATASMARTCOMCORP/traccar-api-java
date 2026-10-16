// Package moovbox is the Moovbox protocol (MoovboxProtocol): XML documents of GPS
// coordinates posted over HTTP.
package moovbox

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "moovbox",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is MoovboxProtocolDecoder.
type Decoder struct{}

// DecodeHTTP decodes one request; an error is where the Java decoder throws.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)
	root := parseXML(body)

	id, _ := root.attr("id")
	ds := m.Session(id)
	if ds == nil {
		return nil, nil, nil
	}

	positions = []*model.Position{}
	for _, n := range root.find("gps", "coordinates", "coordinate") {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Valid = true
		pos.SetTime(time.UnixMilli(kit.Long(n.child("time")) * 1000))
		pos.SetLatitude(kit.Double(n.child("longitude")))
		pos.SetLongitude(kit.Double(n.child("latitude")))
		pos.Altitude = kit.Double(n.child("altitude"))
		pos.Speed = kit.Double(n.child("speed"))

		pos.Set(model.KeySatellites, kit.Int(n.child("satellites")))

		positions = append(positions, pos)
	}

	return &ingest.HTTPResponse{Status: http.StatusOK}, positions, nil
}

// node is an element of a DOM parsed without namespace awareness (as Java's default
// DocumentBuilderFactory): names are the qualified names as written.
type node struct {
	name     string
	attrs    []xml.Attr
	children []*node
	text     strings.Builder // all descendant text, in order: the XPath string value
}

func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// parseXML returns the document element, or panics where the Java parser throws.
func parseXML(data []byte) *node {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var root *node
	var stack []*node
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
			n := &node{name: qname(t.Name), attrs: t.Attr}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			} else if root != nil {
				kit.Throw("SAXParseException: The markup in the document following the root element must be well-formed.")
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != qname(t.Name) {
				kit.Throw("SAXParseException: The element type must be terminated by the matching end-tag.")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			for _, n := range stack {
				n.text.Write(t)
			}
		case xml.Directive:
			if bytes.HasPrefix(t, []byte("DOCTYPE")) {
				kit.Throw("SAXParseException: DOCTYPE is disallowed when the feature \"http://apache.org/xml/features/disallow-doctype-decl\" set to true.")
			}
		}
	}
	if root == nil || len(stack) > 0 {
		kit.Throw("SAXParseException: Premature end of file.")
	}
	return root
}

// child is xPath.evaluate(name, n): the string value of the first child element name.
func (n *node) child(name string) string {
	for _, c := range n.children {
		if c.name == name {
			return c.text.String()
		}
	}
	return ""
}

// attr is Element.getAttribute ("" when missing).
func (n *node) attr(name string) (string, bool) {
	for _, a := range n.attrs {
		if qname(a.Name) == name {
			return a.Value, true
		}
	}
	return "", false
}

// find is the XPath //a/b/c from the document element n: every element named path's last
// step whose ancestors match the previous steps, in document order.
func (n *node) find(path ...string) []*node {
	var out []*node
	var walk func(e *node)
	match := func(e *node) {
		cur := []*node{e}
		for _, step := range path[1:] {
			var next []*node
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
	walk = func(e *node) {
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
