// Package its is the ITS protocol (ItsProtocol): AIS-140 style "$..." text reports from
// many vendors, ending with CRLF or '*'.
package its

import (
	"regexp"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "its",
		DataCommands: protocol.DataCommands("its"),
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

const minimumLength = 20

func readFrame(buf *kit.Buf, delimiterIndex, skip int) *kit.Buf {
	headerIndex := buf.IndexOf(buf.ReaderIndex()+1, buf.WriterIndex(), '$')
	if headerIndex > 0 && headerIndex < delimiterIndex {
		return buf.ReadSlice(headerIndex - buf.ReaderIndex())
	}
	frame := buf.ReadSlice(delimiterIndex - buf.ReaderIndex())
	buf.Skip(skip)
	return frame
}

// Framer is ItsFrameDecoder: from a '$' to CRLF or '*' (a binary checksum after ",*"
// is dropped). As in Java, the delimiter's absolute index is compared to the minimum
// length.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		for buf.IsReadable() && buf.GetByte(buf.ReaderIndex()) != '$' {
			buf.Skip(1)
		}
		delimiterIndex := buf.IndexOfIn("\r\n", buf.ReaderIndex(), buf.WriterIndex())
		if delimiterIndex > minimumLength {
			return readFrame(buf, delimiterIndex, 2)
		}
		delimiterIndex = buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '*')
		if delimiterIndex > minimumLength {
			if buf.WriterIndex() > delimiterIndex+1 && buf.GetByte(delimiterIndex+1) == '*' {
				delimiterIndex++
			}
			if buf.GetByte(delimiterIndex-2) == ',' {
				return readFrame(buf, delimiterIndex-1, 2) // skip binary checksum
			}
			return readFrame(buf, delimiterIndex, 1)
		}
		return nil
	}}
}

var pattern = kit.NewPattern().
	Expression("[^$]*").
	Text("$").
	Expression(",?[^,]+,"). // event
	GroupBegin().
	Expression("[^,]*,").           // vendor
	Expression("[^,]+,").           // firmware version
	Expression("(..),").            // status
	Number("(d+),").Optional().     // event
	Expression("([LH]),").          // history
	Or().
	Expression("([^,]+),"). // type
	GroupEnd().
	Number("(d{15}),"). // imei
	GroupBegin().
	Expression("([^,]{2}),"). // status
	Or().
	Expression("[^,]*,").       // vehicle registration
	Number("([01]),").Optional(). // valid
	GroupEnd().
	Number("(dd),?(dd),?(d{2,4}),?").    // date (ddmmyyyy)
	Number("(dd),?(dd),?(dd),").         // time (hhmmss)
	Expression("([01AV]),").Optional().  // valid
	Number("(d+.d+),([NS]),").           // latitude
	Number("(d+.d+),([EW]),").           // longitude
	GroupBegin().
	Number("(d+.?d*),"). // speed
	Number("(d+.?d*),"). // course
	Number("(d+),").     // satellites
	GroupBegin().
	Number("(d+.?d*),").     // altitude
	Number("(d+.?d*),").     // pdop
	Number("(d+.?d*),").     // hdop
	Expression("([^,]+)?,"). // operator
	Number("([01]),").       // ignition
	Number("([01]),").       // charging
	Number("(d+.?d*),").     // power
	Number("(d+.?d*),").     // battery
	Number("([01]),").       // emergency
	Expression("[COYN]?,").  // tamper
	Expression("(.*),").     // cells
	Number("([012]{4}),").   // inputs
	Number("([01]{2}),").    // outputs
	GroupBegin().
	Number("d+,").          // index
	Number("(d+.?d*),").    // odometer
	Number("(d+.?d*),").    // adc1
	Number("(-?d+.?d*),").  // acceleration x
	Number("(-?d+.?d*),").  // acceleration y
	Number("(-?d+.?d*),").  // acceleration z
	Number("(-?d+),").      // tilt y
	Number("(-?d+),").      // tilt x
	Or().
	Number("d+,").       // index
	Number("(d+.?d*),"). // odometer
	Number("x+,").       // checksum
	Or().
	Number("d+,").       // index
	Number("(d+.?d*),"). // adc1
	Number("(d+.?d*),"). // adc2
	Or().
	Number("(d+.d+),").     // adc1
	Number("(d+),").        // odometer
	Number("(d{6}),").      // index
	Expression("([^,]+),"). // response format
	Expression("([^,]+),"). // response
	GroupEndWith("?").
	GroupEndWith("?").
	Or().
	Number("(-?d+.d+),"). // altitude
	Number("(d+.d+),").   // speed
	GroupEnd().
	Any().
	Compile()

func decodeAlarm(status string) string {
	switch status {
	case "WD", "EA":
		return model.AlarmSos
	case "BL":
		return model.AlarmLowBattery
	case "HB":
		return model.AlarmBraking
	case "HA":
		return model.AlarmAcceleration
	case "RT":
		return model.AlarmCornering
	case "OS":
		return model.AlarmOverspeed
	case "TA":
		return model.AlarmTampering
	case "BD":
		return model.AlarmPowerCut
	case "BR":
		return model.AlarmPowerRestored
	}
	return ""
}

