// Package h02 is the H02 protocol (H02Protocol): "*HQ,...#" text messages and '$' binary
// reports, over TCP and UDP.
package h02

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: a TCP server (with h02.messageLength fixing the
// binary message length) and a UDP server.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	messageLength, err := cfg.Int("h02.messageLength", 0)
	if err != nil {
		return ingest.Protocol{}, err
	}
	return ingest.Protocol{
		Name:         "h02",
		DataCommands: protocol.DataCommands("h02"),
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  Framer(messageLength),
					TextOut: true,
					Decoder: ingest.DecoderFunc(decode),
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					TextOut: true,
					Decoder: ingest.DecoderFunc(decode),
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
		},
	}, nil
}

const (
	messageShort = 32
	messageLong  = 45
)

// Framer is H02FrameDecoder: text messages end with '#', binary ones ('$') have a fixed
// length, guessed from the first message when messageLength is 0.
func Framer(messageLength int) ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		marker := buf.GetByte(buf.ReaderIndex())
		for marker != '*' && marker != '$' && marker != 'X' && buf.ReadableBytes() > 0 {
			buf.Skip(1)
			if buf.ReadableBytes() > 0 {
				marker = buf.GetByte(buf.ReaderIndex())
			}
		}
		switch marker {
		case '*':
			// Return text message
			index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '#')
			if index != -1 {
				result := buf.ReadSlice(index + 1 - buf.ReaderIndex())
				for buf.IsReadable() &&
					(buf.GetByte(buf.ReaderIndex()) == '\r' || buf.GetByte(buf.ReaderIndex()) == '\n') {
					buf.ReadSignedByte() // skip new line
				}
				return result
			}
		case '$':
			if messageLength == 0 {
				if buf.ReadableBytes() == messageLong {
					messageLength = messageLong
				} else {
					messageLength = messageShort
				}
			}
			if buf.ReadableBytes() >= messageLength {
				return buf.ReadSlice(messageLength)
			}
		case 'X':
			if buf.ReadableBytes() >= messageShort {
				return buf.ReadSlice(messageShort)
			}
		}
		return nil
	}}
}

func readCoordinate(buf *kit.Buf, lon bool) float64 {
	degrees := kit.ReadBCD(buf, 2)
	if lon {
		degrees = degrees*10 + buf.GetUnsignedByte(buf.ReaderIndex())>>4
	}
	result := 0.0
	if lon {
		result = float64(buf.ReadUnsignedByte() & 0x0f)
	}
	length := 6
	if lon {
		length = 5
	}
	result = result*10 + float64(kit.ReadBCD(buf, length))/10000.0
	result /= 60
	result += float64(degrees)
	return result
}

func processStatus(pos *model.Position, status int64) {
	switch {
	case !kit.Check(status, 0):
		pos.AddAlarm(model.AlarmVibration)
	case !kit.Check(status, 1) || !kit.Check(status, 18):
		pos.AddAlarm(model.AlarmSos)
	case !kit.Check(status, 2):
		pos.AddAlarm(model.AlarmOverspeed)
	case !kit.Check(status, 19):
		pos.AddAlarm(model.AlarmPowerCut)
	}
	pos.Set(model.KeyIgnition, kit.Check(status, 10))
	pos.Set(model.KeyStatus, status)
}

func decodeBattery(value int) *int {
	var r int
	switch {
	case value == 0:
		return nil
	case value <= 3:
		r = (value - 1) * 10
	case value <= 6:
		r = (value - 1) * 20
	case value <= 100:
		r = value
	case value >= 0xF1 && value <= 0xF6:
		r = value - 0xF0
	default:
		return nil
	}
	return &r
}

// ack is AttributeUtil.lookup(PROTOCOL_ACK): <protocol>.ack, false by default.
func ack(m *ingest.Message, deviceID int64) bool {
	v := m.LookupBool(deviceID, m.Protocol()+".ack", false, true)
	return v != nil && *v
}

func sendResponse(m *ingest.Message, id, t string) {
	if !m.HasChannel() {
		return
	}
	now := m.Now.UTC()
	if t == "R12" {
		m.Send("*HQ," + id + "," + t + "," + now.Format("150405") + "#")
	} else {
		m.Send("*HQ," + id + ",V4," + t + "," + now.Format("20060102150405") + "#")
	}
}

