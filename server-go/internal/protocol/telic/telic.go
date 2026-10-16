// Package telic is the Telic protocol (TelicProtocol): comma-separated text over TCP,
// framed by a little-endian length or a zero byte.
package telic

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "telic",
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

// Framer is TelicFrameDecoder: a 4-byte little-endian length below 1024, or else text up
// to a zero byte (an empty frame is dropped).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 4 {
			return nil
		}
		length := buf.GetUnsignedIntLE(buf.ReaderIndex())
		if length < 1024 {
			if int64(buf.ReadableBytes()) >= length+4 {
				buf.ReadUnsignedIntLE()
				return buf.ReadSlice(int(length))
			}
		} else {
			endIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0)
			if endIndex >= 0 {
				frame := buf.ReadSlice(endIndex - buf.ReaderIndex())
				buf.ReadSignedByte()
				if frame.ReadableBytes() > 0 {
					return frame
				}
			}
		}
		return nil
	}}
}

var pattern = kit.NewPattern().
	Number("dddd").
	Number("(d{6}|d{15})"). // device id
	Number("(d{1,2}),").    // type
	Number("d{12},").       // event time
	Number("d+,").
	Number("(dd)(dd)(dd)").  // date (ddmmyy)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	GroupBegin().
	Number("(-?d{8,}),"). // longitude
	Number("(-?d{7,}),"). // latitude
	Or().
	Number("(-?d+),"). // longitude
	Number("(-?d+),"). // latitude
	GroupEnd().
	Number("(d),").   // validity
	Number("(d+),").  // speed
	Number("(d+),").  // course
	Number("(d+)?,"). // satellites
	Expression("(?:[^,]*,){7}").
	Number("(d+),"). // battery
	Any().
	Compile()

func decodeAlarm(eventID int) string {
	switch eventID {
	case 1:
		return model.AlarmPowerOn
	case 2:
		return model.AlarmSos
	case 5:
		return model.AlarmPowerOff
	case 7:
		return model.AlarmGeofenceEnter
	case 8:
		return model.AlarmGeofenceExit
	case 22:
		return model.AlarmLowBattery
	case 25:
		return model.AlarmMovement
	}
	return ""
}

// decode is TelicProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	event := parser.MustInt()
	pos.Set(model.KeyEvent, event)
	pos.AddAlarm(decodeAlarm(event))

	if event == 11 {
		pos.Set(model.KeyIgnition, true)
	} else if event == 12 {
		pos.Set(model.KeyIgnition, false)
	}

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, time.Time{}))

	if parser.HasNextN(2) {
		pos.SetLongitude(parser.MustDouble() / 1000000)
		pos.SetLatitude(parser.MustDouble() / 1000000)
	}

	if parser.HasNextN(2) {
		pos.SetLongitude(parser.MustDouble() / 10000)
		pos.SetLatitude(parser.MustDouble() / 10000)
	}

	pos.Valid = parser.MustInt() != 1
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Course = parser.MustDouble()

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyBattery, parser.NextInt())

	return pos
}
