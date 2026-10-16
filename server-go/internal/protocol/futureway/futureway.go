// Package futureway is the FutureWay protocol (FutureWayProtocol): text frames with a
// hex header carrying the length, holding logins or lines of GPS, Wi-Fi and LBS data.
package futureway

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "futureway",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is FutureWayFrameDecoder: the length is 8 hex chars after the first 2.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}
		length := int(kit.Hex(buf.GetLatin1(buf.ReaderIndex()+2, 8)).ReadInt()) + 17
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

var patternGPS = kit.NewPattern().
	Text("GPS:").
	Expression("([AV]),").         // validity
	Number("(dd)(dd)(dd)").        // date (yymmdd)
	Number("(dd)(dd)(dd),").       // time (hhmmss)
	GroupBegin().
	Number("(dd)(dd.d+)([NS]),").  // latitude
	Number("(ddd)(dd.d+)([EW]),"). // longitude
	Or().
	Number("(d+.d+)([NS]),"). // latitude
	Number("(d+.d+)([EW]),"). // longitude
	GroupEnd().
	Number("(d+.d+),"). // speed
	Number("(d+.d+)").  // course
	Compile()

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	header := kit.Hex(kit.Substring(sentence, 0, 16))
	sentence = kit.Substring(sentence, 16, kit.Len(sentence)-4)

	header.ReadUnsignedByte() // header
	header.ReadUnsignedInt()  // length
	msgType := header.ReadUnsignedByte()
	header.ReadUnsignedShort() // index

	switch msgType {
	case 0x20:
		m.Session(kit.SubstringFrom(kit.Split(sentence, ",")[1], 5))

	case 0xA0:
		ds := m.Session()
		if ds == nil {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		network := model.NewNetwork()

		for _, line := range kit.Split(sentence, "\r\n") {
			switch {
			case strings.HasPrefix(line, "GPS"):
				parser := kit.NewParser(patternGPS, line)
				if !parser.Matches() {
					return nil
				}
				pos.Valid = parser.Next() == "A"
				pos.SetTime(parser.NextDateTimeDefault())
				if parser.HasNextN(6) {
					pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
					pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
				}
				if parser.HasNextN(4) {
					pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
					pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
				}
				pos.Speed = parser.MustDouble()
				pos.Course = parser.MustDouble()

			case strings.HasPrefix(line, "WIFI"):
				if strings.Contains(line, ",") {
					for _, item := range kit.Split(kit.SubstringFrom(line, kit.IndexOf(line, ",")+1), "&") {
						values := kit.Split(item, "|")
						network.AddWifiAccessPoint(model.NewWifiAccessPoint(
							strings.ReplaceAll(values[1], "-", ":"), kit.Int(values[2])))
					}
				}

			case strings.HasPrefix(line, "LBS"):
				values := kit.Split(kit.SubstringFrom(line, len("LBS:")), ",")
				var lac, cid int
				if kit.Int(values[2]) > 65535 {
					cid = kit.Int(values[2])
					lac = kit.Int(values[3])
				} else {
					lac = kit.Int(values[2])
					cid = kit.Int(values[3])
				}
				network.AddCellTower(model.NewCellTower(kit.Int(values[0]), kit.Int(values[1]), lac, int64(cid)))
			}
		}

		// getCellTowers() is null without an LBS line: Java throws a NullPointerException.
		if network.CellTowers == nil {
			kit.Throw("NullPointerException: network.getCellTowers()")
		}
		pos.Network = network

		if pos.FixTime.IsZero() {
			m.LastLocation(pos, nil)
		}

		return pos
	}

	return nil
}
