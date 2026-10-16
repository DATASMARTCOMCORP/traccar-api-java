// Package globalstar is the Globalstar protocol (GlobalstarProtocol): HTTP posts of
// stuMessages XML (SmartOne/AtlasTrax payloads) or of JSON device reports.
package globalstar

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{Name: "globalstar", Servers: []ingest.Server{{HTTP: &Decoder{}}}}
}

// Decoder is GlobalstarProtocolDecoder. ModelOverride is BaseProtocolDecoder's
// setModelOverride: when set, it replaces the device model.
type Decoder struct {
	ModelOverride *string
}

func (d *Decoder) deviceModel(m *ingest.Message, ds *ingest.DeviceSession) string {
	if d.ModelOverride != nil {
		return *d.ModelOverride
	}
	return m.DeviceModel(ds)
}

// DecodeHTTP decodes one request. Errors are the cases where the Java decoder throws; the
// connection is then closed without a response.
func (d *Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, r *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)
	if r.Header.Get("Content-Type") == "application/json" {
		resp, positions = decodeJSON(m, body)
	} else {
		resp, positions = d.decodeXML(m, body)
	}
	return resp, positions, nil
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

func (n *node) attr(name string) (string, bool) {
	for _, a := range n.attrs {
		if qname(a.Name) == name {
			return a.Value, true
		}
	}
	return "", false
}

// messages is //stuMessages/stuMessage, in document order.
func messages(n *node, out []*node) []*node {
	if n.name == "stuMessages" {
		for _, c := range n.children {
			if c.name == "stuMessage" {
				out = append(out, c)
			}
		}
	}
	for _, c := range n.children {
		out = messages(c, out)
	}
	return out
}

func (d *Decoder) decodeXML(m *ingest.Message, body []byte) (*ingest.HTTPResponse, []*model.Position) {
	root := parseXML(body)

	positions := []*model.Position{}

	for _, n := range messages(root, nil) {
		ds := m.Session(n.child("esn"))
		if ds == nil {
			continue
		}

		atlas := strings.EqualFold("AtlasTrax", d.deviceModel(m, ds))
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.SetTime(time.UnixMilli(kit.Long(n.child("unixTime")) * 1000))

		buf := kit.NewBuf(kit.ParseHex(kit.SubstringFrom(n.child("payload"), 2)))

		flags := buf.ReadUnsignedByte()
		var typ int
		if atlas {
			typ = kit.ToInt(flags, 1)
			pos.Valid = true
			pos.Set(model.PrefixIn+"1", flags&(1<<1) == 0)
			pos.Set(model.PrefixIn+"2", flags&(1<<2) == 0)
			pos.Set(model.KeyCharge, flags&(1<<3) == 0)
			if flags&(1<<4) != 0 {
				pos.AddAlarm(model.AlarmVibration)
			}
			pos.Course = float64(kit.FromInt(flags, 5) * 45)
		} else {
			typ = kit.ToInt(flags, 2)
			if flags&(1<<2) != 0 {
				pos.Set("batteryReplace", true)
			}
			pos.Valid = flags&(1<<3) == 0
		}

		latitude := float64(buf.ReadUnsignedMedium()) * 90.0 / (1 << 23)
		if latitude > 90 {
			latitude -= 180
		}
		pos.SetLatitude(latitude)

		longitude := float64(buf.ReadUnsignedMedium()) * 180.0 / (1 << 23)
		if longitude > 180 {
			longitude -= 360
		}
		pos.SetLongitude(longitude)

		speed := 0
		if atlas {
			speed = buf.ReadUnsignedByte()
			pos.Speed = units.KnotsFromKph(float64(speed))
			pos.Set("batteryReplace", buf.ReadUnsignedByte()&(1<<7) != 0)
		} else if typ == 0 {
			pos.Set(model.KeyInput, kit.ToInt(buf.ReadUnsignedByte(), 4))
			other := buf.ReadUnsignedByte()
			if other&(1<<4) != 0 {
				pos.AddAlarm(model.AlarmVibration)
			}
			pos.Set(model.KeyMotion, other&(1<<6) != 0)
		}

		if speed != 0xff {
			positions = append(positions, pos)
		}
	}

	// document.getFirstChild().getAttributes().getNamedItem("messageID").getNodeValue()
	messageID, ok := root.attr("messageID")
	if !ok {
		kit.Throw("NullPointerException: messageID")
	}
	resp := &ingest.HTTPResponse{Status: http.StatusOK, Body: xmlResponse(m, messageID)}

	if len(positions) == 0 {
		return resp, nil
	}
	return resp, positions
}

