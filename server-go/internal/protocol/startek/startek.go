// Package startek is the Startek protocol (StartekProtocol): "&&" text messages over TCP,
// framed by their length field.
package startek

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "startek",
		DataCommands: protocol.DataCommands("startek"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

// Framer is StartekFrameDecoder: the length after the 3-byte head counts from the comma
// that follows it, plus 4.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}
		lengthIndex := buf.ReaderIndex() + 3
		dividerIndex := buf.IndexOf(lengthIndex, buf.WriterIndex(), ',')
		if dividerIndex > 0 {
			lengthOffset := dividerIndex - buf.ReaderIndex() + 4
			length := lengthOffset + kit.Int(buf.GetString(lengthIndex, dividerIndex-lengthIndex))
			if buf.ReadableBytes() >= length {
				return buf.ReadSlice(length)
			}
		}
		return nil
	}}
}

var pattern = kit.NewPattern().
	Text("&&").
	Expression(".").    // index
	Number("d+,").      // length
	Number("(d+),").    // imei
	Number("(xxx),").   // type
	Expression("(.+)"). // content
	Number("xx").       // checksum
	Text("\r\n").
	Compile()

var patternPosition = kit.NewPattern().
	Number("(d+),").         // event
	Expression("([^,]+)?,"). // event data
	Number("(dd)(dd)(dd)").  // date (yyymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Expression("([AV]),").   // valid
	Number("(-?d+.d+),").    // longitude
	Number("(-?d+.d+),").    // latitude
	Number("(d+),").         // satellites
	Number("(d+.d+),").      // hdop
	Number("(d+),").         // speed
	Number("(d+),").         // course
	Number("(-?d+),").       // altitude
	Number("(d+),").         // odometer
	Number("(d+)|").         // mcc
	Number("(d+)|").         // mnc
	Number("(x+)|").         // lac
	Number("(x+),").         // cid
	Number("(d+),").         // rssi
	Number("(x+),").         // status
	Number("(x+),").         // inputs
	Number("(x+),").         // outputs
	Number("(x+)|").         // power
	Number("(x+)").          // battery
	Expression("([^,]+)?").  // adc
	GroupBegin().
	Number(",d+").           // extended
	Expression(",([^,]+)?"). // fuel
	GroupBegin().
	Expression(",([^,]+)?"). // temperature
	GroupBegin().
	Text(",").
	GroupBegin().
	Number("(d+)?|").              // rpm
	Number("(d+)?|").              // engine load
	Number("(d+)?|").              // maf flow
	Number("(d+)?|").              // intake pressure
	Number("(d+)?|").              // intake temperature
	Number("(d+)?|").              // throttle
	Number("(d+)?|").              // coolant temperature
	Number("(d+)?|").              // instant fuel
	Number("(d+)[%L]").Optional(). // fuel level
	GroupEndWith("?").
	Expression(",([^,]{20,})").Optional(). // driver id
	Number(",(d+)").Optional().            // hours
	GroupEndWith("?").
	GroupEndWith("?").
	GroupEndWith("?").
	Any().
	Compile()

func decodeAlarm(value int) string {
	switch value {
	case 1:
		return model.AlarmSos
	case 5, 6:
		return model.AlarmDoor
	case 17:
		return model.AlarmLowPower
	case 18:
		return model.AlarmPowerCut
	case 19:
		return model.AlarmPowerRestored
	case 39:
		return model.AlarmAcceleration
	case 40:
		return model.AlarmBraking
	case 41:
		return model.AlarmCornering
	}
	return ""
}

// decode is StartekProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	typ := parser.Next()
	content := parser.Next()
	switch typ {
	case "000":
		return decodePosition(m, ds, content)
	case "710":
		return decodeSerial(m, ds, content)
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)
	pos.Set(model.KeyType, typ)
	pos.Set(model.KeyResult, content)
	return pos
}