func decodeBinary(m *ingest.Message, buf *kit.Buf) any {
	pos := m.NewPosition()

	longID := buf.ReadableBytes() == 42

	buf.ReadSignedByte() // marker

	var id string
	if longID {
		id = buf.ReadSlice(8).HexDump()[:15]
	} else {
		id = buf.ReadSlice(5).HexDump()
	}

	ds := m.Session(id)
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	b := kit.NewDateBuilder(nil)
	b.SetHour(kit.ReadBCD(buf, 2))
	b.SetMinute(kit.ReadBCD(buf, 2))
	b.SetSecond(kit.ReadBCD(buf, 2))
	b.SetDay(kit.ReadBCD(buf, 2))
	b.SetMonth(kit.ReadBCD(buf, 2))
	b.SetYear(kit.ReadBCD(buf, 2))
	pos.SetTime(b.Date())

	latitude := readCoordinate(buf, false)
	pos.Set(model.KeyBatteryLevel, decodeBattery(buf.ReadUnsignedByte()))
	longitude := readCoordinate(buf, true)

	flags := buf.ReadUnsignedByte() & 0x0f
	pos.Valid = flags&0x02 != 0
	if flags&0x04 == 0 {
		latitude = -latitude
	}
	if flags&0x08 == 0 {
		longitude = -longitude
	}
	pos.SetLatitude(latitude)
	pos.SetLongitude(longitude)

	pos.Speed = float64(kit.ReadBCD(buf, 3))
	pos.Course = float64(buf.ReadUnsignedByte()&0x0f)*100.0 + float64(kit.ReadBCD(buf, 2))

	processStatus(pos, buf.ReadUnsignedInt())

	if ack(m, ds.DeviceID) {
		sendResponse(m, id, "R12")
	}
	return pos
}

var pattern = kit.NewPattern().
	Text("*").
	Expression("..,"). // manufacturer
	Number("(d+)?,").  // imei
	GroupBegin().
	Text("V4,").
	Expression("(.*),"). // response
	Or().
	Expression("(V[^,]*),").
	GroupEnd().
	Number("(?:(dd)(dd)(dd))?,"). // time (hhmmss)
	GroupBegin().
	Expression("([ABV])?,"). // validity
	Or().
	Number("(d+),"). // coding scheme
	GroupEnd().
	GroupBegin().
	Number("-(d+)-(d+.d+),([NS]),"). // latitude
	Or().
	Number("(d*)(dd.d+),([NS]),"). // latitude
	Or().
	Number("(d+)(dd)(d{4}),([NS]),"). // latitude
	GroupEnd().
	GroupBegin().
	Number("-(d+)-(d+.d+),([EW]),"). // longitude
	Or().
	Number("(d*)(dd.d+),([EW]),"). // longitude
	Or().
	Number("(d+)(dd)(d{4}),([EW]),"). // longitude
	GroupEnd().
	Number(" *(d+.?d*),").       // speed
	Number("(d+.?d*)?,").        // course
	Number("(?:d+,)?").          // battery
	Number("(?:(dd)(dd)(dd))?"). // date (ddmmyy)
	GroupBegin().
	Expression(",[^,]*,").
	Expression("[^,]*,").
	Expression("[^,]*"). // sim info
	GroupEndWith("?").
	GroupBegin().
	Number(",(x{8})").
	GroupBegin().
	Number(",(d+),").   // odometer
	Number("(-?d+),").  // temperature
	Number("(d+.d+),"). // fuel
	Number("(-?d+),").  // altitude
	Number("(x+),").    // lac
	Number("(x+)").     // cid
	Or().
	Text(",").
	Expression("(.*)"). // data
	Or().
	GroupEnd().
	Or().
	GroupEnd().
	Text("#").
	Compile()

var patternNbr = kit.NewPattern().
	Text("*").
	Expression("..,").            // manufacturer
	Number("(d+),").              // imei
	Text("NBR,").                 //
	Number("(dd)(dd)(dd),").      // time (hhmmss)
	Number("(d+),").              // mcc
	Number("(d+),").              // mnc
	Number("d+,").                // gsm delay time
	Number("d+,").                // count
	Number("((?:d+,d+,-?d+,)+)"). // cells
	Number("(dd)(dd)(dd),").      // date (ddmmyy)
	Number("(x{8})").             // status
	Any().
	Compile()

var patternLink = kit.NewPattern().
	Text("*").
	Expression("..,").       // manufacturer
	Number("(d+),").         // imei
	Text("LINK,").           //
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(d+),").         // rssi
	Number("(d+),").         // satellites
	Number("(d+),").         // battery
	Number("(d+),").         // steps
	Number("(d+),").         // turnovers
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(x{8})").        // status
	Any().
	Compile()

