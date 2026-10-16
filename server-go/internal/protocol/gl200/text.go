package gl200

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// This file is Gl200TextProtocolDecoder.

var dateFormat = kit.NewDateFormat("yyyyMMddHHmmss", time.UTC)

func parseDate(s string) time.Time { return dateFormat.Parse(s) }

var protocolModels = map[string]string{
	"02": "GL200", "04": "GV200", "06": "GV300", "08": "GMT100", "09": "GV50P", "0F": "GV55",
	"10": "GV55 LITE", "11": "GL500", "1A": "GL300", "1F": "GV500", "21": "GL200", "25": "GV300",
	"27": "GV300W", "28": "GL300VC", "2C": "GL300W", "2D": "GV500VC", "2F": "GV55", "4F": "GV56",
	"30": "GL300", "31": "GV65", "35": "GV200", "36": "GV500", "3F": "GMT100", "40": "GL500",
	"41": "GV75W", "42": "GT501", "44": "GL530", "45": "GB100", "50": "GV55W", "52": "GL50",
	"55": "GL50B", "5E": "GV500MAP", "6E": "GV310LAU", "BD": "CV200", "C2": "GV600M",
	"C3": "GL320M", "DC": "GV600MG", "DE": "GL500M", "DF": "CV100LG", "F1": "GV350M",
	"F8": "GV800W", "FC": "GV600W", "802004": "GV58LAU", "802005": "GV355CEU", "80201E": "GV30CEU",
}

// TextDecoder is Gl200TextProtocolDecoder.
type TextDecoder struct {
	Options
}

func deviceModel(ds *ingest.DeviceSession, protocolVersion string) string {
	if ds.Model != nil {
		return strings.ToUpper(*ds.Model)
	}
	var prefix string
	if kit.Len(protocolVersion) > 6 {
		prefix = kit.Substring(protocolVersion, 0, 6)
	} else {
		prefix = kit.Substring(protocolVersion, 0, 2)
	}
	return protocolModels[prefix]
}

func initPosition(m *ingest.Message, parser *kit.Parser) *model.Position {
	if parser.Matches() {
		if ds := m.Session(parser.Next()); ds != nil {
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID
			return pos
		}
	}
	return nil
}

func (d *TextDecoder) decodeDeviceTime(pos *model.Position, parser *kit.Parser) {
	if parser.HasNextN(6) {
		if d.IgnoreFixTime {
			pos.SetTime(parser.NextDateTimeDefault())
		} else {
			pos.DeviceTime = model.NewTime(parser.NextDateTimeDefault())
		}
	}
}

func (d *TextDecoder) setDeviceTime(pos *model.Position, t time.Time) {
	if d.IgnoreFixTime {
		pos.SetTime(t)
	} else {
		pos.DeviceTime = model.NewTime(t)
	}
}

func decodeAnalog(pos *model.Position, index int, adc string) {
	if strings.HasPrefix(adc, "F") {
		pos.Set("fuel"+strconv.Itoa(index), kit.Int(kit.SubstringFrom(adc, 1)))
	} else {
		pos.Set(model.PrefixAdc+strconv.Itoa(index), float64(kit.Int(adc))/1000.0)
	}
}

// parseHours gives the milliseconds of "h[:m[:s]]", nil (Java's null) for nil or "".
func parseHours(s *string) *int64 {
	if s == nil || *s == "" {
		return nil
	}
	hours := kit.Split(*s, ":")
	v := int64(kit.Int(hours[0])) * 3600
	if len(hours) > 1 {
		v += int64(kit.Int(hours[1])) * 60
	}
	if len(hours) > 2 {
		v += int64(kit.Int(hours[2]))
	}
	v *= 1000
	return &v
}

func str(s string) *string { return &s }

// values walks a split sentence as the Java code does with v[index++]; reading past the
// end panics, as Java's array access.
type values struct {
	v []string
	i int
}

func (c *values) next() string {
	s := c.v[c.i]
	c.i++
	return s
}

func (c *values) at(offset int) string { return c.v[c.i+offset] }

func (c *values) skip(n int) { c.i += n }

// javaIntCast is Java's (int) cast of a double.
func javaIntCast(f float64) int {
	switch {
	case f != f:
		return 0
	case f >= math.MaxInt32:
		return math.MaxInt32
	case f <= math.MinInt32:
		return math.MinInt32
	}
	return int(int32(f))
}

