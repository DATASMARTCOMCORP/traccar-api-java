// Package sabertek is the Sabertek protocol (SabertekProtocol): comma-separated text
// between STX and ETX, acknowledged with ACK or NAK.
package sabertek

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "sabertek",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Text: true, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is SabertekFrameDecoder: the text between 0x02 and 0x03, then a line ending.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		beginIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0x02)
		if beginIndex >= 0 {
			endIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0x03)
			if endIndex >= 0 {
				buf.SetReaderIndex(beginIndex + 1)
				frame := buf.ReadSlice(endIndex - beginIndex - 1)
				buf.SetReaderIndex(endIndex + 1)
				buf.Skip(2) // end line
				return frame
			}
		}
		return nil
	}}
}

var pattern = kit.NewPattern().
	Text(",").
	Number("(d+),"). // id
	Number("d,").    // type
	GroupBegin().
	Number("d+,").            // imei
	Number("d+,").            // scid
	Expression("[^,]*,").     // phone
	Number("(dddd)(dd)(dd)"). // date (yyyymmdd)
	Number("(dd)(dd)(dd),").  // time (hhmmss)
	GroupEndWith("?").
	Number("(d+),").      // battery
	Number("(d+),").      // rssi
	Number("(d+),").      // state
	Number("(d+),").      // events
	Number("(d),").       // valid
	Number("(-?d+.d+),"). // latitude
	Number("(-?d+.d+),"). // longitude
	Number("(d+),").      // speed
	Number("(d+),").      // course
	Number("(d+),").      // altitude
	Number("(d+),").      // satellites
	Number("(d+),").      // odometer
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds != nil {
		m.Send(kit.NewBuf([]byte{0x06}))
	} else {
		m.Send(kit.NewBuf([]byte{0x15}))
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if parser.HasNextN(6) {
		pos.SetTime(parser.NextDateTimeDefault())
	} else {
		pos.SetTime(m.Now)
	}

	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	pos.Set(model.KeyRssi, parser.NextInt())

	state := int64(parser.MustInt())
	pos.Set(model.KeyIgnition, kit.Check(state, 0))
	pos.Set(model.KeyCharge, kit.Check(state, 1))
	if kit.Check(state, 2) {
		pos.AddAlarm(model.AlarmJamming)
	}
	if kit.Check(state, 3) {
		pos.AddAlarm(model.AlarmTampering)
	}

	events := int64(parser.MustInt())
	if kit.Check(events, 0) {
		pos.AddAlarm(model.AlarmBraking)
	}
	if kit.Check(events, 1) {
		pos.AddAlarm(model.AlarmOverspeed)
	}
	if kit.Check(events, 2) {
		pos.AddAlarm(model.AlarmAccident)
	}
	if kit.Check(events, 3) {
		pos.AddAlarm(model.AlarmCornering)
	}

	pos.Valid = parser.MustInt() == 1
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Course = float64(parser.MustInt())
	pos.Altitude = float64(parser.MustInt())

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyOdometer, int64(parser.MustInt())*1000)

	return pos
}