var patternV3 = kit.NewPattern().
	Text("*").
	Expression("..,").       // manufacturer
	Number("(d+),").         // imei
	Text("V3,").             //
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(ddd)").         // mcc
	Number("(d+),").         // mnc
	Number("(d+),").         // count
	Expression("(.*),").     // cell info
	Number("(x{4}),").       // battery
	Number("d+,").           // reboot info
	Text("X,").              //
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(x{8})").        // status
	Text("#").Optional().
	Compile()

var patternVp1 = kit.NewPattern().
	Text("*hq,").
	Number("(d{15}),"). // imei
	Text("VP1,").
	GroupBegin().
	Text("V,").
	Number("(d+),").       // mcc
	Number("(d+),").       // mnc
	Expression("([^#]+)"). // cells
	Or().
	Expression("[AB],").    // validity
	Number("(d+)(dd.d+),"). // latitude
	Expression("([NS]),").  //
	Number("(d+)(dd.d+),"). // longitude
	Expression("([EW]),").  //
	Number("(d+.d+),").     // speed
	Number("(d+.d+),").     // course
	Number("(dd)(dd)(dd)"). // date (ddmmyy)
	GroupEnd().
	Any().
	Compile()

var patternHtbt = kit.NewPattern().
	Text("*HQ,").
	Number("(d{15}),"). // imei
	Text("HTBT,").
	Number("(d+)"). // battery
	Any().
	Compile()

var patternSms = kit.NewPattern().
	Text("*HQ,").
	Number("(d+),"). // id
	Text("SMS,").
	Expression("(.+)").
	Text("#").
	Compile()

func decodeText(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	id := parser.NextNullable()
	idText := "" // Java passes the null id along as one unique id
	if id != nil {
		idText = *id
	}
	ds := m.Session(idText)
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if parser.HasNext() {
		pos.Set(model.KeyResult, parser.Next())
	}

	if parser.HasNext() && parser.Next() == "V1" {
		if id != nil {
			sendResponse(m, *id, "V1")
		}
	} else if ack(m, ds.DeviceID) {
		if id != nil {
			sendResponse(m, *id, "R12")
		}
	}

	b := kit.NewDateBuilder(nil)
	if parser.HasNextN(3) {
		b.SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	}

	if parser.HasNext() {
		pos.Valid = parser.Next() == "A"
	}
	if parser.HasNext() {
		parser.NextInt() // coding scheme
		pos.Valid = true
	}

	if parser.HasNextN(3) {
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	}
	if parser.HasNextAny(3) {
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	}
	if parser.HasNextN(4) {
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinMinHem))
	}

	if parser.HasNextN(3) {
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	}
	if parser.HasNextAny(3) {
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	}
	if parser.HasNextN(4) {
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinMinHem))
	}

	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	if parser.HasNextN(3) {
		b.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
		pos.SetTime(b.Date())
	} else {
		pos.SetTime(m.Now)
	}

	if parser.HasNext() {
		processStatus(pos, parser.NextLongRadix(16, 0))
	}

	if parser.HasNextN(6) {
		pos.Set(model.KeyOdometer, parser.NextIntDefault(0))
		pos.Set(model.PrefixTemp+"1", parser.NextIntDefault(0))
		pos.Set(model.KeyFuel, parser.NextDoubleDefault(0))

		pos.Altitude = float64(parser.NextIntDefault(0))

		mcc, err := m.Config().Int("geolocation.mcc", 0)
		if err != nil {
			panic(err)
		}
		mnc, err := m.Config().Int("geolocation.mnc", 0)
		if err != nil {
			panic(err)
		}
		lac := parser.NextHexIntDefault(0)
		cid := parser.NextHexIntDefault(0)
		pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))
	}

	if parser.HasNext() {
		for i, value := range kit.Split(parser.Next(), ",") {
			pos.Set(model.PrefixIo+strconv.Itoa(i+1), kit.Trim(value))
		}
	}
	return pos
}

func decodeLbs(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternNbr, sentence)
	if !parser.Matches() {
		return nil
	}

	id := parser.Next()
	ds := m.Session(id)
	if ds == nil {
		return nil
	}

	sendResponse(m, id, "NBR")

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	b := kit.NewDateBuilder(nil)
	b.SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	network := model.NewNetwork()
	mcc := parser.NextIntDefault(0)
	mnc := parser.NextIntDefault(0)

	cells := kit.Split(parser.Next(), ",")
	for i := 0; i < len(cells)/3; i++ {
		network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, kit.Int(cells[i*3]),
			int64(kit.Int(cells[i*3+1])), kit.Int(cells[i*3+2])))
	}

	pos.Network = network

	b.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	date := b.Date()
	m.LastLocation(pos, &date)

	processStatus(pos, parser.NextLongRadix(16, 0))
	return pos
}

