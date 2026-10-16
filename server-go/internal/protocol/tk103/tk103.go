// Package tk103 is the TK103 protocol (Tk103Protocol): "(...)" text frames over TCP and
// UDP, with battery, cell, Wi-Fi, command result, VIN and BMS reports besides locations.
package tk103

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: a TCP and a UDP server.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	decodeLow := cfg.Bool("tk103.decodeLow", false)
	pipeline := func(framer bool) func() ingest.Pipeline {
		return func() ingest.Pipeline {
			pl := ingest.Pipeline{
				Text:    true,
				TextOut: true,
				Decoder: &Decoder{DecodeLow: decodeLow},
				Encoder: &Encoder{},
			}
			if framer {
				pl.Framer = Framer()
			}
			return pl
		}
	}
	return ingest.Protocol{
		Name:         "tk103",
		DataCommands: protocol.DataCommands("tk103"),
		Servers: []ingest.Server{
			{NewPipeline: pipeline(true)},
			{Datagram: true, NewPipeline: pipeline(false)},
		},
	}, nil
}

// Framer is Tk103FrameDecoder: a frame runs from '(' to the first ')' that follows an
// even number of '$' (free text between '$' may hold parentheses).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 2 {
			return nil
		}

		start := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '(')
		if start == -1 {
			buf.SetIndex(0, 0)
			return nil
		}

		end, counter := start, 0
		for ; ; end++ {
			freeText := end
			end = buf.IndexOf(end, buf.WriterIndex(), ')')
			if end == -1 {
				break
			}
			for ; ; freeText, counter = freeText+1, counter+1 {
				freeText = buf.IndexOf(freeText, end, '$')
				if freeText == -1 || freeText >= end {
					break
				}
			}
			if counter%2 == 0 {
				break
			}
		}

		if end == -1 {
			for buf.ReadableBytes() > 1024 {
				discardUntil := buf.IndexOf(buf.ReaderIndex()+1, buf.WriterIndex(), '(')
				if discardUntil == -1 {
					buf.SetIndex(0, 0)
				} else {
					buf.SetReaderIndex(discardUntil)
				}
			}
			return nil
		}

		buf.SetReaderIndex(start)
		return buf.ReadSlice(end + 1 - start)
	}}
}

// Decoder is Tk103ProtocolDecoder; DecodeLow is tk103.decodeLow.
type Decoder struct {
	DecodeLow bool
}

var pattern = kit.NewPattern().
	Text("(").Optional().
	GroupBegin().
	Expression("(.{12})"). // device id
	Or().
	Expression("([^,]+),"). // device id
	GroupEnd().
	Expression("(.{4}),?"). // command
	GroupBegin().
	Number("(d*)").
	Or().
	Text(",ALARM,").
	Number("(d),"). // alarm type
	Number("d+,").
	GroupEnd().
	Number("(dd)(dd)(dd),?"). // date (mmddyy if comma-delimited, otherwise yyddmm)
	Expression("([AV]),?").   // validity
	Number(" *(d*)(dd.d+)").  // latitude
	Expression("([NS]),?").
	Number(" *(d*)(dd.d+)"). // longitude
	Expression("([EW]),?").
	Number("([ d.]{1,5})(?:d*,)?"). // speed
	Number("(dd)(dd)(dd),?").       // time (hhmmss)
	GroupBegin().
	Number("(?:([ d.]{6})|(dd)),?"). // course
	Number("([01])").                // charge
	Number("([01])").                // ignition
	Number("(x)").                   // io
	Number("(x)").                   // io
	Number("(x)").                   // io
	Number("(xxx)").                 // fuel
	Number("L(x+)").                 // odometer
	Or().
	Number("(d+.d+)"). // course
	GroupEnd().
	Any().
	Number("([+-]ddd.d)?"). // temperature
	Text(")").Optional().
	Compile()

var patternBattery = kit.NewPattern().
	Text("(").Optional().
	Number("(d+),"). // device id
	Text("ZC20,").
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(d+),").         // battery level
	Number("(d+),").         // battery voltage
	Number("(d+),").         // power voltage
	Number("d+").            // installed
	Any().
	Compile()

var patternCell = kit.NewPattern().
	Text("(").
	Number("(d{12})").     // device id
	Expression(".{4}").    // type
	Number("(?:d{15})?,"). // imei
	Expression("(.+),").   // cell
	Number("(d{8})").      // odometer
	Text(")").
	Compile()

var patternNetwork = kit.NewPattern().
	Text("(").Optional().
	Number("(d{12})"). // device id
	Text("BZ00,").
	Number("(d+),"). // mcc
	Number("(d+),"). // mnc
	Number("(x+),"). // lac
	Number("(x+),"). // cid
	Any().
	Compile()

