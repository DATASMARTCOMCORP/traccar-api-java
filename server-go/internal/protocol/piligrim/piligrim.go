// Package piligrim is the Piligrim protocol (PiligrimProtocol): HTTP requests with binary
// position logs (/bingps) or SMS-style $GPRMC text (/push.do).
package piligrim

import (
	"context"
	"net/http"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{Name: "piligrim", Servers: []ingest.Server{{HTTP: Decoder{}}}}
}

const (
	msgGPS        = 0xF1
	msgGPSSensors = 0xF2
	msgEvents     = 0xF3
)

var pattern = kit.NewPattern().
	Expression("[^$]+").
	Text("$GPRMC,").
	Number("(dd)(dd)(dd).d+,"). // time (hhmmss)
	Expression("([AV]),").      // validity
	Number("(dd)(dd.d+),").     // latitude
	Expression("([NS]),").
	Number("(d{2,3})(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.d+),").      // speed
	Number("(d+.d+),").      // course
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Any().
	Compile()

// Decoder is PiligrimProtocolDecoder.
type Decoder struct{}

func response(message string) *ingest.HTTPResponse {
	return &ingest.HTTPResponse{Status: http.StatusOK, Body: kit.NettyBytes(message)}
}

// param is parameters().get(name).get(0): a missing parameter throws.
func param(params []kit.QueryParam, name string) string {
	for _, p := range params {
		if p.Name == name {
			return p.Values[0]
		}
	}
	kit.Throw("NullPointerException: no parameter %q", name)
	return ""
}

// DecodeHTTP decodes one request; Java's exceptions are panics.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, r *http.Request, body []byte) (*ingest.HTTPResponse, []*model.Position, error) {
	uri := kit.Latin1([]byte(r.RequestURI))

	switch {
	case strings.HasPrefix(uri, "/config"):
		return response("CONFIG: OK"), nil, nil
	case strings.HasPrefix(uri, "/addlog"):
		return response("ADDLOG: OK"), nil, nil
	case strings.HasPrefix(uri, "/inform"):
		return response("INFORM: OK"), nil, nil
	case strings.HasPrefix(uri, "/bingps"):
		resp := response("BINGPS: OK")
		params, err := kit.QueryParams(uri, true)
		if err != nil {
			return resp, nil, err
		}
		ds := m.Session(param(params, "imei"))
		if ds == nil {
			return resp, nil, nil
		}
		return resp, decodeBinary(m, ds, kit.NewBuf(body)), nil
	case strings.HasPrefix(uri, "/push.do"):
		resp := response("PUSH.DO: OK")
		pos := decodeText(m, kit.Latin1(body))
		if pos == nil {
			return resp, nil, nil
		}
		return resp, []*model.Position{pos}, nil
	}
	return nil, nil, nil
}

func decodeBinary(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) []*model.Position {
	positions := []*model.Position{}

	for buf.ReadableBytes() > 2 {
		buf.ReadUnsignedByte() // header
		kind := buf.ReadUnsignedByte()
		buf.ReadUnsignedByte() // length

		switch kind {
		case msgGPS, msgGPSSensors:
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID

			date := kit.NewDateBuilder(nil).SetDay(buf.ReadUnsignedByte())
			date.SetMonth(int(buf.GetByte(buf.ReaderIndex())) & 0x0f)
			date.SetYear(2010 + buf.ReadUnsignedByte()>>4)
			date.SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
			pos.SetTime(date.Date())

			latitude := float64(buf.ReadUnsignedByte())
			latitude += float64(buf.ReadUnsignedByte()) / 60.0
			latitude += float64(buf.ReadUnsignedByte()) / 6000.0
			latitude += float64(buf.ReadUnsignedByte()) / 600000.0

			longitude := float64(buf.ReadUnsignedByte())
			longitude += float64(buf.ReadUnsignedByte()) / 60.0
			longitude += float64(buf.ReadUnsignedByte()) / 6000.0
			longitude += float64(buf.ReadUnsignedByte()) / 600000.0

			flags := buf.ReadUnsignedByte()
			if kit.Check(int64(flags), 0) {
				latitude = -latitude
			}
			if kit.Check(int64(flags), 1) {
				longitude = -longitude
			}
			pos.SetLatitude(latitude)
			pos.SetLongitude(longitude)

			satellites := buf.ReadUnsignedByte()
			pos.Set(model.KeySatellites, satellites)
			pos.Valid = satellites >= 3

			pos.Speed = float64(buf.ReadUnsignedByte())

			course := float64(buf.ReadUnsignedByte() << 1)
			course += float64((flags >> 2) & 1)
			course += float64(buf.ReadUnsignedByte()) / 100.0
			pos.Course = course

			if kind == msgGPSSensors {
				power := float64(buf.ReadUnsignedByte())
				power += float64(buf.ReadUnsignedByte() << 8)
				pos.Set(model.KeyPower, power/100.0)

				battery := float64(buf.ReadUnsignedByte())
				battery += float64(buf.ReadUnsignedByte() << 8)
				pos.Set(model.KeyBattery, battery/100.0)

				buf.Skip(6)
			}

			positions = append(positions, pos)

		case msgEvents:
			buf.Skip(13)
		}
	}

	return positions
}

func decodeText(m *ingest.Message, sentence string) *model.Position {
	parts := kit.Split(sentence, "&")
	phone := kit.SubstringFrom(parts[1], 16)
	ds := m.Session(phone)
	if ds == nil {
		return nil
	}

	parser := kit.NewParser(pattern, parts[2])
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	date := kit.NewDateBuilder(nil).SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.MustDouble()
	pos.Course = parser.MustDouble()

	date.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.SetTime(date.Date())

	return pos
}