// xmlResponse is the stuResponseMsg document as the JDK's identity transformer writes it:
// the namespace declaration first, then the attributes in name order (Xerces keeps them
// sorted), no indentation. The time is "dd/MM/yyyy hh:mm:ss z" in the server zone (a
// 12-hour clock without a marker, as the Java pattern says).
func xmlResponse(m *ingest.Message, messageID string) []byte {
	now := m.Now.In(m.Zone())
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="no"?>`)
	b.WriteString(`<stuResponseMsg xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`)
	attr := func(name, value string) {
		b.WriteString(" " + name + `="`)
		b.WriteString(escapeAttr(value))
		b.WriteString(`"`)
	}
	attr("correlationID", messageID)
	attr("deliveryTimeStamp", now.Format("02/01/2006 03:04:05 MST"))
	attr("messageID", "00000000000000000000000000000000")
	attr("xsi:noNamespaceSchemaLocation", "http://cody.glpconnect.com/XSD/StuResponse_Rev1_0.xsd")
	b.WriteString(`><state>pass</state><stateMessage>Store OK</stateMessage></stuResponseMsg>`)
	return []byte(b.String())
}

func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#10;", "\r", "&#13;", "\t", "&#9;").Replace(s)
}

var errNull = errors.New("NullPointerException")

// jsonObject is JsonObject.getJsonObject: a missing key is null (NullPointerException when
// used), another type a ClassCastException.
func jsonObject(o map[string]any, key string) map[string]any {
	v, ok := o[key]
	if !ok || v == nil {
		panic(errNull)
	}
	m, ok := v.(map[string]any)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	return m
}

// jsonString is JsonObject.getString: NullPointerException when missing,
// ClassCastException when not a string.
func jsonString(o map[string]any, key string) string {
	v, ok := o[key]
	if !ok {
		panic(errNull)
	}
	s, ok := v.(string)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	return s
}

// jsonLong is getJsonNumber(key).longValue(): the number truncated to a long.
func jsonLong(o map[string]any, key string) int64 {
	v, ok := o[key]
	if !ok || v == nil {
		panic(errNull)
	}
	n, ok := v.(json.Number)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	if i, err := n.Int64(); err == nil {
		return i
	}
	f, err := n.Float64()
	if err != nil {
		panic(err)
	}
	return int64(math.Trunc(f))
}

func decodeJSON(m *ingest.Message, body []byte) (*ingest.HTTPResponse, []*model.Position) {
	dec := json.NewDecoder(bytes.NewReader(body)) // toString(UTF_8)
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		panic(err)
	}
	devicesValue, ok := jsonObject(root, "entry")["devices"]
	if !ok || devicesValue == nil {
		panic(errNull)
	}
	devices, ok := devicesValue.([]any)
	if !ok {
		kit.Throw("ClassCastException: devices")
	}

	positions := []*model.Position{}

	for _, v := range devices {
		data, ok := v.(map[string]any)
		if !ok {
			kit.Throw("ClassCastException: device")
		}

		identify := jsonObject(data, "deviceIdentify")
		ds := m.Session(jsonString(identify, "esn"))
		if ds == nil {
			continue
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.SetTime(time.UnixMilli(jsonLong(identify, "unixTime") * 1000))

		gps := jsonObject(data, "gpsCoordinate")
		info := jsonObject(data, "deviceInfo")

		latitude := jsonString(gps, "latitude")
		longitude := jsonString(gps, "longitude")
		if latitude != "" && longitude != "" {
			pos.Valid = jsonString(info, "gpsDataValid") == "Valid"
			pos.SetLatitude(kit.Double(latitude))
			pos.SetLongitude(kit.Double(longitude))
		} else {
			t := pos.DeviceTime.Time
			m.LastLocation(pos, &t)
		}

		if jsonString(info, "batteryStatus") == "Low" {
			pos.Set(model.KeyAlarm, model.AlarmLowBattery)
		}

		positions = append(positions, pos)
	}

	resp := &ingest.HTTPResponse{Status: http.StatusOK, Body: kit.NettyBytes(`{"status":"ok"}`)}
	if len(positions) == 0 {
		return resp, nil
	}
	return resp, positions
}