var patternLbsWifi = kit.NewPattern().
	Text("(").Optional().
	Number("(d+),").       // device id
	Expression("(.{4}),"). // command
	Number("(d+),").       // mcc
	Number("(d+),").       // mnc
	Number("(d+),").       // lac
	Number("(d+),").       // cid
	Number("(d+),").       // number of wifi macs
	Number(`((?:(?:xx:){5}(?:xx)\*[-+]?d+\*d+,)*)`).
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(dd)(dd)(dd)").  // time (hhmmss)
	Any().
	Compile()

var patternCommandResult = kit.NewPattern().
	Text("(").Optional().
	Number("(d+),").         // device id
	Expression(".{4},").     // command
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	// message: Java's \$([\s\S]*?)(?:\$|$), where $ also matches before a final line
	// terminator (RE2's $ is only the end)
	Expression(`\$(.*?)(?:\$|(?:\r\n|[\n\r\x{85}\x{2028}\x{2029}])?$)`).
	Any().
	Compile()

var patternVin = kit.NewPattern().
	Text("(").
	Number("(d+)").        // device id
	Expression("BV00").    // command
	Expression("(.{17})"). // vin
	Text(")").
	Compile()

func decodeAlarm(value int) string {
	switch value {
	case 1:
		return model.AlarmAccident
	case 2:
		return model.AlarmSos
	case 3:
		return model.AlarmVibration
	case 4:
		return model.AlarmLowSpeed
	case 5:
		return model.AlarmOverspeed
	case 6:
		return model.AlarmGeofenceExit
	}
	return ""
}

// decodeType reads the alarms and ignition of a message type; data nil is Java's null.
func decodeType(pos *model.Position, typ string, data *string) {
	switch typ {
	case "BQ81":
		if data == nil {
			kit.Throw("NumberFormatException: Cannot parse null string: null")
		}
		switch kit.Int(*data) {
		case 0:
			pos.AddAlarm(model.AlarmLowBattery)
		case 1:
			pos.AddAlarm(model.AlarmOverspeed)
		case 2:
			pos.AddAlarm(model.AlarmIdle)
		case 3:
			pos.AddAlarm(model.AlarmAcceleration)
		case 4:
			pos.AddAlarm(model.AlarmBraking)
		case 5:
			pos.AddAlarm(model.AlarmTemperature)
		}
	case "BO01":
		if data == nil {
			kit.Throw("NullPointerException")
		}
		pos.AddAlarm(decodeAlarm(int(kit.CharAt(*data, 0) - '0')))
	case "ZC11", "DW31", "DW51":
		pos.AddAlarm(model.AlarmMovement)
	case "ZC12", "DW32", "DW52":
		pos.AddAlarm(model.AlarmLowBattery)
	case "ZC13", "DW33", "DW53":
		pos.AddAlarm(model.AlarmPowerCut)
	case "ZC15", "DW35", "DW55":
		pos.Set(model.KeyIgnition, true)
	case "ZC16", "DW36", "DW56":
		pos.Set(model.KeyIgnition, false)
	case "ZC29", "DW42", "DW62":
		pos.Set(model.KeyIgnition, true)
	case "ZC17", "DW37", "DW57":
		pos.AddAlarm(model.AlarmRemoving)
	case "ZC25", "DW3E", "DW5E":
		pos.AddAlarm(model.AlarmSos)
	case "ZC26", "DW3F", "DW5F":
		pos.AddAlarm(model.AlarmTampering)
	case "ZC27", "DW40", "DW60":
		pos.AddAlarm(model.AlarmLowPower)
	}
}

func decodeBatteryLevel(value int) *int {
	var v int
	switch value {
	case 6:
		v = 100
	case 5:
		v = 80
	case 4:
		v = 50
	case 3:
		v = 20
	case 2:
		v = 10
	default:
		return nil
	}
	return &v
}

func (d *Decoder) decodeBattery(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternBattery, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	t := parser.NextDateTime(kit.DMY_HMS, nil, m.Now)
	m.LastLocation(pos, &t)

	if level := parser.NextIntDefault(0); level != 255 {
		pos.Set(model.KeyBatteryLevel, decodeBatteryLevel(level))
	}
	if battery := parser.NextIntDefault(0); battery != 65535 {
		pos.Set(model.KeyBattery, float64(battery)/100.0)
	}
	if power := parser.NextIntDefault(0); power != 65535 {
		pos.Set(model.KeyPower, float64(power)/10.0)
	}
	return pos
}

