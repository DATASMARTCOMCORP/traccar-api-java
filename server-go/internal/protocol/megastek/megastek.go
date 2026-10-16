// Package megastek is the Megastek protocol (MegastekProtocol): text reports, the old
// format (a $GPRMC sentence and a status) and the new "$MGV" one.
package megastek

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "megastek",
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

// Framer is MegastekFrameDecoder: frames with a 4-digit length prefix, or text ended by
// CRLF, '!' or LF.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}
		if b := buf.GetByte(buf.ReaderIndex()); b >= '0' && b <= '9' {
			length := 4 + kit.Int(buf.GetLatin1(buf.ReaderIndex(), 4))
			if buf.ReadableBytes() >= length {
				return buf.ReadSlice(length)
			}
		} else {
			for buf.GetByte(buf.ReaderIndex()) == '\r' || buf.GetByte(buf.ReaderIndex()) == '\n' {
				buf.Skip(1)
			}
			delimiter := buf.IndexOfIn("\r\n", buf.ReaderIndex(), buf.WriterIndex())
			if delimiter == -1 {
				delimiter = buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '!')
			}
			if delimiter == -1 {
				delimiter = buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '\n')
			}
			if delimiter != -1 {
				result := buf.ReadSlice(delimiter - buf.ReaderIndex())
				buf.Skip(1)
				return result
			}
		}
		return nil
	}}
}

var patternGprmc = kit.NewPattern().
	Text("$GPRMC,").
	Number("(dd)(dd)(dd).(ddd),"). // time (hhmmss.sss)
	Expression("([AV]),").         // validity
	Number("(d+)(dd.d+),([NS]),"). // latitude
	Number("(d+)(dd.d+),([EW]),"). // longitude
	Number("(d+.d+)?,").           // speed
	Number("(d+.d+)?,").           // course
	Number("(dd)(dd)(dd)").        // date (ddmmyy)
	Any().                         // checksum
	Compile()

var patternSimple = kit.NewPattern().
	Expression("[FL],").        // flag
	Expression("([^,]*),").     // alarm
	Number("imei:(d+),").       // imei
	Number("(d+/?d*)?,").       // satellites
	Number("(d+.d+)?,").        // altitude
	Number("Battery=(d+)%,,?"). // battery
	Number("(d)?,").            // charger
	Number("(d+)?,").           // mcc
	Number("(d+)?,").           // mnc
	Number("(xxxx),").          // lac
	Number("(xxxx);").          // cid
	Any().                      // checksum
	Compile()

var patternAlternative = kit.NewPattern().
	Number("(d+),").      // mcc
	Number("(d+),").      // mnc
	Number("(xxxx),").    // lac
	Number("(xxxx),").    // cid
	Number("(d+),").      // gsm signal
	Number("(d+),").      // battery
	Number("(d+),").      // flags
	Number("(d+),").      // inputs
	Number("(?:(d+),)?"). // outputs
	Number("(d.?d*),").   // adc 1
	GroupBegin().
	Number("(d.dd),"). // adc 2
	Number("(d.dd),"). // adc 3
	GroupEndWith("?").
	Expression("([^;]+);"). // alarm
	Any().                  // checksum
	Compile()

func parseLocation(location string, pos *model.Position) bool {
	parser := kit.NewParser(patternGprmc, location)
	if !parser.Matches() {
		return false
	}
	date := kit.NewDateBuilder(nil).SetTimeMillis(
		parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)
	date.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(date.Date())
	return true
}

