// Package vtfms is the VTFMS protocol (VtfmsProtocol): "(...)" text messages followed by
// a 3-digit checksum, over TCP and UDP.
package vtfms

import (
	"math"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: a TCP and a UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "vtfms",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  Framer(),
					Text:    true,
					Decoder: ingest.DecoderFunc(decode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Text:    true,
					Decoder: ingest.DecoderFunc(decode),
				}
			}},
		},
	}
}

// Framer is VtfmsFrameDecoder: a frame ends 3 bytes (the checksum) after ')'.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		endIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ')')
		if endIndex > 0 {
			endIndex += 1 + 3
			if buf.WriterIndex() >= endIndex {
				return buf.ReadSlice(endIndex - buf.ReaderIndex())
			}
		}
		return nil
	}}
}

var directions = []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}

var pattern = kit.NewPattern().
	Text("(").
	Number("(d{15}),").                // imei
	Number("[0-9A-Z]{3}dd,").          // packet count
	Number("(dd),").                   // packet id
	Number("[^,]*,").                  // reserved
	Number("(d+)?,").                  // rssi
	Number("(?:d+)?,").                // fix status
	Number("(d+)?,").                  // satellites
	Number("[^,]*,").                  // reserved
	Expression("([AV]),").             // validity
	Number("(dd)(dd)(dd),").           // time (hhmmss)
	Number("(dd)(dd)(dd),").           // time (ddmmyy)
	Number("(-?d+.d+),").              // latitude
	Number("(-?d+.d+),").              // longitude
	Number("(?:(d+)|([NESW]{1,2})),"). // course
	Number("(d+),").                   // speed
	Number("(d+),").                   // hours
	Number("(d+),").                   // idle hours
	Expression("[KNT],").              // antenna status
	Number("(d+),").                   // odometer
	Expression("([01]),").             // power status
	Number("(d+.d+),").                // power voltage
	Number("[^,]*,").                  // reserved
	Number("(d+)?,").                  // fuel level
	Number("(d+.d+)?,").               // adc 1
	Number("[^,]*,").                  // reserved
	Number("(d+.d+)?,").               // adc 2
	Expression("([01]),").             // di 1
	Expression("([01]),").             // di 2
	Expression("([01]),").             // di 3
	Expression("([01]),").             // di 4
	Expression("([01]),").             // do 1
	Expression("([01]),").             // do 2
	Expression("([01]),").             // do 3
	Number("[^,]*,").                  // reserved
	Number("[^,]*").                   // reserved
	Text(")").
	Number("ddd"). // checksum
	Compile()

func decodeAlarm(value int) string {
	switch value {
	case 10:
		return model.AlarmOverspeed
	case 14:
		return model.AlarmPowerCut
	case 15:
		return model.AlarmPowerRestored
	case 32:
		return model.AlarmBraking
	case 33:
		return model.AlarmAcceleration
	}
	return ""
}

func convertToDegrees(value float64) float64 {
	degrees := math.Floor(value / 100)
	return degrees + (value-degrees*100)/60
}

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

	pos.AddAlarm(decodeAlarm(parser.MustInt()))
	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set(model.KeySatellites, parser.NextInt())

	pos.Valid = parser.Next() == "A"
	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, nil, m.Now))

	latitude := parser.MustDouble()
	longitude := parser.MustDouble()
	if math.Abs(latitude) > 90 || math.Abs(longitude) > 180 {
		pos.SetLatitude(convertToDegrees(latitude))
		pos.SetLongitude(convertToDegrees(longitude))
	} else {
		pos.SetLatitude(latitude)
		pos.SetLongitude(longitude)
	}

	pos.Course = parser.NextDoubleDefault(0)
	if parser.HasNext() {
		direction := parser.Next()
		for i, d := range directions {
			if direction == d {
				pos.Course = float64(i) * 45.0
				break
			}
		}
	}

	pos.Speed = units.KnotsFromKph(parser.MustDouble())

	pos.Set(model.KeyHours, units.MsFromHours(int64(parser.MustInt())))
	pos.Set("idleHours", parser.NextInt())
	pos.Set(model.KeyOdometer, parser.MustInt()*100)
	pos.Set(model.KeyCharge, parser.Next() == "1")
	pos.Set(model.KeyPower, parser.NextDouble())
	pos.Set(model.KeyFuel, parser.NextInt())
	pos.Set(model.PrefixAdc+"1", parser.NextDouble())
	pos.Set(model.PrefixAdc+"2", parser.NextDouble())
	pos.Set(model.PrefixIn+"1", parser.NextInt())
	pos.Set(model.PrefixIn+"2", parser.NextInt())
	pos.Set(model.PrefixIn+"3", parser.NextInt())
	pos.Set(model.PrefixIn+"4", parser.NextInt())
	pos.Set(model.PrefixOut+"1", parser.NextInt())
	pos.Set(model.PrefixOut+"2", parser.NextInt())
	pos.Set(model.PrefixOut+"3", parser.NextInt())

	return pos
}