func (d *Decoder) decodeCell(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternCell, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	m.LastLocation(pos, nil)

	network := model.NewNetwork()
	for _, cell := range kit.Split(parser.Next(), "\n") {
		values := kit.Split(kit.Substring(cell, 1, kit.Len(cell)-1), ",")
		network.AddCellTower(model.NewCellTower(
			kit.Int(values[0]), kit.Int(values[1]), kit.Int(values[2]), int64(kit.Int(values[3]))))
	}
	pos.Network = network

	pos.Set(model.KeyOdometer, parser.NextLongRadix(16, 0))
	return pos
}

func (d *Decoder) decodeNetwork(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternNetwork, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	m.LastLocation(pos, nil)

	mcc, mnc := parser.NextIntDefault(0), parser.NextIntDefault(0)
	lac, cid := parser.NextHexIntDefault(0), parser.NextHexIntDefault(0)
	pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))
	return pos
}

func (d *Decoder) decodeLbsWifi(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternLbsWifi, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	zero := "0"
	decodeType(pos, parser.Next(), &zero)

	m.LastLocation(pos, nil)

	network := model.NewNetwork()
	mcc, mnc, lac, cid := parser.MustInt(), parser.MustInt(), parser.MustInt(), parser.MustInt()
	network.AddCellTower(model.NewCellTower(mcc, mnc, lac, int64(cid)))

	wifiCount := parser.MustInt()
	if parser.HasNext() {
		macs := kit.Split(parser.Next(), ",")
		if len(macs) == wifiCount {
			for i := range wifiCount {
				info := kit.SplitRegex(macs[i], `\*`)
				network.AddWifiAccessPoint(model.NewWifiAccessPointChannel(
					info[0], kit.Int(info[1]), kit.Int(info[2])))
			}
		}
	}
	pos.Network = network // the cell tower list is never null

	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	return pos
}

func (d *Decoder) decodeCommandResult(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternCommandResult, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	t := parser.NextDateTime(kit.DMY_HMS, nil, m.Now)
	m.LastLocation(pos, &t)

	pos.Set(model.KeyResult, parser.NextNullable())
	return pos
}

func (d *Decoder) decodeVin(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternVin, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	m.LastLocation(pos, nil)

	pos.Set(model.KeyVin, parser.NextNullable())
	return pos
}

// startsWithAt is String.startsWith(prefix, offset).
func startsWithAt(s, prefix string, offset int) bool {
	end := offset + kit.Len(prefix)
	return offset >= 0 && end <= kit.Len(s) && kit.Substring(s, offset, end) == prefix
}