func decodeOld(m *ingest.Message, sentence string) *model.Position {
	// Detect type
	simple := kit.CharAt(sentence, 3) == ',' || kit.CharAt(sentence, 6) == ','

	// Split message
	var id, location, status string
	if simple {
		begin := kit.IndexOf(sentence, ",") + 1
		end := kit.IndexOfFrom(sentence, ",", begin)
		id = kit.Substring(sentence, begin, end)
		begin = end + 1
		end = kit.IndexOfFrom(sentence, "*", begin)
		if end != -1 {
			end += 3
		} else {
			end = kit.Len(sentence)
		}
		location = kit.Substring(sentence, begin, end)
		begin = end + 1
		if begin > kit.Len(sentence) {
			begin = end
		}
		status = kit.SubstringFrom(sentence, begin)
	} else {
		begin := 3
		end := begin + 16
		id = kit.Trim(kit.Substring(sentence, begin, end))
		begin = end + 2
		end = kit.IndexOfFrom(sentence, "*", begin) + 3
		if end < 0 {
			return nil
		}
		location = kit.Substring(sentence, begin, end)
		begin = end + 1
		status = kit.SubstringFrom(sentence, begin)
	}

	pos := m.NewPosition()
	if !parseLocation(location, pos) {
		return nil
	}

	if simple {
		parser := kit.NewParser(patternSimple, status)
		if parser.Matches() {
			pos.AddAlarm(decodeAlarm(parser.Next()))

			ds := m.Session(parser.Next(), id)
			if ds == nil {
				return nil
			}
			pos.DeviceID = ds.DeviceID

			sat := parser.NextNullable()
			if sat == nil {
				kit.Throw("NullPointerException")
			}
			if strings.Contains(*sat, "/") {
				pos.Set(model.KeySatellites, kit.Int(kit.Split(*sat, "/")[0]))
				pos.Set(model.KeySatellitesVisible, kit.Int(kit.Split(*sat, "/")[1]))
			} else {
				pos.Set(model.KeySatellites, kit.Int(*sat))
			}

			pos.Altitude = parser.NextDoubleDefault(0)
			pos.Set(model.KeyBatteryLevel, parser.NextDoubleDefault(0))

			if charger := parser.NextNullable(); charger != nil {
				pos.Set(model.KeyCharge, kit.Int(*charger) == 1)
			}

			if parser.HasNextN(4) {
				pos.Network = model.NetworkOf(model.NewCellTower(
					parser.NextIntDefault(0), parser.NextIntDefault(0),
					parser.NextHexIntDefault(0), int64(parser.NextHexIntDefault(0))))
			}
		} else {
			ds := m.Session(id)
			if ds == nil {
				return nil
			}
			pos.DeviceID = ds.DeviceID
		}
	} else {
		parser := kit.NewParser(patternAlternative, status)
		if parser.Matches() {
			ds := m.Session(id)
			if ds == nil {
				return nil
			}
			pos.DeviceID = ds.DeviceID

			pos.Network = model.NetworkOf(model.NewCellTowerRSSI(
				parser.NextIntDefault(0), parser.NextIntDefault(0),
				parser.NextHexIntDefault(0), int64(parser.NextHexIntDefault(0)), parser.NextIntDefault(0)))

			pos.Set(model.KeyBatteryLevel, parser.NextDouble())
			pos.Set(model.KeyFlags, parser.NextNullable())
			pos.Set(model.KeyInput, parser.NextNullable())
			pos.Set(model.KeyOutput, parser.NextNullable())
			pos.Set(model.PrefixAdc+"1", parser.NextNullable())
			pos.Set(model.PrefixAdc+"2", parser.NextNullable())
			pos.Set(model.PrefixAdc+"3", parser.NextNullable())
			pos.AddAlarm(decodeAlarm(parser.Next()))
		}
	}
	return pos
}

var patternNew = kit.NewPattern().
	Number("dddd").Optional().
	Text("$MGV").
	Number("ddd,").
	Number("(d+),").      // imei
	Expression("[^,]*,"). // name
	Expression("([RS]),").
	Number("(dd)(dd)(dd),").       // date (ddmmyy)
	Number("(dd)(dd)(dd),").       // time (hhmmss)
	Expression("([AV]),").         // validity
	Number("(d+)(dd.d+),([NS]),"). // latitude
	Number("(d+)(dd.d+),([EW]),"). // longitude
	Number("dd,").
	Number("(dd),"). // satellites
	Number("dd,").
	Number("(d+.d+)?,").   // hdop
	Number("(d+.d+)?,").   // speed
	Number("(d+.d+)?,").   // course
	Number("(-?d+.d+)?,"). // altitude
	Number("(d+.d+)?,").   // odometer
	Number("(d+)?,").      // mcc
	Number("(d+)?,").      // mnc
	Number("(x+)?,").      // lac
	Number("(x+)?,").      // cid
	Number("(d+)?,").      // gsm
	GroupBegin().
	Number("(ddd),").Optional(). // heart rate
	Number("([01]{4})?,").       // input
	Number("([01]{4})?,").       // output
	Number("(d+)?,").            // adc1
	Number("(d+)?,").            // adc2
	Number("(d+)?,").            // adc3
	Or().
	Number("(d+),"). // input
	Number("(d+),"). // output
	Number("(d+),"). // adc1
	Number("(d+),"). // adc2
	Number("(d+),"). // adc3
	GroupEnd().
	GroupBegin().
	Number("(-?d+.?d*)"). // temperature 1
	Or().Text(" ").
	GroupEndWith("?").Text(",").
	GroupBegin().
	Number("(-?d+.?d*)"). // temperature 2
	Or().Text(" ").
	GroupEndWith("?").Text(",").
	GroupBegin().
	Number("(d+)?,"). // rfid
	GroupBegin().
	Number("([01])(d)?").Optional(). // charge and belt status
	Expression("[^,]*,").
	GroupEndWith("?").
	Number("(d+)?,").                     // battery
	Expression("([^,]*)[,;]").            // alert
	Expression("([^,]*)[,;]").Optional(). // wifi
	GroupEndWith("?").
	Any().
	Compile()

