// Package l100 is the L100 protocol (L100Protocol): ATL text messages (GPRMC based, OBD
// and a newer '@'-terminated format) over TCP.
package l100

import (
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "l100",
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

// Framer is L100FrameDecoder.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}
		if buf.GetLatin1(buf.ReaderIndex(), 4) == "ATL," {
			return frameNew(buf)
		}
		return frameOld(buf)
	}}
}

func frameOld(buf *kit.Buf) any {
	header := buf.GetByte(buf.ReaderIndex())
	obd := header == 'L' || header == 'H'

	var index int
	if obd {
		index = buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '*')
	} else {
		index = buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0x02)
		if index < 0 {
			index = buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0x04)
			if index < 0 {
				return nil
			}
		}
	}

	index += 2 // checksum

	if buf.WriterIndex() >= index {
		if !obd {
			buf.Skip(2) // header
		}
		frame := buf.ReadSlice(index - buf.ReaderIndex() - 2)
		buf.Skip(2) // footer
		return frame
	}
	return nil
}

func frameNew(buf *kit.Buf) any {
	index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '@')
	if index < 0 {
		return nil
	}
	if buf.WriterIndex() >= index+1 {
		frame := buf.ReadSlice(index - buf.ReaderIndex())
		buf.Skip(1) // delimiter
		return frame
	}
	return nil
}

var pattern = kit.NewPattern().
	Text("ATL").
	Expression(",[^,]+,").Optional().
	Number("(d{15}),"). // imei
	Text("$GPRMC,").
	Number("(dd)(dd)(dd)"). // time (hhmmss.sss)
	Number(".(ddd)").Optional().
	Expression(",([AV]),"). // validity
	Number("(d+)(dd.d+),"). // latitude
	Expression("([NS]),").
	Number("(d+)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.?d*)?,").    // speed
	Number("(d+.?d*)?,").    // course
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Any().
	Text("#").
	Number("([01]+),").      // io status
	Number("(d+.?d*|N.C),"). // adc
	Expression("[^,]*,").    // reserved
	Expression("[^,]*,").    // reserved
	Number("(d+.?d*),").     // odometer
	Number("(d+.?d*),").     // temperature
	Number("(d+.?d*),").     // battery
	Number("(d+),").         // rssi
	Number("(d+),").         // mcc
	Number("(d+),").         // mnc
	Number("(x+),").         // lac
	Number("(x+)").          // cid
	Any().
	Text("ATL").
	Compile()

var patternObdLocation = kit.NewPattern().
	Expression("[LH],"). // archive
	Text("ATL,").
	Number("(d{15}),"). // imei
	Number("(d+),").    // type
	Number("(d+),").    // index
	GroupBegin().
	Number("(dd)(dd)(dd),").   // time (hhmmss)
	Number("(dd)(dd)(dd),").   // date (ddmmyy)
	Expression("([AV]),").     // validity
	Number("(d+.d+);([NS]),"). // latitude
	Number("(d+.d+);([EW]),"). // longitude
	Number("(d+),").           // speed
	Number("(d+),").           // course
	Number("(d+.d+),").        // odometer
	Number("(d+.d+),").        // battery
	Number("(d+),").           // rssi
	Number("(d+),").           // mcc
	Number("(d+),").           // mnc
	Number("(d+),").           // lac
	Number("(x+),").           // cid
	Number("#(d)(d)(d)(d),").  // status
	Number("(d),").            // overspeed
	Text("ATL,").
	GroupEndWith("?").
	Compile()

var patternObdData = kit.NewPattern().
	Expression("[LH],"). // archive
	Text("ATLOBD,").
	Number("(d{15}),").      // imei
	Number("d+,").           // type
	Number("d+,").           // index
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Expression("[^,]+,").    // obd protocol
	Expression("(.+)").      // data
	Compile()

var patternNew = kit.NewPattern().
	GroupBegin().
	Text("ATL,").
	Expression("[LH],"). // archive
	Number("(d{15}),").  // imei
	GroupEndWith("?").
	Expression("([NPT]),").    // alarm
	Number("(dd)(dd)(dd),").   // date (ddmmyy)
	Number("(dd)(dd)(dd),").   // time (hhmmss)
	Expression("([AV]),").     // validity
	Number("(d+.d+),([NS]),"). // latitude
	Number("(d+.d+),([EW]),"). // longitude
	Number("(d+.?d*),").       // speed
	Expression("(?:GPS|GSM|INV),").
	Number("(d+),"). // battery
	Number("(d+),"). // mcc
	Number("(d+),"). // mnc
	Number("(d+),"). // lac
	Number("(d+)").  // cid
	Compile()

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	switch {
	case strings.HasPrefix(sentence, "L") || strings.HasPrefix(sentence, "H"):
		if kit.Len(sentence) >= 2 && strings.HasPrefix(kit.SubstringFrom(sentence, 2), "ATLOBD") {
			return decodeObdData(m, sentence)
		}
		return decodeObdLocation(m, sentence)
	case !strings.Contains(sentence, "$GPRMC"):
		return decodeNew(m, sentence)
	default:
		return decodeNormal(m, sentence)
	}
}