func (d *Decoder) decodeBms(m *ingest.Message, sentence string) any {
	ds := m.Session(kit.Substring(sentence, 1, 13))
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	m.LastLocation(pos, nil)

	payload := kit.Substring(sentence, 1+12+4, kit.Len(sentence)-1)

	if startsWithAt(sentence, "BS50", 1+12) {
		buf := kit.NewBuf(kit.ParseHex(payload))

		buf.ReadUnsignedByte()
		buf.ReadUnsignedByte()
		buf.ReadUnsignedByte() // header

		batteryCount := buf.ReadUnsignedByte()
		for i := 1; i <= 24; i++ {
			voltage := buf.ReadUnsignedShortLE()
			if i <= batteryCount {
				pos.Set(model.KeyBattery+kit.Format("%d", i), float64(voltage)/1000.0)
			}
		}

		pos.Set(model.KeyCharge, buf.ReadUnsignedByte() == 0)
		pos.Set("current", float64(buf.ReadUnsignedShortLE())/10.0)
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/100.0)
		pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
		pos.Set("batteryOverheat", buf.ReadUnsignedByte() > 0)
		pos.Set("chargeProtection", buf.ReadUnsignedByte() > 0)
		pos.Set("dischargeProtection", buf.ReadUnsignedByte() > 0)
		buf.ReadUnsignedByte() // drop line
		buf.ReadUnsignedByte() // balanced
		pos.Set("cycles", buf.ReadUnsignedShortLE())
		pos.Set("faultAlarm", buf.ReadUnsignedByte())

		buf.Skip(6)

		temperatureCount := buf.ReadUnsignedByte()
		pos.Set("powerTemp", buf.ReadUnsignedByte()-40)
		pos.Set("equilibriumTemp", buf.ReadUnsignedByte()-40)
		for i := 1; i <= 7; i++ {
			temperature := buf.ReadUnsignedByte() - 40
			if i <= temperatureCount {
				pos.Set("batteryTemp"+kit.Format("%d", i), temperature)
			}
		}

		pos.Set("calibrationCapacity", float64(buf.ReadUnsignedShortLE())/100.0)
		pos.Set("dischargeCapacity", buf.ReadUnsignedIntLE())
	} else {
		for _, value := range kit.Split(payload, ",") {
			pair := kit.Split(value, ":")
			key := kit.IntRadix(pair[0], 16)
			buf := kit.NewBuf(kit.ParseHex(pair[1]))
			switch key {
			case 0x90:
				pos.Set("cumulativeVoltage", float64(buf.ReadUnsignedShortLE())/10.0)
				pos.Set("gatherVoltage", float64(buf.ReadUnsignedShortLE())/10.0)
				pos.Set("current", float64(buf.ReadUnsignedShortLE()-30000)/10.0)
				pos.Set("soc", float64(buf.ReadUnsignedShortLE())/10.0)
			case 0x91:
				pos.Set("maxCellVoltage", float64(buf.ReadUnsignedShortLE())/1000.0)
				pos.Set("maxCellVoltageCount", buf.ReadUnsignedByte())
				pos.Set("minCellVoltage", float64(buf.ReadUnsignedShortLE())/1000.0)
				pos.Set("minCellVoltageCount", buf.ReadUnsignedByte())
			case 0x92:
				pos.Set("maxTemp", buf.ReadUnsignedByte()-40)
				pos.Set("maxTempCount", buf.ReadUnsignedByte())
				pos.Set("minTemp", buf.ReadUnsignedByte()-40)
				pos.Set("minTempCount", buf.ReadUnsignedByte())
			case 0x96:
				buf.ReadUnsignedByte() // frame
				for buf.IsReadable() {
					index := buf.ReaderIndex()
					pos.Set("cellTemp"+kit.Format("%d", index), buf.ReadUnsignedByte()-40)
				}
			}
		}
	}
	return pos
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	if m.HasChannel() {
		id := kit.Substring(sentence, 1, 13)
		switch kit.Substring(sentence, 13, 17) {
		case "BP00":
			m.Send("(" + id + "AP01HSO)")
			return nil
		case "BP05":
			m.Send("(" + id + "AP05)")
		}
	}

	switch {
	case kit.IndexOf(sentence, "{") > 0 && kit.IndexOf(sentence, "}") > 0:
		return d.decodeCell(m, sentence)
	case strings.Contains(sentence, "ZC20"):
		return d.decodeBattery(m, sentence)
	case strings.Contains(sentence, "BZ00"):
		return d.decodeNetwork(m, sentence)
	case strings.Contains(sentence, "ZC03"):
		return d.decodeCommandResult(m, sentence)
	case strings.Contains(sentence, "DW5"):
		return d.decodeLbsWifi(m, sentence)
	case strings.Contains(sentence, "BV00"):
		return d.decodeVin(m, sentence)
	case strings.Contains(sentence, "BS50") || strings.Contains(sentence, "BS51"):
		return d.decodeBms(m, sentence)
	}

	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	var id string
	alternative := false
	if parser.HasNext() {
		id = parser.Next()
	}
	if parser.HasNext() {
		id = parser.Next()
		alternative = true
	}

	ds := m.Session(id)
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	typ := parser.Next()
	var data *string
	if parser.HasNext() {
		data = parser.NextNullable()
	}
	if parser.HasNext() {
		data = parser.NextNullable()
	}
	decodeType(pos, typ, data)

	db := kit.NewDateBuilder(nil)
	if alternative {
		db.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	} else {
		db.SetDate(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	}

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))

	pos.Speed = m.ConvertSpeed(parser.NextDoubleDefault(0), "kmh")

	db.SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(db.Date())

	if parser.HasNext() {
		pos.Course = parser.MustDouble()
	}
	if parser.HasNext() {
		pos.Course = parser.MustDouble()
	}

	if parser.HasNextN(7) {
		pos.Set(model.KeyCharge, parser.MustInt() == 0)
		pos.Set(model.KeyIgnition, parser.MustInt() == 1)

		bit := func(mask, i int) int {
			if kit.Check(int64(mask), i) {
				return 1
			}
			return 0
		}

		mask1 := parser.MustHexInt()
		pos.Set(model.PrefixIn+"2", bit(mask1, 0))
		pos.Set("panic", bit(mask1, 1))
		pos.Set(model.PrefixOut+"2", bit(mask1, 2))
		if d.DecodeLow || kit.Check(int64(mask1), 3) {
			pos.Set(model.KeyBlocked, bit(mask1, 3))
		}

		mask2 := parser.MustHexInt()
		for i := range 3 {
			if d.DecodeLow || kit.Check(int64(mask2), i) {
				pos.Set("hs"+kit.Format("%d", 3-i), bit(mask2, i))
			}
		}
		if d.DecodeLow || kit.Check(int64(mask2), 3) {
			pos.Set(model.KeyDoor, bit(mask2, 3))
		}

		mask3 := parser.MustHexInt()
		for i := 1; i <= 3; i++ {
			if d.DecodeLow || kit.Check(int64(mask3), i) {
				pos.Set("ls"+kit.Format("%d", 3-i+1), bit(mask3, i))
			}
		}

		pos.Set(model.KeyFuel, parser.NextHexInt())
		pos.Set(model.KeyOdometer, parser.NextLongRadix(16, 0))
	}

	if parser.HasNext() {
		pos.Course = parser.MustDouble()
	}

	if parser.HasNext() {
		pos.Set(model.PrefixTemp+"1", parser.NextDoubleDefault(0))
	}

	return pos
}