func (d *TextDecoder) decodeAck(m *ingest.Message, v []string) any {
	ds := m.Session(v[2])
	if ds == nil {
		return nil
	}
	if v[0] == "+ACK:GTHBD" {
		m.Send("+SACK:GTHBD," + v[1] + "," + v[len(v)-1] + "$")
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	t := parseDate(v[len(v)-2])
	m.LastLocation(pos, &t)
	pos.Valid = false
	pos.Set(model.KeyResult, v[0])
	return pos
}

func (d *TextDecoder) decodeInf(m *ingest.Message, all []string) any {
	v := &values{v: all}
	v.skip(1) // header
	protocolVersion := v.next()
	if kit.Len(protocolVersion) > 10 {
		return nil // gt300 protocol
	}

	ds := m.Session(v.next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	model_ := deviceModel(ds, protocolVersion)
	v.skip(1) // device name

	if s := v.next(); s != "" {
		switch kit.IntRadix(s, 16) {
		case 0x16, 0x1A, 0x12:
			pos.Set(model.KeyIgnition, false)
			pos.Set(model.KeyMotion, true)
		case 0x11:
			pos.Set(model.KeyIgnition, false)
			pos.Set(model.KeyMotion, false)
		case 0x21:
			pos.Set(model.KeyIgnition, true)
			pos.Set(model.KeyMotion, false)
		case 0x22:
			pos.Set(model.KeyIgnition, true)
			pos.Set(model.KeyMotion, true)
		case 0x41:
			pos.Set(model.KeyMotion, false)
		case 0x42:
			pos.Set(model.KeyMotion, true)
		}
	}

	pos.Set(model.KeyIccid, v.next())
	if s := v.next(); s != "" {
		pos.Set(model.KeyRssi, kit.Int(s))
	}
	v.skip(1) // signal quality
	v.skip(1) // external power supply

	if kit.Len(v.at(1)) >= 12 {
		v.skip(1) // ble sensor mac
		pos.Set(model.KeyDeviceTemp, kit.Int(v.next()))
		pos.Set(model.KeyHumidity, kit.Int(v.next()))
	}

	if s := v.next(); s != "" {
		if strings.Contains(s, ".") {
			pos.Set(model.KeyOdometer, kit.Double(s)*1000)
		} else {
			pos.Set(model.KeyPower, float64(kit.Int(s))/1000.0)
		}
	}
	if model_ != "GV500VC" {
		if model_ == "GV350M" || model_ == "GV310LAU" {
			v.skip(1) // expand mask or network type
		} else if s := v.next(); s != "" {
			pos.Set("power2", float64(kit.Int(s))/1000.0)
		}
	}

	if s := v.next(); s != "" {
		pos.Set(model.KeyBattery, kit.Double(s))
	}
	if v.next() != "" {
		if kit.Int(v.next()) == 1 {
			pos.Set(model.KeyCharge, true)
		}
	}

	if model_ == "GV310LAU" {
		v.skip(1) // led state
		v.skip(1) // power saving mode
		v.skip(1) // external antenna
		v.skip(1) // last fix time
		v.skip(1) // pin mask
		pos.Set(model.PrefixAdc+"1", kit.Int(v.next()))
		pos.Set(model.PrefixAdc+"2", kit.Int(v.next()))
		pos.Set(model.PrefixAdc+"3", kit.Int(v.next()))
	}

	d.setDeviceTime(pos, parseDate(all[len(all)-2]))

	t := pos.DeviceTime.Time
	m.LastLocation(pos, &t)

	return pos
}

var patternVer = kit.NewPattern().
	Text("+").Expression("(?:RESP|BUFF):GTVER,").
	Expression("(?:.{6}|.{10})?,"). // protocol version
	Number("(d{15}|x{14}),").       // imei
	Expression("[^,]*,").           // device name
	Expression("([^,]*),").         // device type
	Number("(xxxx),").              // firmware version
	Number("(xxxx),").              // hardware version
	Number("(dddd)(dd)(dd)").       // date (yyyymmdd)
	Number("(dd)(dd)(dd),").        // time (hhmmss)
	Number("(xxxx)").               // counter
	Text("$").Optional().
	Compile()

func decodeVer(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternVer, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	pos.Set("deviceType", parser.NextNullable())
	pos.Set(model.KeyVersionFw, parser.NextHexInt())
	pos.Set(model.KeyVersionHw, parser.NextHexInt())

	t := parser.NextDateTimeDefault()
	m.LastLocation(pos, &t)

	return pos
}

func skipLocation(parser *kit.Parser) { parser.Skip(20) }

var patternLocation = kit.NewPattern().
	Number("(d{1,2}.?d?)?,").            // hdop
	Number("(d{1,3}.d)?,").              // speed
	Number("(d{1,3}.?d?)?,").            // course
	Number("(-?d{1,5}.d)?,").            // altitude
	Number("(-?d{1,3}.d{6})?,").         // longitude
	Number("(-?d{1,2}.d{6})?,").         // latitude
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	GroupBegin().
	Number(",d+").                  // wifi count
	Number("((?:,x{12},-d+,,,)+)"). // wifi
	GroupEndWith("?").
	Text(",").
	Number("(d+)?,"). // mcc
	Number("(d+)?,"). // mnc
	GroupBegin().
	Number("(d+),"). // lac
	Number("(d+),"). // cid
	Or().
	Number("(x+)?,"). // lac
	Number("(x+)?,"). // cid
	GroupEnd().
	Number("(?:d+|(d+.d))?,"). // rssi / odometer
	String()

var patternLocationCompiled = kit.CompilePattern(patternLocation)

var pairs = regexp.MustCompile(`(..)`)

// macAddress is mac.replaceAll("(..)", "$1:") without the last character.
func macAddress(s string) string {
	mac := pairs.ReplaceAllString(s, "$1:")
	return kit.Substring(mac, 0, kit.Len(mac)-1)
}

func (d *TextDecoder) decodeLocationParser(m *ingest.Message, pos *model.Position, parser *kit.Parser) {
	hdop := parser.NextDouble()
	pos.Valid = hdop == nil || *hdop > 0
	pos.Set(model.KeyHdop, hdop)

	pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
	pos.Course = parser.NextDoubleDefault(0)
	pos.Altitude = parser.NextDoubleDefault(0)

	if parser.HasNextN(8) {
		pos.Valid = true
		pos.SetLongitude(parser.MustDouble())
		pos.SetLatitude(parser.MustDouble())
		pos.SetTime(parser.NextDateTimeDefault())
	} else {
		m.LastLocation(pos, nil)
	}

	network := model.NewNetwork()

	if parser.HasNext() {
		v := kit.Split(parser.Next(), ",")
		for i := 0; i < len(v); i += 5 {
			network.AddWifiAccessPoint(model.NewWifiAccessPoint(macAddress(v[i+1]), kit.Int(v[i+2])))
		}
	}

	if parser.HasNextN(6) {
		mcc := parser.MustInt()
		mnc := parser.MustInt()
		if parser.HasNextN(2) {
			lac := parser.MustInt()
			cid := parser.MustInt()
			network.AddCellTower(model.NewCellTower(mcc, mnc, lac, int64(cid)))
		}
		if parser.HasNextN(2) {
			lac := parser.MustHexInt()
			cid := parser.MustHexInt()
			network.AddCellTower(model.NewCellTower(mcc, mnc, lac, int64(cid)))
		}
	}

	if network.WifiAccessPoints != nil || network.CellTowers != nil {
		pos.Network = network
	}

	if parser.HasNext() {
		pos.Set(model.KeyOdometer, parser.MustDouble()*1000)
	}
}

func decodeLocationValues(m *ingest.Message, pos *model.Position, model_ string, v *values) {
	hdop := 0.0
	if s := v.next(); s != "" {
		hdop = kit.Double(s)
	}
	pos.Set(model.KeyHdop, hdop)

	speed := 0.0
	if s := v.next(); s != "" {
		speed = kit.Double(s)
	}
	pos.Speed = units.KnotsFromKph(speed)
	if s := v.next(); s != "" {
		pos.Course = float64(kit.Int(s))
	} else {
		pos.Course = 0
	}
	if s := v.next(); s != "" {
		pos.Altitude = kit.Double(s)
	} else {
		pos.Altitude = 0
	}

	if v.at(0) != "" {
		pos.Valid = true
		lon := 0.0
		if s := v.next(); s != "" {
			lon = kit.Double(s)
		}
		pos.SetLongitude(lon)
		lat := 0.0
		if s := v.next(); s != "" {
			lat = kit.Double(s)
		}
		pos.SetLatitude(lat)
		pos.SetTime(parseDate(v.next()))
	} else {
		v.skip(3)
		m.LastLocation(pos, nil)
	}

	network := model.NewNetwork()

	if v.at(0) != "" {
		mcc := kit.Int(v.next())
		mnc := kit.Int(v.next())
		lac := kit.IntRadix(v.next(), 16)
		cid := kit.LongRadix(v.next(), 16)
		network.AddCellTower(model.NewCellTower(mcc, mnc, lac, cid))
	} else {
		v.skip(4)
	}

	if network.WifiAccessPoints != nil || network.CellTowers != nil {
		pos.Network = network
	}

	if strings.HasPrefix(model_, "GL5") {
		v.skip(1) // csq rssi
		v.skip(1) // csq ber
	}

	if model_ != "GL320M" {
		if s := v.next(); s != "" {
			appendMask := kit.Int(s)
			if checkBit(appendMask, 0) {
				pos.Set(model.KeySatellites, kit.Int(v.next()))
			}
			if checkBit(appendMask, 1) {
				v.skip(1) // trigger type
			}
		}
	}
}

var patternObd = kit.NewPattern().
	Text("+RESP:GTOBD,").
	Expression("(?:.{6}|.{10})?,").   // protocol version
	Number("(d{15}|x{14}),").         // imei
	Expression("(?:[0-9A-Z]{17})?,"). // vin
	Expression("[^,]{0,20},").        // device name
	Expression("[01],").              // report type
	Number("x{1,8},").                // report mask
	Expression("(?:[0-9A-Z]{17})?,"). // vin
	Number("[01],").                  // obd connect
	Number("(?:d{1,5})?,").           // obd voltage
	Number("(?:x{8})?,").             // support pids
	Number("(d{1,5})?,").             // engine rpm
	Number("(d{1,3})?,").             // speed
	Number("(-?d{1,3})?,").           // coolant temp
	Number("(d+.?d*|Inf|NaN)?,").     // fuel consumption
	Number("(d{1,5})?,").             // dtcs cleared distance
	Number("(?:d{1,5})?,").
	Expression("([01])?,").     // obd connect
	Number("(d{1,3})?,").       // number of dtcs
	Number("(x*),").            // dtcs
	Number("(d{1,3})?,").       // throttle
	Number("(?:d{1,3})?,").     // engine load
	Number("(d{1,3})?,").       // fuel level
	Expression("(?:[0-9A],)?"). // obd protocol
	Number("(d+),").            // odometer
	Expression(patternLocation).
	Number("(d{1,7}.d)?,").              // odometer
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func (d *TextDecoder) decodeObd(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternObd, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	pos.Set(model.KeyRpm, parser.NextInt())
	pos.Set(model.KeyObdSpeed, parser.NextInt())
	pos.Set(model.PrefixTemp+"1", parser.NextInt())
	pos.Set(model.KeyFuelConsumption, parser.NextNullable())
	pos.Set("dtcsClearedDistance", parser.NextInt())
	if parser.HasNext() {
		pos.Set("odbConnect", parser.MustInt() == 1)
	}
	pos.Set("dtcsNumber", parser.NextInt())
	pos.Set("dtcsCodes", parser.NextNullable())
	pos.Set(model.KeyThrottle, parser.NextInt())
	pos.Set(model.KeyFuel, parser.NextInt())
	if parser.HasNext() {
		pos.Set(model.KeyObdOdometer, int(int32(parser.MustInt())*1000))
	}

	d.decodeLocationParser(m, pos, parser)

	if parser.HasNext() {
		pos.Set(model.KeyObdOdometer, javaIntCast(parser.MustDouble()*1000))
	}

	d.decodeDeviceTime(pos, parser)

	return pos
}

func (d *TextDecoder) decodeCan(m *ingest.Message, all []string) any {
	v := &values{v: all}
	v.skip(1) // header
	protocolVersion := v.next()
	ds := m.Session(v.next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	model_ := deviceModel(ds, protocolVersion)
	v.skip(1) // device name
	v.skip(1) // report type
	v.skip(1) // can bus state
	reportMask := kit.LongRadix(v.next(), 16)
	check := func(mask int64, i int) bool { return kit.Check(mask, i) }
	// field reads the next value when the mask bit is set, and reports whether it is
	// non-empty (Java's check(mask, i) && !v[index++].isEmpty()).
	field := func(mask int64, i int) (string, bool) {
		if !check(mask, i) {
			return "", false
		}
		s := v.next()
		return s, s != ""
	}

	if check(reportMask, 0) {
		pos.Set(model.KeyVin, v.next())
	}
	if s, ok := field(reportMask, 1); ok {
		pos.Set(model.KeyIgnition, kit.Int(s) > 0)
	}
	if s, ok := field(reportMask, 2); ok {
		pos.Set(model.KeyObdOdometer, kit.Int(kit.SubstringFrom(s, 1)))
	}
	if s, ok := field(reportMask, 3); ok {
		pos.Set(model.KeyFuelUsed, kit.Double(s))
	}
	if s, ok := field(reportMask, 5); ok {
		pos.Set(model.KeyRpm, kit.Int(s))
	}
	if s, ok := field(reportMask, 4); ok {
		pos.Set(model.KeyObdSpeed, kit.Int(s))
	}
	if s, ok := field(reportMask, 6); ok {
		pos.Set(model.KeyCoolantTemp, kit.Int(s))
	}
	if s, ok := field(reportMask, 7); ok {
		if strings.HasPrefix(s, "L/H") {
			pos.Set(model.KeyFuelConsumption, kit.Double(kit.SubstringFrom(s, 3)))
		}
	}
	if s, ok := field(reportMask, 8); ok {
		pos.Set(model.KeyFuel, kit.Double(kit.SubstringFrom(s, 1)))
	}
	if s, ok := field(reportMask, 9); ok {
		pos.Set("range", kit.Long(s)*100)
	}
	if s, ok := field(reportMask, 10); ok {
		pos.Set(model.KeyThrottle, kit.Int(s))
	}
	if s, ok := field(reportMask, 11); ok {
		pos.Set(model.KeyHours, units.MsFromHoursFloat(kit.Double(s)))
	}
	if s, ok := field(reportMask, 12); ok {
		pos.Set(model.KeyDrivingTime, kit.Double(s))
	}
	if s, ok := field(reportMask, 13); ok {
		pos.Set("idleHours", kit.Double(s))
	}
	if s, ok := field(reportMask, 14); ok {
		pos.Set("idleFuelConsumption", kit.Double(s))
	}
	if s, ok := field(reportMask, 15); ok {
		pos.Set(model.KeyAxleWeight, kit.Int(s))
	}
	if s, ok := field(reportMask, 16); ok {
		pos.Set("tachographInfo", kit.IntRadix(s, 16))
	}
	if s, ok := field(reportMask, 17); ok {
		pos.Set("indicators", kit.IntRadix(s, 16))
	}
	if s, ok := field(reportMask, 18); ok {
		pos.Set("lights", kit.IntRadix(s, 16))
	}
	if s, ok := field(reportMask, 19); ok {
		pos.Set("doors", kit.IntRadix(s, 16))
	}
	if s, ok := field(reportMask, 20); ok {
		pos.Set("vehicleOverspeed", kit.Double(s))
	}
	if s, ok := field(reportMask, 21); ok {
		pos.Set("engineOverspeed", kit.Double(s))
	}
	switch model_ {
	case "GV350M":
		for i := 22; i <= 24; i++ { // impulse distance, gross vehicle weight, catalyst liquid level
			if check(reportMask, i) {
				v.skip(1)
			}
		}
	case "GV355CEU":
		// impulse distance, engine cold starts, engine all starts, engine starts by
		// ignition, total engine cold running time, handbrake applies during ride,
		// electric report mask
		for i := 22; i <= 28; i++ {
			if check(reportMask, i) {
				v.skip(1)
			}
		}
	}

	var reportMaskExt int64
	if s, ok := field(reportMask, 29); ok {
		reportMaskExt = kit.LongRadix(s, 16)
	}
	if s, ok := field(reportMaskExt, 0); ok {
		pos.Set("adBlueLevel", kit.Double(kit.SubstringFrom(s, 1)))
	}
	if s, ok := field(reportMaskExt, 1); ok {
		pos.Set("axleWeight1", kit.Int(s))
	}
	if s, ok := field(reportMaskExt, 2); ok {
		pos.Set("axleWeight3", kit.Int(s))
	}
	if s, ok := field(reportMaskExt, 3); ok {
		pos.Set("axleWeight4", kit.Int(s))
	}
	for i := 4; i <= 6; i++ { // tachograph overspeed, motion, direction
		if check(reportMaskExt, i) {
			v.skip(1)
		}
	}
	if s, ok := field(reportMaskExt, 7); ok {
		pos.Set(model.PrefixAdc+"1", float64(kit.Int(s))/1000.0)
	}
	// pedal breaking factor, engine breaking factor, total accelerator kick-downs, total
	// effective engine speed, total cruise control time, total accelerator kick-down
	// time, total brake application
	for i := 8; i <= 14; i++ {
		if check(reportMaskExt, i) {
			v.skip(1)
		}
	}
	if s, ok := field(reportMaskExt, 15); ok {
		pos.Set("driver1Card", s)
	}
	if s, ok := field(reportMaskExt, 16); ok {
		pos.Set("driver2Card", s)
	}
	if s, ok := field(reportMaskExt, 17); ok {
		pos.Set("driver1Name", s)
	}
	if s, ok := field(reportMaskExt, 18); ok {
		pos.Set("driver2Name", s)
	}
	if s, ok := field(reportMaskExt, 19); ok {
		pos.Set("registration", s)
	}
	// expansion information, rapid brakings, rapid accelerations, engine torque, service
	// distance, ambient temperature, tachograph driver1/driver2 working time masks, dtc
	// codes, gaseous fuel level, tachograph information expand
	for i := 20; i <= 30; i++ {
		if check(reportMaskExt, i) {
			v.skip(1)
		}
	}

	var reportMaskCan int64
	if s, ok := field(reportMaskExt, 31); ok {
		reportMaskCan = kit.LongRadix(s, 16)
	}
	for i := 0; i <= 2; i++ { // retarder usage, power mode, tachograph timestamp
		if check(reportMaskCan, i) {
			v.skip(1)
		}
	}

	if model_ != "GV355CEU" && check(reportMask, 30) {
		for v.at(0) == "" {
			v.skip(1)
		}
		pos.Valid = kit.Int(v.next()) > 0
		if v.at(0) != "" {
			pos.Speed = units.KnotsFromKph(kit.Double(v.next()))
			pos.Course = float64(kit.Int(v.next()))
			pos.Altitude = kit.Double(v.next())
			pos.SetLongitude(kit.Double(v.next()))
			pos.SetLatitude(kit.Double(v.next()))
			pos.SetTime(parseDate(v.next()))
		} else {
			v.skip(6) // no location
			m.LastLocation(pos, nil)
		}
	} else {
		m.LastLocation(pos, nil)
	}

	d.setDeviceTime(pos, parseDate(all[len(all)-2]))

	return pos
}

func decodeStatus(pos *model.Position, value int64) {
	ignition := kit.Between(value, 2*8, 3*8)
	if kit.Check(ignition, 4) {
		pos.Set(model.KeyIgnition, false)
	} else if kit.Check(ignition, 5) {
		pos.Set(model.KeyIgnition, true)
	}
	input := kit.Between(value, 8, 2*8)
	output := kit.To(value, 8)
	pos.Set(model.KeyInput, input)
	pos.Set(model.PrefixIn+"1", kit.Check(input, 1))
	pos.Set(model.PrefixIn+"2", kit.Check(input, 2))
	pos.Set(model.KeyOutput, output)
	pos.Set(model.PrefixOut+"1", kit.Check(output, 0))
	pos.Set(model.PrefixOut+"2", kit.Check(output, 1))
}

var patternFri = kit.NewPattern().
	Text("+").Expression("(?:RESP|BUFF):GT...,").
	Expression("(?:.{6}|.{10})?,").     // protocol version
	Number("(d{15}|x{14}),").           // imei
	Expression("(?:([0-9A-Z]{17}),)?"). // vin
	Expression("[^,]*,").               // device name
	Number("(d+)?,").                   // power
	Number("(d{1,2}),").Optional().     // report type
	Number("d{1,2},").Optional().       // count
	Number("d*,").Optional().           // reserved
	Number("(d+),").Optional().         // battery
	Expression("((?:").
	Expression(patternLocation).
	Expression(")+)").
	GroupBegin().
	Number("d{1,2},").
	Number("(d{1,5})?,").     // battery
	Number("(d{1,3}),").      // battery level
	Number("[01],").          // mode
	Number("(?:[01])?,").     // motion
	Number("(-?d{1,2}.d)?,"). // temperature
	Or().
	Number("(d{1,7}.d)?,").         // odometer
	Number("(d{5}:dd:dd)?,").       // hour meter
	Number("(x+)?,").               // adc 1
	Number("(x+)?,").               // adc 2
	Number("d*,").Optional().       // reserved
	Number("(d{1,3})?,").           // battery
	Number("(x{6})?,").             // device status
	Number("(d+)?,").               // rpm
	Number("(?:d+.?d*|Inf|NaN)?,"). // fuel consumption
	Number("(d+)?,").               // fuel level
	Or().
	Number("(-?d),").    // rssi
	Number("(d{1,3}),"). // battery
	Or().
	Number("(d{1,7}.d)?,").Optional(). // odometer
	Number("(d{1,3})?,").              // battery
	GroupEnd().
	Any().
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func (d *TextDecoder) decodeFri(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternFri, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	var positions []*model.Position

	vin := parser.NextNullable()
	power := parser.NextInt()
	reportType := parser.NextInt()
	battery := parser.NextInt()

	itemParser := kit.NewParser(patternLocationCompiled, parser.Next())
	for itemParser.Find() {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Set(model.KeyVin, vin)

		d.decodeLocationParser(m, pos, itemParser)

		positions = append(positions, pos)
	}

	pos := positions[len(positions)-1] // getLast

	skipLocation(parser)

	if power != nil && *power > 10 {
		pos.Set(model.KeyPower, float64(*power)/1000.0) // only on some devices
	}
	if battery != nil {
		pos.Set(model.KeyBatteryLevel, *battery)
	}

	if parser.HasNext() {
		pos.Set(model.KeyBattery, float64(parser.MustInt())/1000.0)
	}
	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	pos.Set(model.PrefixTemp+"1", parser.NextDouble())

	if parser.HasNext() {
		pos.Set(model.KeyOdometer, parser.MustDouble()*1000)
	}
	pos.Set(model.KeyHours, parseHours(parser.NextNullable()))
	pos.Set(model.PrefixAdc+"1", parser.NextNullable())
	pos.Set(model.PrefixAdc+"2", parser.NextNullable())
	pos.Set(model.KeyBatteryLevel, parser.NextInt())

	if parser.HasNext() {
		decodeStatus(pos, parser.MustHexLong())
	}

	pos.Set(model.KeyRpm, parser.NextInt())
	pos.Set(model.KeyFuel, parser.NextInt())

	if parser.HasNextN(2) {
		if reportType != nil {
			pos.Set(model.KeyMotion, checkBit(*reportType, 0))
			pos.Set(model.KeyCharge, checkBit(*reportType, 1))
		}
		pos.Set(model.KeyRssi, parser.NextInt())
		pos.Set(model.KeyBatteryLevel, parser.NextInt())
	}
	if parser.HasNext() {
		pos.Set(model.KeyOdometer, parser.MustDouble()*1000)
	}
	pos.Set(model.KeyBatteryLevel, parser.NextInt())

	d.decodeDeviceTime(pos, parser)
	if d.IgnoreFixTime {
		positions = []*model.Position{pos}
	}

	return positions
}

func (d *TextDecoder) decodeEri(m *ingest.Message, all []string) any {
	v := &values{v: all}
	v.skip(1) // header
	protocolVersion := v.next()
	ds := m.Session(v.next())
	if ds == nil {
		return nil
	}

	model_ := deviceModel(ds, protocolVersion)
	v.skip(1) // device name
	mask := kit.LongRadix(v.next(), 16)
	var power *float64
	if s := v.next(); s != "" {
		p := float64(kit.Int(s)) / 1000.0
		power = &p
	}
	v.skip(1) // report type

	count := kit.Int(v.next())
	var positions []*model.Position
	for range count {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		decodeLocationValues(m, pos, model_, v)
		positions = append(positions, pos)
	}

	if len(positions) == 0 {
		kit.Throw("NoSuchElementException")
	}
	pos := positions[len(positions)-1]
	pos.Set(model.KeyPower, power)

	intOrNil := func(s string, radix int) *int {
		if s == "" {
			return nil
		}
		n := kit.IntRadix(s, radix)
		return &n
	}

	gl5 := strings.HasPrefix(model_, "GL5")
	if !gl5 {
		if s := v.next(); s != "" {
			pos.Set(model.KeyOdometer, kit.Double(s)*1000)
		}
	}
	if !gl5 && model_ != "GL320M" {
		pos.Set(model.KeyHours, parseHours(str(v.next())))
		if s := v.next(); s != "" {
			decodeAnalog(pos, 1, s)
		}
	}
	if strings.HasPrefix(model_, "GV") && !strings.HasPrefix(model_, "GV6") && model_ != "GV350M" {
		if s := v.next(); s != "" {
			decodeAnalog(pos, 2, s)
		}
	}
	if model_ == "GV200" || model_ == "GV310LAU" {
		if s := v.next(); s != "" {
			decodeAnalog(pos, 3, s)
		}
	}
	if strings.HasPrefix(model_, "GV3") && strings.HasSuffix(model_, "CEU") || strings.HasPrefix(model_, "GV600M") {
		v.skip(1) // reserved
	}

	switch {
	case gl5:
		pos.Set(model.KeyBatteryLevel, intOrNil(v.next(), 10))
		v.skip(1) // mode selection
		if s := v.next(); s != "" {
			pos.Set(model.KeyMotion, kit.Int(s) > 0)
		}
	case model_ == "GV200":
		pos.Set(model.KeyInput, intOrNil(v.next(), 16))
		pos.Set(model.KeyOutput, intOrNil(v.next(), 16))
		v.skip(1) // uart device type
	case model_ == "GL320M":
		pos.Set(model.KeyBatteryLevel, intOrNil(v.next(), 10))
		if kit.Check(mask, 7) {
			pos.Set("externalBattery", intOrNil(v.next(), 10))
		}
	default:
		pos.Set(model.KeyBatteryLevel, intOrNil(v.next(), 10))
		if s := v.next(); s != "" {
			decodeStatus(pos, kit.LongRadix(s, 16))
		}
		v.skip(1) // reserved / uart device type
	}

	t := parseDate(all[len(all)-2])
	if d.IgnoreFixTime {
		pos.SetTime(t)
		positions = []*model.Position{pos}
	} else {
		pos.DeviceTime = model.NewTime(t)
	}

	if kit.Check(mask, 0) && model_ != "GV350M" {
		pos.Set(model.KeyFuel, intOrNil(v.next(), 16))
	}

	if kit.Check(mask, 1) {
		deviceCount := kit.Int(v.next())
		for i := 1; i <= deviceCount; i++ {
			v.skip(1) // id
			v.skip(1) // type
			if s := v.next(); s != "" {
				pos.Set(model.PrefixTemp+strconv.Itoa(i), float64(int16(kit.IntRadix(s, 16)))*0.0625)
			}
		}
	}

	if kit.Check(mask, 2) {
		return positions // can data not supported
	}

	if (kit.Check(mask, 3) || kit.Check(mask, 4) || (kit.Check(mask, 0) && model_ == "GV350M")) &&
		v.i < len(all)-2 {
		deviceCount := kit.Int(v.next())
		for i := 1; i <= deviceCount; i++ {
			v.skip(1) // type
			if model_ == "GV350M" {
				v.skip(1) // uart id
				if kit.Check(mask, 0) {
					pos.Set(model.KeyFuel, kit.IntRadix(v.next(), 16))
				}
			}
			if kit.Check(mask, 3) {
				pos.Set(model.KeyFuel, kit.Double(v.next()))
			}
			if kit.Check(mask, 4) {
				v.skip(1) // volume
			}
		}
	}

	if kit.Check(mask, 7) && model_ != "GL320M" {
		deviceCount := kit.Int(v.next())
		for i := 1; i <= deviceCount; i++ {
			v.skip(1) // serial number
			typ := kit.Int(v.next())
			v.skip(1) // temperature
			if typ == 2 {
				v.skip(1) // humidity
			}
		}
	}

	if kit.Check(mask, 8) && model_ != "GL320M" {
		deviceCount := kit.Int(v.next())
		for i := 1; i <= deviceCount; i++ {
			tag := "tag" + strconv.Itoa(i)
			v.skip(1) // index
			v.skip(1) // type
			v.skip(1) // model
			if strings.HasPrefix(model_, "GV600M") {
				v.skip(1) // raw data length
			}
			v.skip(1) // raw data
			deviceMask := kit.IntRadix(v.next(), 16)
			if checkBit(deviceMask, 0) {
				v.skip(1) // name
			}
			if checkBit(deviceMask, 1) {
				pos.Set(tag+"Id", v.next())
			}
			if checkBit(deviceMask, 2) {
				v.skip(1) // status
			}
			if checkBit(deviceMask, 3) {
				v.skip(1) // battery level
			}
			if checkBit(deviceMask, 4) {
				if s := v.next(); s != "" {
					pos.Set(tag+"Temp", kit.Double(s))
				}
			}
			if checkBit(deviceMask, 5) {
				if s := v.next(); s != "" {
					pos.Set(tag+"Humidity", kit.Int(s))
				}
			}
			// input / output, event notification, tire pressure, timestamp, enhanced
			// temperature, magnet
			for b := 7; b <= 12; b++ {
				if checkBit(deviceMask, b) {
					v.skip(1)
				}
			}
			if checkBit(deviceMask, 13) {
				if s := v.next(); s != "" {
					pos.Set(tag+"Battery", kit.Int(s))
				}
			}
			if checkBit(deviceMask, 14) {
				v.skip(1) // relay
			}
		}
	}

	return positions
}

func (d *TextDecoder) decodeIgn(m *ingest.Message, all []string, typ string) any {
	v := &values{v: all}
	v.skip(1) // header
	protocolVersion := v.next()
	ds := m.Session(v.next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	model_ := deviceModel(ds, protocolVersion)
	v.skip(1) // device name
	if model_ == "CV200" {
		v.skip(1) // reserved
		v.skip(1) // report type
	}
	v.skip(1) // duration of ignition on/off

	decodeLocationValues(m, pos, model_, v)

	pos.Set(model.KeyIgnition, strings.Contains(typ, "GN"))
	pos.Set(model.KeyHours, parseHours(str(v.next())))
	if s := v.next(); s != "" {
		pos.Set(model.KeyOdometer, kit.Double(s)*1000)
	}

	d.setDeviceTime(pos, parseDate(all[len(all)-2]))

	return pos
}

var patternLsw = kit.NewPattern().
	Text("+RESP:").Expression("GT[LT]SW,").
	Expression("(?:.{6}|.{10})?,"). // protocol version
	Number("(d{15}|x{14}),").       // imei
	Expression("[^,]*,").           // device name
	Number("[01],").                // type
	Number("([01]),").              // state
	Expression(patternLocation).
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func (d *TextDecoder) decodeLsw(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternLsw, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	index := "2"
	if strings.Contains(sentence, "LSW") {
		index = "1"
	}
	pos.Set(model.PrefixIn+index, parser.MustInt() == 1)

	d.decodeLocationParser(m, pos, parser)

	d.decodeDeviceTime(pos, parser)

	return pos
}

var patternIda = kit.NewPattern().
	Text("+RESP:GTIDA,").
	Expression("(?:.{6}|.{10})?,"). // protocol version
	Number("(d{15}|x{14}),").       // imei
	Expression("[^,]*,,").          // device name
	Number("([^,]+),").             // rfid
	Expression("[01],").            // report type
	Number("1,").                   // count
	Expression(patternLocation).
	Number("(d+.d),"). // odometer
	Text(",,,,").
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func (d *TextDecoder) decodeIda(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternIda, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	pos.Set(model.KeyDriverUniqueID, parser.NextNullable())

	d.decodeLocationParser(m, pos, parser)

	pos.Set(model.KeyOdometer, parser.MustDouble()*1000)

	d.decodeDeviceTime(pos, parser)

	return pos
}

var patternWif = kit.NewPattern().
	Text("+RESP:GTWIF,").
	Expression("(?:.{6}|.{10})?,").      // protocol version
	Number("(d{15}|x{14}),").            // imei
	Expression("[^,]*,").                // device name
	Number("(d+),").                     // count
	Number("((?:x{12},-?d+,,,,)+),,,,"). // wifi
	Number("(d{1,3}),").                 // battery
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

var patternWifi = regexp.MustCompile(`([0-9a-fA-F]{12}),(-?\d+),,,,`)

func decodeWif(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternWif, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	m.LastLocation(pos, nil)

	network := model.NewNetwork()

	parser.NextInt() // count
	for _, match := range patternWifi.FindAllStringSubmatch(parser.Next(), -1) {
		network.AddWifiAccessPoint(model.NewWifiAccessPoint(macAddress(match[1]), kit.Int(match[2])))
	}

	pos.Network = network

	pos.Set(model.KeyBatteryLevel, parser.NextInt())

	return pos
}

var patternGsm = kit.NewPattern().
	Text("+RESP:GTGSM,").
	Expression("(?:.{6}|.{10})?,").      // protocol version
	Number("(d{15}|x{14}),").            // imei
	Expression("(?:STR|CTN|NMR|RTL),").  // fix type
	Expression("(.*)").                  // cells
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func decodeGsm(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternGsm, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	m.LastLocation(pos, nil)

	network := model.NewNetwork()

	data := kit.Split(parser.Next(), ",")
	for i := range 6 {
		if data[i*6] != "" {
			network.AddCellTower(model.NewCellTowerRSSI(
				kit.Int(data[i*6]), kit.Int(data[i*6+1]),
				kit.IntRadix(data[i*6+2], 16), int64(kit.IntRadix(data[i*6+3], 16)),
				kit.Int(data[i*6+4])))
		}
	}

	pos.Network = network

	return pos
}

var patternPna = kit.NewPattern().
	Text("+RESP:GT").Expression("P[NF]A,").
	Expression("(?:.{6}|.{10})?,").      // protocol version
	Number("(d{15}|x{14}),").            // imei
	Expression("[^,]*,").                // device name
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func decodePna(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternPna, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	m.LastLocation(pos, nil)

	if strings.Contains(sentence, "PNA") {
		pos.AddAlarm(model.AlarmPowerOn)
	} else {
		pos.AddAlarm(model.AlarmPowerOff)
	}

	return pos
}

var patternDar = kit.NewPattern().
	Text("+RESP:GTDAR,").
	Expression("(?:.{6}|.{10})?,"). // protocol version
	Number("(d{15}|x{14}),").       // imei
	Expression("[^,]*,").           // device name
	Number("(d),").                 // warning type
	Number("(d{1,2}),,,").          // fatigue degree
	Expression(patternLocation).
	Any().
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func (d *TextDecoder) decodeDar(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternDar, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	warningType := parser.MustInt()
	fatigueDegree := parser.MustInt()
	if warningType == 1 {
		pos.AddAlarm(model.AlarmFatigueDriving)
		pos.Set("fatigueDegree", fatigueDegree)
	} else {
		pos.Set("warningType", warningType)
	}

	d.decodeLocationParser(m, pos, parser)

	d.decodeDeviceTime(pos, parser)

	return pos
}

var patternDtt = kit.NewPattern().
	Text("+RESP:GTDTT,").
	Expression("(?:.{6}|.{10})?,").      // protocol version
	Number("(d{15}|x{14}),").            // imei
	Expression("[^,]*,,,").              // device name
	Number("d,").                        // data type
	Number("d+,").                       // data length
	Number("(x+),").                     // data
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func (d *TextDecoder) decodeDtt(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternDtt, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	m.LastLocation(pos, nil)

	data := kit.Latin1(kit.ParseHex(parser.Next()))
	if strings.Contains(data, "COMB") {
		pos.Set(model.KeyFuel, kit.Double(kit.Split(data, ",")[2]))
	} else {
		pos.Set(model.KeyResult, data)
	}

	d.decodeDeviceTime(pos, parser)

	return pos
}

var patternBaa = kit.NewPattern().
	Text("+RESP:GTBAA,").
	Expression("(?:.{6}|.{10})?,").  // protocol version
	Number("(d{15}|x{14}),").        // imei
	Expression("[^,]*,").            // device name
	Number("x+,").                   // index
	Number("d,").                    // accessory type
	Number("d,").                    // accessory model
	Number("x+,").                   // alarm type
	Number("(x{4}),").               // append mask
	Expression("((?:[^,]+,){0,6})"). // accessory optionals
	Expression(patternLocation).
	Any().
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func (d *TextDecoder) decodeBaa(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternBaa, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	mask := parser.MustHexInt()
	v := &values{v: kit.Split(parser.Next(), ",")}
	if checkBit(mask, 0) {
		pos.Set("accessoryName", v.next())
	}
	if checkBit(mask, 1) {
		pos.Set("accessoryMac", v.next())
	}
	if checkBit(mask, 2) {
		pos.Set("accessoryStatus", kit.Int(v.next()))
	}
	if checkBit(mask, 3) {
		pos.Set("accessoryVoltage", float64(kit.Int(v.next()))/1000.0)
	}
	if checkBit(mask, 4) {
		pos.Set("accessoryTemp", kit.Int(v.next()))
	}
	if checkBit(mask, 5) {
		pos.Set("accessoryHumidity", kit.Int(v.at(0)))
	}

	d.decodeLocationParser(m, pos, parser)

	d.decodeDeviceTime(pos, parser)

	return pos
}

var patternBid = kit.NewPattern().
	Text("+RESP:GTBID,").
	Expression("(?:.{6}|.{10})?,").  // protocol version
	Number("(d{15}|x{14}),").        // imei
	Expression("[^,]*,").            // device name
	Number("d,").                    // count
	Number("d,").                    // accessory model
	Number("(x{4}),").               // append mask
	Expression("((?:[^,]+,){0,2})"). // accessory optionals
	Expression(patternLocation).
	Any().
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func (d *TextDecoder) decodeBid(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternBid, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	mask := parser.MustHexInt()
	v := &values{v: kit.Split(parser.Next(), ",")}
	if checkBit(mask, 1) {
		pos.Set("accessoryMac", v.next())
	}
	if checkBit(mask, 3) {
		pos.Set("accessoryVoltage", float64(kit.Int(v.at(0)))/1000.0)
	}

	d.decodeLocationParser(m, pos, parser)

	d.decodeDeviceTime(pos, parser)

	return pos
}

var patternLsa = kit.NewPattern().
	Text("+RESP:GTLSA,").
	Expression("(?:.{6}|.{10})?,"). // protocol version
	Number("(d{15}|x{14}),").       // imei
	Expression("[^,]*,").           // device name
	Number("d,").                   // event state 1
	Number("d,").                   // event state 2
	Number("d+,").                  // number
	Expression(patternLocation).
	Number("d+,").                       // bit error rate
	Number("(d),").                      // light level
	Number("(d+),").                     // battery level
	Number("[01],").                     // mode selection
	Number("[01]?,").                    // movement status
	Number("(-?d+.d)?,").                // temperature
	Number("(dddd)(dd)(dd)").            // date (yyyymmdd)
	Number("(dd)(dd)(dd)").OptionalN(2). // time (hhmmss)
	Text(",").
	Number("(xxxx)"). // count number
	Text("$").Optional().
	Compile()

func (d *TextDecoder) decodeLsa(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternLsa, sentence)
	pos := initPosition(m, parser)
	if pos == nil {
		return nil
	}

	d.decodeLocationParser(m, pos, parser)

	pos.Set("lightLevel", parser.NextInt())
	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	pos.Set(model.PrefixTemp+"1", parser.NextDouble())

	d.decodeDeviceTime(pos, parser)

	return pos
}

func decodeLbs(m *ingest.Message, all []string) any {
	v := &values{v: all}
	v.skip(1) // header
	v.skip(1) // protocol version
	ds := m.Session(v.next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	v.skip(1) // device name
	v.skip(1) // trigger type
	v.skip(1) // report type
	pos.Set(model.KeyBatteryLevel, kit.Int(v.next()))
	v.skip(1) // reserved
	v.skip(1) // motion status
	v.skip(1) // reserved
	v.skip(1) // reserved
	v.skip(1) // component expansion mask

	network := model.NewNetwork()
	for range 7 {
		if v.at(1) != "" {
			mcc := kit.Int(v.next())
			mnc := kit.Int(v.next())
			lac := kit.IntRadix(v.next(), 16)
			cid := kit.IntRadix(v.next(), 16)
			rssi := kit.Int(v.next())
			network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
		} else {
			v.skip(5) // empty
		}
		v.skip(1) // reserved
	}
	pos.Network = network

	t := parseDate(all[len(all)-2])
	m.LastLocation(pos, &t)

	return pos
}

func (d *TextDecoder) decodeDat(m *ingest.Message, all []string) any {
	v := &values{v: all}
	v.skip(1) // header

	protocolVersion := v.next()
	ds := m.Session(v.next())
	if ds == nil {
		return nil
	}

	model_ := deviceModel(ds, protocolVersion)

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	v.skip(1) // device name
	v.skip(1) // report type
	v.skip(1) // reserved
	v.skip(1) // reserved

	pos.Set("data", v.next())

	decodeLocationValues(m, pos, model_, v)

	d.setDeviceTime(pos, parseDate(all[len(all)-2]))

	return pos
}

var (
	reHex12      = regexp.MustCompile(`^[0-9A-Fa-f]{1,2}$`)
	reCoordinate = regexp.MustCompile(`^-?\d{1,3}\.\d{6}$`)
	reDigits4    = regexp.MustCompile(`^\d{4}$`)
	reHex4       = regexp.MustCompile(`^[0-9A-Fa-f]{4}$`)
	reHex48      = regexp.MustCompile(`^[0-9A-Fa-f]{4,8}$`)
	reDigits13   = regexp.MustCompile(`^\d{1,3}$`)
)

func (d *TextDecoder) decodeBasic(m *ingest.Message, v []string, typ string) any {
	index := 0
	index++ // header
	index++ // protocol version

	ds := m.Session(v[index])
	index++
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if reHex12.MatchString(v[index+2]) {
		reportType := kit.IntRadix(v[index+2], 16)
		switch typ {
		case "NMR":
			pos.Set(model.KeyMotion, reportType == 1)
		case "DIS":
			pos.Set(model.PrefixIn+strconv.Itoa(reportType/0x10), reportType%0x10 == 1)
		case "IGL":
			pos.Set(model.KeyIgnition, reportType%0x10 == 1)
		case "HBM":
			switch reportType % 0x10 {
			case 0, 3:
				pos.AddAlarm(model.AlarmBraking)
			case 1, 4:
				pos.AddAlarm(model.AlarmAcceleration)
			case 2:
				pos.AddAlarm(model.AlarmCornering)
			}
		}
	}

	for index+2 < len(v) {
		if reCoordinate.MatchString(v[index]) && reCoordinate.MatchString(v[index+1]) {
			c := &values{v: v, i: index - 4}
			pos.Valid = true
			pos.Set(model.KeyHdop, kit.Int(c.next()))
			pos.Speed = units.KnotsFromKph(kit.Double(c.next()))
			pos.Course = float64(kit.Int(c.next()))
			pos.Altitude = kit.Double(c.next())
			pos.SetLongitude(kit.Double(c.next()))
			pos.SetLatitude(kit.Double(c.next()))
			pos.SetTime(parseDate(c.next()))
			index = c.i
			break
		}
		index++
	}
	if !pos.Attributes.Has(model.KeyHdop) {
		m.LastLocation(pos, nil)
		index = 2
	}

	for index+3 < len(v) {
		if reDigits4.MatchString(v[index]) && reDigits4.MatchString(v[index+1]) &&
			reHex4.MatchString(v[index+2]) && reHex48.MatchString(v[index+3]) {
			pos.Network = model.NetworkOf(model.NewCellTower(
				kit.Int(v[index]), kit.Int(v[index+1]),
				kit.IntRadix(v[index+2], 16), kit.LongRadix(v[index+3], 16)))
			break
		}
		index++
	}

	index = len(v) - 2
	if kit.Len(v[index]) == 14 {
		d.setDeviceTime(pos, parseDate(v[index]))

		if pos.Attributes.Has(model.KeyHdop) && reDigits13.MatchString(v[index-1]) {
			pos.Set(model.KeyBatteryLevel, kit.Int(v[index-1]))
		}
	}

	switch typ {
	case "SOS":
		pos.AddAlarm(model.AlarmSos)
	case "SPD":
		pos.AddAlarm(model.AlarmOverspeed)
	case "TOW":
		pos.AddAlarm(model.AlarmTow)
	case "IDL":
		pos.AddAlarm(model.AlarmIdle)
	case "PNA":
		pos.AddAlarm(model.AlarmPowerOn)
	case "PFA":
		pos.AddAlarm(model.AlarmPowerOff)
	case "EPN", "MPN":
		pos.AddAlarm(model.AlarmPowerRestored)
	case "EPF", "MPF":
		pos.AddAlarm(model.AlarmPowerCut)
	case "BPL":
		pos.AddAlarm(model.AlarmLowBattery)
	case "STT":
		pos.AddAlarm(model.AlarmMovement)
	case "SWG":
		pos.AddAlarm(model.AlarmGeofence)
	case "TMP", "TEM":
		pos.AddAlarm(model.AlarmTemperature)
	case "JDR", "JDS":
		pos.AddAlarm(model.AlarmJamming)
	}

	if pos.Attributes.Len() > 0 || pos.Network != nil {
		return pos
	}
	return nil
}

// stripDollar is replaceAll("\\$$", ""): a '$' at the end, or before a final line
// terminator, is removed.
func stripDollar(s string) string {
	for _, end := range []string{"", "\r\n", "\n", "\r", "\u0085", " ", " "} {
		if strings.HasSuffix(s, "$"+end) {
			return s[:len(s)-len(end)-1] + end
		}
	}
	return s
}

// Decode is Gl200TextProtocolDecoder.decode.
func (d *TextDecoder) Decode(m *ingest.Message, msg any) any {
	sentence := stripDollar(kit.Latin1(msg.(*kit.Buf).Bytes()))

	typeIndex := kit.IndexOf(sentence, ":GT")
	if typeIndex < 0 {
		return nil
	}

	v := kit.Split(sentence, ",")

	var result any
	typ := kit.Substring(sentence, typeIndex+3, typeIndex+6)
	if strings.HasPrefix(sentence, "+ACK") {
		result = d.decodeAck(m, v)
	} else {
		switch typ {
		case "INF":
			result = d.decodeInf(m, v)
		case "OBD":
			result = d.decodeObd(m, sentence)
		case "CAN":
			result = d.decodeCan(m, v)
		case "CTN", "FRI", "GEO", "RTL", "DOG", "STR":
			result = d.decodeFri(m, sentence)
		case "ERI":
			result = d.decodeEri(m, v)
		case "IGN", "IGF", "VGN", "VGF":
			result = d.decodeIgn(m, v, typ)
		case "LSW", "TSW":
			result = d.decodeLsw(m, sentence)
		case "IDA":
			result = d.decodeIda(m, sentence)
		case "WIF":
			result = decodeWif(m, sentence)
		case "GSM":
			result = decodeGsm(m, sentence)
		case "VER":
			result = decodeVer(m, sentence)
		case "PNA", "PFA":
			result = decodePna(m, sentence)
		case "DAR":
			result = d.decodeDar(m, sentence)
		case "DTT":
			result = d.decodeDtt(m, sentence)
		case "BAA":
			result = d.decodeBaa(m, sentence)
		case "BID":
			result = d.decodeBid(m, sentence)
		case "LSA":
			result = d.decodeLsa(m, sentence)
		case "LBS":
			result = decodeLbs(m, v)
		case "DAT":
			result = d.decodeDat(m, v)
		}

		if isNil(result) {
			result = d.decodeBasic(m, v, typ)
		}

		switch r := result.(type) {
		case *model.Position:
			if r != nil {
				r.Set(model.KeyType, typ)
			}
		case []*model.Position:
			for _, p := range r {
				p.Set(model.KeyType, typ)
			}
		}
	}

	if m.HasChannel() && d.Ack {
		m.Send("+SACK:" + v[len(v)-1] + "$")
	}

	if isNil(result) {
		return nil
	}
	return result
}

// isNil reports Java's null: nil, or a nil position (a nil list is a non-null empty list
// only where the decoder made one, which these decoders never leave nil).
func isNil(v any) bool {
	switch r := v.(type) {
	case nil:
		return true
	case *model.Position:
		return r == nil
	}
	return false
}