func decodeNormal(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	hour, minute, second := parser.MustInt(), parser.MustInt(), parser.MustInt()
	b := kit.NewDateBuilder(nil).SetTimeMillis(hour, minute, second, parser.NextIntDefault(0))

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	day, month, year := parser.MustInt(), parser.MustInt(), parser.MustInt()
	b.SetDateReverse(day, month, year)
	pos.SetTime(b.Date())

	pos.Set(model.KeyStatus, parser.NextNullable())
	pos.Set(model.PrefixAdc+"1", parser.NextNullable())
	pos.Set(model.KeyOdometer, parser.NextDouble())
	pos.Set(model.PrefixTemp+"1", parser.NextDouble())
	pos.Set(model.KeyBattery, parser.NextDouble())

	rssi := parser.MustInt()
	if rssi > 0 {
		mcc, mnc := parser.MustInt(), parser.MustInt()
		lac, cid := parser.MustHexInt(), parser.MustHexInt()
		pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
	}

	m.Send(string(rune(0x01)))
	return pos
}

func decodeObdLocation(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternObdLocation, sentence)
	if !parser.Matches() {
		return nil
	}
	imei := parser.Next()
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	typ := parser.MustInt()
	index := parser.MustInt()

	if typ == 1 {
		response := "@" + imei + ",00," + strconv.Itoa(index) + ","
		response += "*" + string(rune(kit.XORString(response)))
		m.Send(response)
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.SetTime(parser.NextDateTime(kit.HMS_DMY, nil, m.Now))
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	pos.Speed = float64(parser.MustInt())
	pos.Course = float64(parser.MustInt())

	pos.Set(model.KeyOdometer, parser.MustDouble()*1000)
	pos.Set(model.KeyBattery, parser.NextDouble())

	rssi := parser.MustInt()
	mcc, mnc, lac := parser.MustInt(), parser.MustInt(), parser.MustInt()
	cid := parser.MustHexInt()
	pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))

	pos.Set(model.KeyIgnition, parser.MustInt() == 1)
	parser.Next() // reserved

	switch parser.MustInt() {
	case 0:
		pos.AddAlarm(model.AlarmBraking)
	case 2:
		pos.AddAlarm(model.AlarmAcceleration)
	case 1:
		pos.AddAlarm(model.AlarmGeneral)
	}

	pos.Set(model.KeyCharge, parser.MustInt() == 1)

	if parser.MustInt() == 1 {
		pos.AddAlarm(model.AlarmOverspeed)
	}
	return pos
}

func decodeObdData(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternObdData, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	t := parser.NextDateTime(kit.HMS_DMY, nil, m.Now)
	m.LastLocation(pos, &t)

	for _, entry := range kit.Split(parser.Next(), ",") {
		values := kit.Split(entry, ":")
		if len(values) == 2 && kit.CharAt(values[1], 0) != 'X' {
			kit.ObdDecodeData(kit.IntRadix(kit.SubstringFrom(values[0], 2), 16),
				int64(kit.IntRadix(values[1], 16)), true).Add(pos)
		}
	}
	return pos
}

func decodeNew(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternNew, sentence)
	if !parser.Matches() {
		return nil
	}
	var ds *ingest.DeviceSession
	if imei := parser.NextNullable(); imei != nil {
		ds = m.Session(*imei)
	} else {
		ds = m.Session()
	}
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	switch parser.Next() {
	case "P":
		pos.AddAlarm(model.AlarmSos)
	case "T":
		pos.AddAlarm(model.AlarmTampering)
	}

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	pos.Speed = parser.MustDouble()

	pos.Set(model.KeyBattery, float64(parser.MustInt())/1000.0)

	mcc, mnc, lac := parser.MustInt(), parser.MustInt(), parser.MustInt()
	pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(parser.MustHexInt())))
	return pos
}