// Encoder is Tk103ProtocolEncoder; ForceAlternative selects the "[begin]sms2" forms
// whatever tk103.alternative says.
type Encoder struct {
	ForceAlternative bool
}

func (e *Encoder) Encode(m *ingest.Message, c *model.Command) any {
	alternative := false
	if v := m.LookupBool(c.DeviceID, m.Protocol()+".alternative", false, true); v != nil {
		alternative = *v
	}

	if !c.Attributes.Has(model.CommandKeyDevicePassword) {
		c.Attributes.Set(model.CommandKeyDevicePassword, m.DevicePassword(c.DeviceID, m.Protocol(), "123456"))
	}

	format := func(format string, keys ...string) string {
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), format, keys...)
	}
	alt := func(f string, keys ...string) string {
		return format("[begin]sms2,"+f+",[end]", keys...)
	}
	enable := kit.CommandBool(c, model.CommandKeyEnable)

	if alternative || e.ForceAlternative {
		switch c.Type {
		case model.CommandCustom:
			return alt("%s", model.CommandKeyData)
		case model.CommandPositionSingle:
			return alt("*getposl*")
		case model.CommandPositionPeriodic:
			return alt("*routetrack*99*")
		case model.CommandPositionStop:
			return alt("*routetrackoff*")
		case model.CommandGetVersion:
			return alt("*about*")
		case model.CommandPowerOff:
			return alt("*turnoff*")
		case model.CommandRebootDevice:
			return alt("88888888")
		case model.CommandGetDeviceStatus:
			return alt("*status*")
		case model.CommandIdentification:
			return alt("999999")
		case model.CommandModeDeepSleep:
			if enable {
				return alt("*sleep*2*")
			}
			return alt("*sleepoff*")
		case model.CommandModePowerSaving:
			if enable {
				return alt("*sleepv*")
			}
			return alt("*sleepoff*")
		case model.CommandAlarmSos:
			if enable {
				return alt("*soson*")
			}
			return alt("*sosoff*")
		case model.CommandSetConnection:
			server := kit.CommandString(c, model.CommandKeyServer)
			if server == nil {
				kit.Throw("NullPointerException")
			}
			return alt("*setip*"+strings.ReplaceAll(*server, ".", "*")+"*%s*", model.CommandKeyPort)
		case model.CommandSosNumber:
			return alt("*master*%s*%s*", model.CommandKeyDevicePassword, model.CommandKeyPhone)
		}
		return nil
	}

	switch c.Type {
	case model.CommandCustom:
		return format("(%s%s)", model.CommandKeyUniqueID, model.CommandKeyData)
	case model.CommandGetVersion:
		return format("(%sAP07)", model.CommandKeyUniqueID)
	case model.CommandRebootDevice:
		return format("(%sAT00)", model.CommandKeyUniqueID)
	case model.CommandSetOdometer:
		return format("(%sAX01)", model.CommandKeyUniqueID)
	case model.CommandPositionSingle:
		return format("(%sAP00)", model.CommandKeyUniqueID)
	case model.CommandPositionPeriodic:
		frequency := kit.Format("%04X", kit.CommandInt(c, model.CommandKeyFrequency))
		return format("(%sAR00"+frequency+"0000)", model.CommandKeyUniqueID)
	case model.CommandPositionStop:
		return format("(%sAR0000000000)", model.CommandKeyUniqueID)
	case model.CommandEngineStop:
		return format("(%sAV010)", model.CommandKeyUniqueID)
	case model.CommandEngineResume:
		return format("(%sAV011)", model.CommandKeyUniqueID)
	case model.CommandOutputControl:
		return format("(%sAV00%s)", model.CommandKeyUniqueID, model.CommandKeyData)
	}
	return nil
}
