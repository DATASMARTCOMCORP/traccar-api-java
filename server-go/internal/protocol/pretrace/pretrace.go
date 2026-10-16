// Package pretrace is the Pretrace protocol (PretraceProtocol): text reports in
// parentheses, "(" ... "^" checksum ")".
package pretrace

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "pretrace",
		DataCommands: protocol.DataCommands("pretrace"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, ")"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	Text("(").
	Number("(d{15})").       // imei
	Number("Uddd").          // type
	Number("d").             // gps type
	Expression("([AV])").    // validity
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd)").  // time (hhmmss)
	Number("(dd)(dd.dddd)"). // latitude
	Expression("([NS])").
	Number("(ddd)(dd.dddd)"). // longitude
	Expression("([EW])").
	Number("(ddd)").        // speed
	Number("(ddd)").        // course
	Number("(xxx)").        // altitude
	Number("(x{8})").       // odometer
	Number("(x)").          // satellites
	Number("(dd)").         // hdop
	Number("(dd)").         // gsm
	Expression("(.{8}),&"). // state
	Expression("(.+)?").    // optional data
	Text("^").
	Number("xx"). // checksum
	Compile()

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

	pos.Valid = parser.Next() == "A"

	pos.SetTime(parser.NextDateTimeDefault())

	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = units.KnotsFromKph(float64(parser.NextIntDefault(0)))
	pos.Course = float64(parser.NextIntDefault(0))
	pos.Altitude = float64(parser.NextHexIntDefault(0))

	pos.Set(model.KeyOdometer, parser.NextHexIntDefault(0))
	pos.Set(model.KeySatellites, parser.NextHexIntDefault(0))
	pos.Set(model.KeyHdop, parser.NextIntDefault(0))
	pos.Set(model.KeyRssi, parser.NextIntDefault(0))

	parser.Next() // state

	if parser.HasNext() {
		for _, value := range kit.Split(parser.Next(), ",") {
			switch kit.CharAt(value, 0) {
			case 'P':
				switch {
				case kit.CharAt(value, 1) != '1':
					pos.Set(model.KeyPower, float64(kit.IntRadix(kit.SubstringFrom(value, 2), 16))/100.0)
				case kit.CharAt(value, 4) == '%':
					pos.Set(model.KeyBatteryLevel, kit.Int(kit.Substring(value, 2, 4)))
				default:
					pos.Set(model.KeyBattery, float64(kit.IntRadix(kit.SubstringFrom(value, 2), 16))/100.0)
				}
			case 'T':
				temperature := float64(kit.IntRadix(kit.SubstringFrom(value, 2), 16)) * 0.25
				if kit.CharAt(value, 1) == '1' {
					pos.Set(model.KeyDeviceTemp, temperature)
				} else {
					pos.Set(model.PrefixTemp+strconv.Itoa(int(kit.CharAt(value, 1)-'0')), temperature)
				}
			case 'F':
				pos.Set("fuel"+strconv.Itoa(int(kit.CharAt(value, 1)-'0')),
					float64(kit.IntRadix(kit.SubstringFrom(value, 2), 16))/100.0)
			case 'R':
				pos.Set(model.KeyDriverUniqueID, kit.SubstringFrom(value, 3))
			}
		}
	}

	return pos
}

func formatCommand(uniqueID, data string) string {
	content := uniqueID + data
	return kit.Format("(%s^%02X)", content, kit.XORString(content))
}

// encode is PretraceProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	uniqueID := m.UniqueID(c.DeviceID)
	switch c.Type {
	case model.CommandCustom:
		data := "null"
		if v := kit.CommandString(c, model.CommandKeyData); v != nil {
			data = *v
		}
		return formatCommand(uniqueID, data)
	case model.CommandPositionPeriodic:
		return formatCommand(uniqueID, kit.Format("D221%1$d,%1$d,,", kit.CommandInt(c, model.CommandKeyFrequency)))
	}
	return nil
}
