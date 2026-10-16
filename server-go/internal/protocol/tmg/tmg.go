// Package tmg is the TMG protocol (TmgProtocol): "$xxx,..." text lines, found after any
// binary noise in the stream.
package tmg

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "tmg",
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

func isLetter(c int8) bool { return c >= 'a' && c <= 'z' }

// findHeader finds "$" followed by three lowercase letters and a comma.
func findHeader(buf *kit.Buf) int {
	guessed := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '$')
	for guessed != -1 && buf.WriterIndex()-guessed >= 5 {
		if buf.GetByte(guessed+4) == ',' &&
			isLetter(buf.GetByte(guessed+1)) &&
			isLetter(buf.GetByte(guessed+2)) &&
			isLetter(buf.GetByte(guessed+3)) {
			return guessed
		}
		guessed = buf.IndexOf(guessed+1, buf.WriterIndex(), '$')
	}
	return -1
}

// Framer is TmgFrameDecoder: a line from a "$xxx," header to '\n'.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		begin := findHeader(buf)
		if begin >= 0 {
			buf.SetReaderIndex(begin)
			end := buf.IndexOf(begin, buf.WriterIndex(), '\n')
			if end >= 0 {
				frame := buf.ReadSlice(end - begin)
				buf.ReadSignedByte() // delimiter
				return frame
			}
		}
		return nil
	}}
}

var pattern = kit.NewPattern().
	Text("$").
	Expression("(...),").           // type
	Expression("[LH],").Optional(). // history
	Number("(d+),").                // imei
	Number("(dd)(dd)(dddd),").      // date (ddmmyyyy)
	Number("(dd)(dd)(dd),").        // time (hhmmss)
	Number("(d),").                 // status
	Number("(dd)(dd.d+),").         // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.?d*),"). // speed
	Number("(d+.?d*),"). // course
	GroupBegin().
	Number("(-?d+.?d*),").             // altitude
	Number("(d+.d+),").                // hdop
	Number("(d+),").                   // satellites
	Number("(d+),").                   // visible satellites
	Number("([^,]*),").                // operator
	Number("(d+),").                   // rssi
	Number("[^,]*,").                  // cid
	Expression("([01]),").             // ignition
	Number("(d+.?d*),").               // battery
	Number("(d+.?d*),").               // power
	Expression("([01]+),").            // input
	Expression("([01]+),").            // output
	Expression("[01]+,").              // temper status
	Number("(d+.?d*)[^,]*,").          // adc1
	Number("(d+.?d*)[^,]*,").          // adc2
	Number("d+.?d*,").                 // trip meter
	Expression("([^,]*),").            // software version
	Expression("([^,]*),").Optional(). // rfid
	Or().
	Number("[^,]*,").         // cid
	Number("(d+),").          // rssi
	Number("(d+),").          // satellites
	Number("[^,]*,").         // battery level
	Expression("([01]),").    // ignition
	Expression("([LH]{4}),"). // input
	Expression("[NT]{4},").   // tamper status
	Expression("([LH]{2}),"). // output
	Number("(d+.d+),").       // adc1
	Number("(d+.d+),").       // adc1
	Number("[^,]*,").         // device id
	Number("(d+),").          // odometer
	GroupEnd().
	Any().
	Compile()

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}

	typ := parser.Next()

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	switch typ {
	case "rmv":
		pos.AddAlarm(model.AlarmPowerCut)
	case "ebl":
		pos.AddAlarm(model.AlarmLowPower)
	case "ibl":
		pos.AddAlarm(model.AlarmLowBattery)
	case "tmp", "smt", "btt":
		pos.AddAlarm(model.AlarmTampering)
	case "ion":
		pos.Set(model.KeyIgnition, true)
	case "iof":
		pos.Set(model.KeyIgnition, false)
	}

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

	pos.Valid = parser.MustInt() > 0
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Course = parser.MustDouble()

	if parser.HasNextN(15) {
		pos.Altitude = parser.MustDouble()

		pos.Set(model.KeyHdop, parser.NextDouble())
		pos.Set(model.KeySatellites, parser.NextInt())
		pos.Set(model.KeySatellitesVisible, parser.NextInt())
		pos.Set(model.KeyOperator, parser.NextNullable())
		pos.Set(model.KeyRssi, parser.NextInt())
		pos.Set(model.KeyIgnition, parser.MustInt() == 1)
		pos.Set(model.KeyBattery, parser.NextDouble())
		pos.Set(model.KeyPower, parser.NextDouble())

		input := mustBin(parser)
		output := mustBin(parser)

		if !kit.Check(int64(input), 0) {
			pos.AddAlarm(model.AlarmSos)
		}

		pos.Set(model.KeyInput, input)
		pos.Set(model.KeyOutput, output)

		pos.Set(model.PrefixAdc+"1", parser.NextDouble())
		pos.Set(model.PrefixAdc+"2", parser.NextDouble())
		pos.Set(model.KeyVersionFw, parser.NextNullable())
		pos.Set(model.KeyDriverUniqueID, parser.NextNullable())
	}

	if parser.HasNextN(6) {
		pos.Set(model.KeyRssi, parser.NextInt())
		pos.Set(model.KeySatellites, parser.NextInt())
		pos.Set(model.KeyIgnition, parser.MustInt() == 1)

		for i, c := range []rune(parser.Next()) {
			pos.Set(model.PrefixIn+kit.Format("%d", i+1), c == 'H')
		}
		for i, c := range []rune(parser.Next()) {
			pos.Set(model.PrefixOut+kit.Format("%d", i+1), c == 'H')
		}

		pos.Set(model.PrefixAdc+"1", parser.NextDouble())
		pos.Set(model.PrefixAdc+"2", parser.NextDouble())
		pos.Set(model.KeyOdometer, parser.NextInt())
	}

	return pos
}

// mustBin is the unboxed parser.nextBinInt(): a missing group fails.
func mustBin(parser *kit.Parser) int {
	v := parser.NextBinInt()
	if v == nil {
		kit.Throw("NullPointerException: the group didn't match")
	}
	return *v
}