func decodeNew(m *ingest.Message, sentence string) *model.Position {
	parser := kit.NewParser(patternNew, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if parser.Next() == "S" {
		pos.Set(model.KeyArchive, true)
	}

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))

	pos.Set(model.KeySatellites, parser.NextIntDefault(0))
	pos.Set(model.KeyHdop, parser.NextDoubleDefault(0))

	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)
	pos.Altitude = parser.NextDoubleDefault(0)

	if parser.HasNext() {
		pos.Set(model.KeyOdometer, parser.NextDoubleDefault(0)*1000)
	}

	network := model.NewNetwork()

	if parser.HasNextN(5) {
		mcc := parser.MustInt()
		mnc := parser.MustInt()
		lac := parser.NextHexInt()
		cid := parser.NextHexInt()
		rssi := parser.NextInt()
		if lac != nil && cid != nil {
			tower := model.NewCellTower(mcc, mnc, *lac, int64(*cid))
			if rssi != nil {
				tower.SetSignalStrength(*rssi)
			}
			network.AddCellTower(tower)
		}
	}

	if parser.HasNextN(6) {
		pos.Set(model.KeyHeartRate, parser.NextInt())
		pos.Set(model.KeyInput, parser.NextBinIntDefault(0))
		pos.Set(model.KeyOutput, parser.NextBinIntDefault(0))
		for i := 1; i <= 3; i++ {
			pos.Set(model.PrefixAdc+kit.Format("%d", i), parser.NextIntDefault(0))
		}
	}

	if parser.HasNextN(5) {
		pos.Set(model.KeyHeartRate, parser.NextInt())
		pos.Set(model.KeySteps, parser.NextInt())
		pos.Set("activityTime", parser.NextInt())
		pos.Set("lightSleepTime", parser.NextInt())
		pos.Set("deepSleepTime", parser.NextInt())
	}

	for i := 1; i <= 2; i++ {
		if adc := parser.NextNullable(); adc != nil {
			pos.Set(model.PrefixTemp+kit.Format("%d", i), kit.Double(*adc))
		}
	}

	pos.Set(model.KeyDriverUniqueID, parser.NextNullable())

	if parser.HasNext() {
		pos.Set(model.KeyCharge, parser.MustInt() > 0)
	}
	if parser.HasNext() {
		pos.Set("belt", parser.NextInt())
	}
	if parser.HasNext() {
		pos.Set(model.KeyBatteryLevel, parser.NextInt())
	}
	if parser.HasNext() {
		pos.AddAlarm(decodeAlarm(parser.Next()))
	}
	if parser.HasNext() {
		for _, point := range kit.Split(parser.Next(), "|") {
			wifi := kit.Split(point, ":")
			mac := pairColons(wifi[0])
			network.AddWifiAccessPoint(model.NewWifiAccessPoint(
				kit.Substring(mac, 0, kit.Len(mac)-1), kit.Int(wifi[1])))
		}
	}

	if network.CellTowers != nil || network.WifiAccessPoints != nil {
		pos.Network = network
	}
	return pos
}

// pairColons is s.replaceAll("(..)", "$1:"): a colon after each pair of chars.
func pairColons(s string) string {
	var b strings.Builder
	n := kit.Len(s)
	for i := 0; i+2 <= n; i += 2 {
		b.WriteString(kit.Substring(s, i, i+2))
		b.WriteByte(':')
	}
	if n%2 == 1 {
		b.WriteString(kit.SubstringFrom(s, n-1))
	}
	return b.String()
}

func decodeAlarm(value string) string {
	value = strings.ToLower(value)
	if strings.HasPrefix(value, "geo") {
		if strings.HasSuffix(value, "in") {
			return model.AlarmGeofenceEnter
		} else if strings.HasSuffix(value, "out") {
			return model.AlarmGeofenceExit
		}
	}
	switch value {
	case "pw on", "poweron":
		return model.AlarmPowerOn
	case "poweroff":
		return model.AlarmPowerOff
	case "sos", "help":
		return model.AlarmSos
	case "over speed", "overspeed":
		return model.AlarmOverspeed
	case "lowspeed":
		return model.AlarmLowSpeed
	case "low battery", "lowbattery":
		return model.AlarmLowBattery
	case "low extern voltage":
		return model.AlarmLowPower
	case "gps cut":
		return model.AlarmGpsAntennaCut
	case "vib":
		return model.AlarmVibration
	case "move in":
		return model.AlarmGeofenceEnter
	case "move out":
		return model.AlarmGeofenceExit
	case "corner":
		return model.AlarmCornering
	case "fatigue":
		return model.AlarmFatigueDriving
	case "psd":
		return model.AlarmPowerCut
	case "psr":
		return model.AlarmPowerRestored
	case "hit":
		return model.AlarmVibration
	case "belt on", "belton":
		return model.AlarmLock
	case "belt off", "beltoff":
		return model.AlarmUnlock
	case "error":
		return model.AlarmFault
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	var pos *model.Position
	if strings.Contains(sentence, "$MG") {
		pos = decodeNew(m, sentence)
	} else {
		pos = decodeOld(m, sentence)
	}
	if pos == nil {
		return nil
	}
	return pos
}
