// Package tek is the Tek protocol (TekProtocol): binary tank level and NMEA-like GPS
// messages over TCP.
package tek

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "tek",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is TekFrameDecoder: a 17-byte header whose bytes 15 and 16 give the body length.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: frame}
}

func frame(_ *ingest.Message, buf *kit.Buf) any {
	if buf.ReadableBytes() < 17 {
		return nil
	}
	// Absolute indexes, as in Java.
	length := 17 + buf.GetUnsignedByte(16) + (kit.FromInt(buf.GetUnsignedByte(15), 6) << 6)
	if buf.ReadableBytes() >= length {
		return buf.ReadSlice(length)
	}
	return nil
}

var pattern = kit.NewPattern().
	Number(",d+,").
	Number("(dd)(dd)(dd).d,"). // time (hhmmss)
	Number("(dd)(dd.d+)").     // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+)"). // longitude
	Expression("([EW]),").
	Number("(d+.d+),").      // hdop
	Number("(d+.d+),").      // altitude
	Number("(d+),").         // fix mode
	Number("(d+.d+),").      // course
	Number("d+.d+,").        // speed km
	Number("(d+.d+),").      // speed kn
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(d+),").         // satellites
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // product type
	buf.ReadUnsignedByte() // hardware version
	buf.ReadUnsignedByte() // firmware version
	buf.ReadUnsignedByte() // contact reason
	buf.ReadUnsignedByte() // alarm / status
	buf.ReadUnsignedByte() // rssi
	buf.ReadUnsignedByte() // battery / status

	imei := kit.SubstringFrom(buf.ReadSlice(8).HexDump(), 1)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	typ := kit.ToInt(buf.ReadUnsignedByte(), 6)
	buf.ReadUnsignedByte() // length

	switch typ {
	case 4, 8:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		count := buf.ReadUnsignedShort()
		buf.ReadUnsignedByte() // hours / tickets
		buf.ReadUnsignedByte() // error code
		buf.ReadUnsignedByte() // reserved
		buf.ReadUnsignedByte() // logger speed
		buf.ReadUnsignedByte() // login time
		buf.ReadUnsignedByte() // minutes

		for i := 0; i < count; i++ {
			n := kit.Format("%d", i+1)
			pos.Set("rssi"+n, buf.ReadUnsignedByte())
			pos.Set("temp"+n, buf.ReadUnsignedByte()-30)
			data := buf.ReadUnsignedShort()
			pos.Set("src"+n, kit.FromInt(data, 10))
			pos.Set("ullage"+n, kit.ToInt(data, 10))
		}
		return pos

	case 17:
		parser := kit.NewParser(pattern, kit.Latin1(buf.Bytes()))
		if !parser.Matches() {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		date := kit.NewDateBuilder(nil).SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
		pos.Set(model.KeyHdop, parser.NextDouble())
		pos.Altitude = parser.MustDouble()
		pos.Valid = parser.MustInt() > 0
		pos.Course = parser.MustDouble()
		pos.Speed = parser.MustDouble()
		date.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
		pos.SetTime(date.Date())
		return pos
	}

	return nil
}