var validPattern = regexp.MustCompile(`^[1A]$`)

// javaDouble is a nullable Double in string concatenation.
func javaDouble(v *float64) string {
	if v == nil {
		return "null"
	}
	return model.JavaToString(*v)
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	if m.HasChannel() {
		switch {
		case strings.HasPrefix(sentence, "$,01,"):
			m.Send("$,1,*")
		case strings.HasPrefix(sentence, "$,LGN,"):
			m.Send("$LGN" + m.Now.UTC().Format("02012006150405") + "*")
		case strings.HasPrefix(sentence, "$,HBT,"):
			m.Send("$HBT*")
		}
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	status := parser.NextNullable()
	event := parser.NextInt()
	history := parser.Next() == "H"
	msgType := parser.NextNullable()

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if msgType != nil && *msgType == "EMR" {
		pos.AddAlarm(model.AlarmSos)
	}
	if event != nil {
		pos.Set(model.KeyEvent, *event)
	}
	if history {
		pos.Set(model.KeyArchive, true)
	}

	if parser.HasNext() {
		status = parser.NextNullable()
	}
	if status != nil {
		switch *status {
		case "IN":
			pos.Set(model.KeyIgnition, true)
		case "IF":
			pos.Set(model.KeyIgnition, false)
		default:
			pos.AddAlarm(decodeAlarm(*status))
		}
	}

	if parser.HasNext() {
		pos.Valid = parser.MustInt() == 1
	}
	zone, err := m.TimeZone(ds.DeviceID, "")
	if err != nil {
		panic(err)
	}
	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, zone, m.Now))
	if parser.HasNext() {
		pos.Valid = validPattern.MatchString(parser.Next())
	}
	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))

	if parser.HasNextN(3) {
		pos.Speed = units.KnotsFromKph(parser.MustDouble())
		pos.Course = parser.MustDouble()
		pos.Set(model.KeySatellites, parser.NextInt())
	}

	if parser.HasNextN(8) {
		pos.Altitude = parser.MustDouble()
		pos.Set(model.KeyPdop, parser.NextDouble())
		pos.Set(model.KeyHdop, parser.NextDouble())
		pos.Set(model.KeyOperator, parser.Next())
		pos.Set(model.KeyIgnition, parser.MustInt() > 0)
		pos.Set(model.KeyCharge, parser.MustInt() > 0)
		pos.Set(model.KeyPower, parser.NextDouble())
		pos.Set(model.KeyBattery, parser.NextDouble())

		pos.Set("emergency", parser.MustInt() > 0)

		cellsString := parser.Next()
		if !strings.Contains(cellsString, "x") {
			cells := kit.Split(cellsString, ",")
			mcc := kit.Int(cells[1])
			mnc := kit.Int(cells[2])
			lac := kit.IntRadix(cells[3], 16)
			cid := kit.IntRadix(cells[4], 16)
			network := model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), kit.Int(cells[0])))
			if len(cells) > 5 && !strings.HasPrefix(cells[5], "(") {
				for i := 0; i < 4; i++ {
					lac = kit.IntRadix(cells[5+3*i+1], 16)
					cid = kit.IntRadix(cells[5+3*i+2], 16)
					if lac > 0 && cid > 0 {
						network.AddCellTower(model.NewCellTower(mcc, mnc, lac, int64(cid)))
					}
				}
			}
			pos.Network = network
		}

		input := parser.Next()
		if kit.CharAt(input, kit.Len(input)-1) == '2' {
			input = kit.Substring(input, 0, kit.Len(input)-1) + "0"
		}
		pos.Set(model.KeyInput, kit.IntRadix(input, 2))
		pos.Set(model.KeyOutput, parser.NextBinInt())
	}

	if parser.HasNextN(7) {
		pos.Set(model.KeyOdometer, parser.MustDouble()*1000)
		pos.Set(model.PrefixAdc+"1", parser.NextDouble())
		x, y, z := parser.NextDouble(), parser.NextDouble(), parser.NextDouble()
		pos.Set(model.KeyGSensor, "["+javaDouble(x)+","+javaDouble(y)+","+javaDouble(z)+"]")
		pos.Set("tiltY", parser.NextInt())
		pos.Set("tiltX", parser.NextInt())
	}

	if parser.HasNext() {
		pos.Set(model.KeyOdometer, parser.MustDouble()*1000)
	}

	if parser.HasNextN(2) {
		pos.Set(model.PrefixAdc+"1", parser.NextDouble())
		pos.Set(model.PrefixAdc+"2", parser.NextDouble())
	}

	if parser.HasNextN(5) {
		pos.Set(model.PrefixAdc+"1", parser.NextDouble())
		pos.Set(model.KeyOdometer, parser.NextInt())
		pos.Set(model.KeyIndex, parser.NextInt())
		pos.Set("responseFormat", parser.Next())
		pos.Set("response", parser.Next())
	}

	if parser.HasNextN(2) {
		pos.Altitude = parser.MustDouble()
		pos.Speed = units.KnotsFromKph(parser.MustDouble())
	}

	return pos
}

func encode(_ *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandEngineStop:
		return "@SET#RLP,OP1,"
	case model.CommandEngineResume:
		return "@CLR#RLP,OP1,"
	}
	return nil
}
