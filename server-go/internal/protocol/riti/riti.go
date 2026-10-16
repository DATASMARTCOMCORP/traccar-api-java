// Package riti is the Riti protocol (RitiProtocol): a binary header followed by a $GPRMC
// sentence.
package riti

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "riti",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			f := ingest.NewLengthFramer(ingest.MaxFrameLength, 105, 2, 3, 0)
			f.LittleEndian = true
			return ingest.Pipeline{Framer: f, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("$GPRMC,").
	Number("(dd)(dd)(dd).?d*,"). // time (hhmmss)
	Expression("([AV]),").       // validity
	Number("(dd)(dd.d+),").      // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.?d*)?,").   // speed
	Number("(d+.?d*)?,").   // course
	Number("(dd)(dd)(dd)"). // date (ddmmyy)
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // header

	pos := m.NewPosition()

	ds := m.Session(strconv.Itoa(buf.ReadUnsignedShort()))
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Set("mode", buf.ReadUnsignedByte())
	pos.Set(model.KeyCommand, buf.ReadUnsignedByte())
	pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/1000.0)

	buf.Skip(5)               // status
	buf.ReadUnsignedShortLE() // idleCount
	buf.ReadUnsignedShortLE() // idleTime in seconds

	pos.Set(model.KeyDistance, buf.ReadUnsignedIntLE())
	pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedIntLE())

	// Parse GPRMC
	end := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '*')
	gprmc := buf.GetString(buf.ReaderIndex(), end-buf.ReaderIndex())
	parser := kit.NewParser(pattern, gprmc)
	if !parser.Matches() {
		return nil
	}

	dateBuilder := kit.NewDateBuilder(nil).
		SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	dateBuilder.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(dateBuilder.Date())

	return pos
}