func decodePosition(m *ingest.Message, ds *ingest.DeviceSession, content string) any {
	parser := kit.NewParser(patternPosition, content)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	event := parser.MustInt()
	eventData := parser.Next()
	pos.Set(model.KeyEvent, event)
	if event == 53 {
		pos.Set(model.KeyDriverUniqueID, eventData)
	} else {
		pos.AddAlarm(decodeAlarm(event))
	}

	pos.SetTime(parser.NextDateTimeDefault())
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyHdop, parser.NextDouble())

	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Course = float64(parser.MustInt())
	pos.Altitude = float64(parser.MustInt())

	pos.Set(model.KeyOdometer, parser.NextLong())

	mcc, mnc := parser.MustInt(), parser.MustInt()
	lac, cid := parser.MustHexInt(), parser.MustHexInt()
	pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), parser.MustInt()))

	pos.Set(model.KeyStatus, parser.NextHexInt())

	input := parser.MustHexInt()
	output := parser.MustHexInt()
	pos.Set(model.KeyIgnition, kit.Check(int64(input), 1))
	pos.Set(model.KeyDoor, kit.Check(int64(input), 2))
	pos.Set(model.KeyInput, input)
	pos.Set(model.KeyOutput, output)

	pos.Set(model.KeyPower, float64(parser.MustHexInt())/100.0)
	pos.Set(model.KeyBattery, float64(parser.MustHexInt())/100.0)

	if parser.HasNext() {
		adc := kit.Split(parser.Next(), "|")
		for i := 1; i < len(adc); i++ {
			pos.Set(model.PrefixAdc+kit.Format("%d", i+1), float64(kit.IntRadix(adc[i], 16))/100.0)
		}
	}

	if parser.HasNext() {
		for _, fuel := range kit.Split(parser.Next(), "|") {
			index := kit.Int(kit.Substring(fuel, 0, 2))
			value := kit.IntRadix(kit.SubstringFrom(fuel, 2), 16)
			pos.Set("fuel"+kit.Format("%d", index), float64(value)/10.0)
		}
	}

	if parser.HasNext() {
		for _, temperature := range kit.Split(parser.Next(), "|") {
			index := kit.Int(kit.Substring(temperature, 0, 2))
			value := kit.IntRadix(kit.SubstringFrom(temperature, 2), 16)
			converted := float64(kit.To(int64(value), 15))
			if kit.Check(int64(value), 15) {
				converted = -converted
			}
			pos.Set(model.PrefixTemp+kit.Format("%d", index), converted/10.0)
		}
	}

	if parser.HasNextAny(9) {
		pos.Set(model.KeyRpm, parser.NextInt())
		pos.Set(model.KeyEngineLoad, parser.NextInt())
		pos.Set("airFlow", parser.NextInt())
		pos.Set("airPressure", parser.NextInt())
		if parser.HasNext() {
			pos.Set("airTemp", parser.MustInt()-40)
		}
		pos.Set(model.KeyThrottle, parser.NextInt())
		if parser.HasNext() {
			pos.Set(model.KeyCoolantTemp, parser.MustInt()-40)
		}
		if parser.HasNext() {
			pos.Set(model.KeyFuelConsumption, float64(parser.MustInt())/10.0)
		}
		pos.Set(model.KeyFuel, parser.NextInt())
	}

	if parser.HasNext() {
		pos.Set(model.KeyDriverUniqueID, parser.Next())
	}

	if parser.HasNext() {
		pos.Set(model.KeyHours, int64(parser.MustInt())*1000)
	}

	return pos
}

func decodeSerial(m *ingest.Message, ds *ingest.DeviceSession, content string) any {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	m.LastLocation(pos, nil)

	for _, frame := range kit.Split(content, "\r\n") {
		values := kit.Split(frame, ",")
		index := 0
		next := func() string {
			v := values[index]
			index++
			return v
		}
		switch next() {
		case "T1":
			index++ // speed
			pos.Set(model.KeyRpm, kit.Double(next()))
			index++ // fuel consumption
			pos.Set(model.KeyFuel, kit.Double(next()))
			index += 4 // axel weights
			index++    // turbo pressure
			pos.Set(model.KeyCoolantTemp, kit.Int(next()))
			index++ // accelerator pedal
			pos.Set("torque", kit.Int(next()))
			index++ // firmware version
			pos.Set(model.KeyPower, kit.Double(next()))
			index++ // coolant level
			pos.Set("oilTemp", kit.Double(next()))
			index++ // oil level
			pos.Set(model.KeyThrottle, kit.Double(next()))
			index += 4 // air inlet pressure, fuel tank secondary, current gear, seatbelt
			pos.Set("oilPressure", kit.Int(next()))
			index += 2 // wet tank air pressure, pto state
			ignition := kit.Int(next())
			if ignition < 2 {
				pos.Set(model.KeyIgnition, ignition > 0)
			}
			index++ // brake pedal
			pos.Set("catalystLevel", kit.Double(next()))
			index++ // fuel type
		case "T2":
			pos.Set(model.KeyOdometer, kit.Double(next())*1000)
			index += 15 // total fuel ... clutch apps
			pos.Set(model.KeyHours, kit.Int(next()))
			index++ // time torque
			pos.Set(model.KeyFuelConsumption, kit.Double(next()))
			index++ // total cruise control distance
			pos.Set(model.KeyFuelUsed, kit.Double(next()))
			index++ // total drive time
		}
	}

	return pos
}

// encode is StartekProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandCustom:
		return formatCommand(m, c, "%s", model.CommandKeyData)
	case model.CommandOutputControl:
		return formatCommand(m, c, "900,%s,%s", model.CommandKeyIndex, model.CommandKeyData)
	case model.CommandEngineStop:
		return formatCommand(m, c, "900,1,1")
	case model.CommandEngineResume:
		return formatCommand(m, c, "900,1,0")
	}
	return nil
}

func formatCommand(m *ingest.Message, c *model.Command, format string, keys ...string) string {
	uniqueID := m.UniqueID(c.DeviceID)
	payload := kit.FormatCommand(c, uniqueID, format, keys...)
	length := 1 + kit.Len(uniqueID) + 1 + kit.Len(payload)
	sentence := "$$:" + kit.Format("%d", length) + "," + uniqueID + "," + payload
	return sentence + kit.SumString(sentence) + "\r\n"
}