func decodeLink(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternLink, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	b := kit.NewDateBuilder(nil)
	b.SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	pos.Set(model.KeySteps, parser.NextInt())
	pos.Set("turnovers", parser.NextInt())

	b.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	date := b.Date()
	m.LastLocation(pos, &date)

	processStatus(pos, parser.NextLongRadix(16, 0))
	return pos
}

func decodeV3(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternV3, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	b := kit.NewDateBuilder(nil)
	b.SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	mcc := parser.MustInt()
	mnc := parser.MustInt()

	count := parser.MustInt()
	network := model.NewNetwork()
	values := kit.Split(parser.Next(), ",")
	for i := range count {
		network.AddCellTower(model.NewCellTower(mcc, mnc, kit.Int(values[i*4]), int64(kit.Int(values[i*4+1]))))
	}

	pos.Network = network

	pos.Set(model.KeyBattery, parser.NextHexInt())

	b.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	date := b.Date()
	m.LastLocation(pos, &date)

	processStatus(pos, parser.NextLongRadix(16, 0))
	return pos
}

func decodeSms(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternSms, sentence)
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

	pos.Set(model.KeyResult, parser.Next())
	return pos
}

func decodeVp1(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternVp1, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if parser.HasNextN(3) {
		m.LastLocation(pos, nil)
		mcc := parser.MustInt()
		mnc := parser.MustInt()
		network := model.NewNetwork()
		for _, cell := range kit.Split(parser.Next(), "Y") {
			values := kit.Split(cell, ",")
			network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc,
				kit.Int(values[0]), int64(kit.Int(values[1])), kit.Int(values[2])))
		}
		pos.Network = network
	} else {
		pos.Valid = true
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
		pos.Speed = parser.MustDouble()
		pos.Course = parser.MustDouble()
		b := kit.NewDateBuilder(nil)
		b.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
		pos.SetTime(b.Date())
	}
	return pos
}

func decodeHeartbeat(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternHtbt, sentence)
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

	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	return pos
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	switch kit.Latin1(buf.GetBytes(0, 1)) {
	case "*":
		sentence := kit.Trim(kit.Latin1(buf.Bytes()))
		typeStart := kit.IndexOfFrom(sentence, ",", kit.IndexOf(sentence, ",")+1) + 1
		typeEnd := kit.IndexOfFrom(sentence, ",", typeStart)
		if typeEnd < 0 {
			typeEnd = kit.IndexOfFrom(sentence, "#", typeStart)
		}
		if typeEnd <= 0 {
			return nil
		}
		switch kit.Substring(sentence, typeStart, typeEnd) {
		case "V0", "HTBT":
			m.Send(kit.Substring(sentence, 0, typeEnd) + "#")
			return decodeHeartbeat(m, sentence)
		case "NBR":
			return decodeLbs(m, sentence)
		case "LINK":
			return decodeLink(m, sentence)
		case "V3":
			return decodeV3(m, sentence)
		case "VP1":
			return decodeVp1(m, sentence)
		case "SMS":
			return decodeSms(m, sentence)
		default:
			return decodeText(m, sentence)
		}
	case "$":
		return decodeBinary(m, buf)
	}
	return nil
}

// encode is H02ProtocolEncoder.encodeCommand(command, time), time being the message
// clock; the time is formatted in the server's zone, as Java's %tH formats a Date.
func encode(m *ingest.Message, c *model.Command) any {
	uniqueID := m.UniqueID(c.DeviceID)
	format := func(t string, params ...string) string {
		result := "*HQ," + uniqueID + "," + t + "," + m.Now.In(m.Zone()).Format("150405")
		for _, param := range params {
			result += "," + param
		}
		return result + "#"
	}
	switch c.Type {
	case model.CommandAlarmArm:
		return format("SCF", "0", "0")
	case model.CommandAlarmDisarm:
		return format("SCF", "1", "1")
	case model.CommandEngineStop:
		return format("S20", "1", "1")
	case model.CommandEngineResume:
		return format("S20", "1", "0")
	case model.CommandPositionPeriodic:
		value, ok := c.Attributes.Get(model.CommandKeyFrequency)
		if !ok || value == nil {
			kit.Throw("NullPointerException: frequency")
		}
		frequency := model.JavaToString(value)
		if v := m.LookupBool(c.DeviceID, m.Protocol()+".alternative", false, true); v != nil && *v {
			return format("D1", frequency)
		}
		return format("S71", "22", frequency)
	}
	return nil
}
