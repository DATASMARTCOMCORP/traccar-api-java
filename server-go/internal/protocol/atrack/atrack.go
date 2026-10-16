// Package atrack is the Atrack protocol (AtrackProtocol): binary and text reports over TCP
// and UDP, with the custom data of a configured or reported form.
package atrack

import (
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: TCP and UDP servers on the same port.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	opts, err := ReadOptions(cfg, "atrack")
	if err != nil {
		return ingest.Protocol{}, err
	}
	pipeline := func(framer bool) func() ingest.Pipeline {
		return func() ingest.Pipeline {
			pl := ingest.Pipeline{Decoder: &Decoder{Options: opts}, Encoder: ingest.CommandEncoderFunc(encode)}
			if framer {
				pl.Framer = Framer()
			}
			return pl
		}
	}
	return ingest.Protocol{
		Name:         "atrack",
		DataCommands: protocol.DataCommands("atrack"),
		Servers:      []ingest.Server{{NewPipeline: pipeline(true)}, {Datagram: true, NewPipeline: pipeline(false)}},
	}, nil
}

const keepAliveLength = 12

// Framer is AtrackFrameDecoder: keep-alives, binary frames with a length, and text
// reports whose length follows the third comma or that end with CRLF.
//
// Like the Java decoder, the text length is added to an absolute index.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 2 {
			return nil
		}
		r := buf.ReaderIndex()
		switch {
		case buf.GetUnsignedShort(r) == 0xfe02:
			if buf.ReadableBytes() >= keepAliveLength {
				return buf.ReadSlice(keepAliveLength)
			}
		case buf.GetUnsignedByte(r) == 0x40 && buf.GetByte(r+2) != ',':
			if buf.ReadableBytes() > 6 {
				length := buf.GetUnsignedShort(r+4) + 4 + 2
				if buf.ReadableBytes() >= length {
					return buf.ReadSlice(length)
				}
			}
		default:
			lengthStart := buf.IndexOf(r+3, buf.WriterIndex(), ',') + 1
			if lengthStart > 0 {
				lengthEnd := buf.IndexOf(lengthStart, buf.WriterIndex(), ',')
				if lengthEnd > 0 {
					length := lengthEnd + kit.Int(buf.GetLatin1(lengthStart, lengthEnd-lengthStart))
					if buf.ReadableBytes() > length && buf.GetByte(r+length) == '\n' {
						length++
					}
					if buf.ReadableBytes() >= length {
						return buf.ReadSlice(length)
					}
				}
			} else if end := buf.IndexOfIn("\r\n", r, buf.WriterIndex()); end > 0 {
				return buf.ReadSlice(end - r + 2)
			}
		}
		return nil
	}}
}

func encode(m *ingest.Message, c *model.Command) any {
	if c.Type == model.CommandCustom {
		data := "null"
		if s := kit.CommandString(c, model.CommandKeyData); s != nil {
			data = *s
		}
		return kit.NewBuf(kit.NettyBytes(data + "\r\n")) // copiedBuffer(..., US_ASCII)
	}
	return nil
}

// Options are the decoder's settings: <protocol>.longDate, .decimalFuel, .custom,
// .frameMask, .form and .alarmMap.
type Options struct {
	LongDate    bool
	DecimalFuel bool
	Custom      bool
	FrameMask   int
	Form        *string // nil: the device reports it
	AlarmMap    map[int]string
}

// ReadOptions is AtrackProtocolDecoder.init.
func ReadOptions(cfg *config.Config, protocol string) (Options, error) {
	o := Options{
		LongDate:    cfg.Bool(protocol+".longDate", false),
		DecimalFuel: cfg.Bool(protocol+".decimalFuel", false),
		Custom:      cfg.Bool(protocol+".custom", false),
		AlarmMap:    map[int]string{},
	}
	var err error
	if o.FrameMask, err = cfg.Int(protocol+".frameMask", 0); err != nil {
		return o, err
	}
	if form, ok := cfg.Lookup(protocol + ".form"); ok {
		o.Form, o.Custom = &form, true
	}
	if alarms, ok := cfg.Lookup(protocol + ".alarmMap"); ok {
		for _, pair := range kit.Split(alarms, ",") {
			if pair == "" {
				continue
			}
			eq := strings.IndexByte(pair, '=')
			if eq < 0 {
				return o, &kit.Fault{Msg: "StringIndexOutOfBoundsException: " + protocol + ".alarmMap " + pair}
			}
			id, err := model.ParseInt(pair[:eq])
			if err != nil {
				return o, err
			}
			o.AlarmMap[int(id)] = pair[eq+1:]
		}
	}
	return o, nil
}

// Decoder is AtrackProtocolDecoder (one per connection, for its photo buffer).
type Decoder struct {
	Options
	media *kit.Buf
}

var dateFormat = kit.NewDateFormat("yyyyMMddHHmmss", time.UTC)

const minDataLength = 40

func sendResponse(m *ingest.Message, rawID int64, index int) {
	m.Send(kit.Empty().WriteShort(0xfe02).WriteLong(rawID).WriteShort(index))
}

// readString reads a zero-terminated string: nil when empty. Without a terminator it
// reads nothing and skips one byte, as Java's does.
func readString(buf *kit.Buf) *string {
	var result *string
	if index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0); index > buf.ReaderIndex() {
		s := buf.ReadString(index - buf.ReaderIndex())
		result = &s
	}
	buf.ReadSignedByte()
	return result
}

func decodeBeaconData(pos *model.Position, mode, mask int, data *kit.Buf) {
	check := func(bit int) bool { return kit.Check(int64(mask), bit) }
	for i := 1; data.IsReadable(); i++ {
		tag := "tag" + strconv.Itoa(i)
		if check(7) {
			pos.Set(tag+"Id", data.ReadSlice(6).HexDump())
		}
		switch mode {
		case 1:
			if check(6) {
				data.ReadUnsignedShort() // major
			}
			if check(5) {
				data.ReadUnsignedShort() // minor
			}
			if check(4) {
				data.ReadUnsignedByte() // tx power
			}
			if check(3) {
				pos.Set(tag+"Rssi", data.ReadUnsignedByte())
			}
		case 2:
			if check(6) {
				data.ReadUnsignedShort() // battery voltage
			}
			if check(5) {
				pos.Set(tag+"Temp", data.ReadUnsignedShort())
			}
			if check(4) {
				data.ReadUnsignedByte() // tx power
			}
			if check(3) {
				pos.Set(tag+"Rssi", data.ReadUnsignedByte())
			}
		case 3:
			if check(6) {
				pos.Set(tag+"Humidity", data.ReadUnsignedShort())
			}
			if check(5) {
				pos.Set(tag+"Temp", data.ReadUnsignedShort())
			}
			if check(3) {
				pos.Set(tag+"Rssi", data.ReadUnsignedByte())
			}
			if check(2) {
				data.ReadUnsignedShort()
			}
		case 4:
			if check(6) {
				hardwareID := data.ReadUnsignedByte()
				if check(5) {
					switch hardwareID {
					case 1, 4:
						data.Skip(11) // fuel
					case 2:
						data.Skip(2) // temperature
					case 3:
						data.Skip(6) // temperature and luminosity
					case 5:
						data.Skip(10) // temperature, humidity, luminosity and pressure
					}
				}
			}
			if check(4) {
				data.Skip(9) // name
			}
		}
	}
}

// cell collects a CellTower's fields as the custom data sets them.
type cell struct {
	model.CellTower
}

func (c *cell) apply(pos *model.Position, nonZeroCellID bool) {
	t := c.CellTower
	if t.MobileCountryCode != nil && t.MobileNetworkCode != nil && t.CellID != nil &&
		(!nonZeroCellID || *t.CellID != 0) && t.LocationAreaCode != nil {
		pos.Network = model.NetworkOf(t)
	} else if t.SignalStrength != nil {
		pos.Set(model.KeyRssi, *t.SignalStrength)
	}
}

func int64p(v int64) *int64 { return &v }

func (d *Decoder) readTextCustomData(pos *model.Position, data, form string) {
	var tower cell
	keys := kit.Split(kit.SubstringFrom(form, 1), "%")
	values := kit.SplitRegex(data, ",|\r\n")
	for i := 0; i < min(len(keys), len(values)); i++ {
		v := values[i]
		switch keys[i] {
		case "SA":
			pos.Set(model.KeySatellites, kit.Int(v))
		case "MV":
			pos.Set(model.KeyPower, float64(kit.Int(v))/10.0)
		case "BV":
			pos.Set(model.KeyBattery, float64(kit.Int(v))/10.0)
		case "GQ":
			tower.SetSignalStrength(kit.Int(v))
		case "CE":
			tower.CellID = int64p(kit.Long(v))
		case "LC":
			tower.LocationAreaCode = int64p(int64(kit.Int(v)))
		case "CN":
			if kit.Len(v) > 3 {
				tower.MobileCountryCode = int64p(int64(kit.Int(kit.Substring(v, 0, 3))))
				tower.MobileNetworkCode = int64p(int64(kit.Int(kit.SubstringFrom(v, 3))))
			}
		case "PC":
			pos.Set(model.PrefixCount+"1", kit.Int(v))
		case "AT":
			pos.Altitude = float64(kit.Int(v))
		case "RP":
			pos.Set(model.KeyRpm, kit.Int(v))
		case "GS":
			pos.Set(model.KeyRssi, kit.Int(v))
		case "DT":
			pos.Set(model.KeyArchive, kit.Int(v) == 1)
		case "VN":
			pos.Set(model.KeyVin, v)
		case "TR":
			pos.Set(model.KeyThrottle, kit.Int(v))
		case "ET":
			pos.Set(model.KeyCoolantTemp, kit.Int(v))
		case "FL":
			pos.Set(model.KeyFuel, kit.Int(v))
		case "FC":
			pos.Set(model.KeyFuelConsumption, kit.Int(v))
		case "AV1":
			pos.Set(model.PrefixAdc+"1", kit.Int(v))
		case "CD":
			pos.Set(model.KeyIccid, v)
		case "EH":
			pos.Set(model.KeyHours, units.MsFromHoursFloat(float64(kit.Int(v))/10.0))
		case "IA":
			pos.Set("intakeTemp", kit.Int(v))
		case "EL":
			pos.Set(model.KeyEngineLoad, kit.Int(v))
		case "HA":
			if kit.Int(v) > 0 {
				pos.AddAlarm(model.AlarmAcceleration)
			}
		case "HB":
			if kit.Int(v) > 0 {
				pos.AddAlarm(model.AlarmBraking)
			}
		case "HC":
			if kit.Int(v) > 0 {
				pos.AddAlarm(model.AlarmCornering)
			}
		case "MT":
			pos.Set(model.KeyMotion, kit.Int(v) > 0)
		case "BC":
			if d.FrameMask == 1 {
				buf := kit.Hex(v)
				for index := 1; buf.IsReadable(); index++ {
					length := buf.ReadUnsignedByte()
					id := buf.ReadUnsignedByte()
					buf.Skip(6) // mac
					if id == 0xE0 {
						pos.Set("tag"+strconv.Itoa(index)+"Temp", float64(buf.ReadShort())/100.0)
						buf.Skip(length - 1 - 6 - 2)
					} else {
						buf.Skip(length - 1 - 6)
					}
				}
			} else {
				beacon := kit.Split(v, ":")
				decodeBeaconData(pos, kit.Int(beacon[0]), kit.Int(beacon[1]), kit.Hex(beacon[2]))
			}
		}
	}
	tower.apply(pos, false)
}

func (d *Decoder) readBinaryCustomData(pos *model.Position, buf *kit.Buf, form string) {
	var tower cell
	for _, key := range kit.Split(kit.SubstringFrom(form, 1), "%") {
		switch key {
		case "SA":
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		case "MT":
			pos.Set(model.KeyMotion, buf.ReadUnsignedByte() > 0)
		case "MV":
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/10.0)
		case "BV":
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/10.0)
		case "GQ":
			tower.SetSignalStrength(buf.ReadUnsignedByte())
		case "CE":
			tower.CellID = int64p(buf.ReadUnsignedInt())
		case "LC":
			tower.LocationAreaCode = int64p(int64(buf.ReadUnsignedShort()))
		case "CN":
			combined := int(buf.ReadUnsignedInt() % 100000) // cccnn
			tower.MobileCountryCode = int64p(int64(combined / 100))
			tower.MobileNetworkCode = int64p(int64(combined % 100))
		case "RL":
			buf.ReadUnsignedByte() // rxlev
		case "PC":
			pos.Set(model.PrefixCount+"1", buf.ReadUnsignedInt())
		case "AT":
			pos.Altitude = float64(buf.ReadUnsignedInt())
		case "RP":
			pos.Set(model.KeyRpm, buf.ReadUnsignedShort())
		case "GS":
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		case "DT":
			pos.Set(model.KeyArchive, buf.ReadUnsignedByte() == 1)
		case "VN":
			pos.Set(model.KeyVin, readString(buf))
		case "MF":
			buf.ReadUnsignedShort() // mass air flow rate
		case "EL":
			buf.ReadUnsignedByte() // engine load
		case "TR":
			pos.Set(model.KeyThrottle, buf.ReadUnsignedByte())
		case "ET":
			pos.Set(model.PrefixTemp+"1", buf.ReadUnsignedShort())
		case "FL":
			pos.Set(model.KeyFuel, buf.ReadUnsignedByte())
		case "ML":
			buf.ReadUnsignedByte() // mil status
		case "FC":
			pos.Set(model.KeyFuelConsumption, buf.ReadUnsignedInt())
		case "CI":
			readString(buf) // format string
		case "AV1":
			pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedShort())
		case "NC":
			readString(buf) // gsm neighbor cell info
		case "SM":
			buf.ReadUnsignedShort() // max speed between reports
		case "GL":
			readString(buf) // google link
		case "MA":
			readString(buf) // mac address
		case "PD":
			buf.ReadUnsignedByte() // pending code status
		case "CD":
			pos.Set(model.KeyIccid, readString(buf))
		case "CM":
			buf.ReadLong() // imsi
		case "GN":
			buf.Skip(60) // g sensor data
		case "GV":
			buf.Skip(6) // maximum g force
		case "ME":
			buf.ReadLong() // imei
		case "IA":
			buf.ReadUnsignedByte() // intake air temperature
		case "MP":
			buf.ReadUnsignedByte() // manifold absolute pressure
		case "EO":
			pos.Set(model.KeyOdometer, units.MetersFromMiles(float64(buf.ReadUnsignedInt())))
		case "EH":
			pos.Set(model.KeyHours, buf.ReadUnsignedInt()*360000)
		case "ZO1", "ZO2", "ZO3", "ZO5", "ZO7", "ZO8", "ZO11", "ZO12", "ZO13", "ZO15", "ZO16", "ZO17":
			buf.ReadUnsignedByte() // brake, warning, abs, parking brake, cruise, pedal, oil, boost, intake, brake pressures
		case "ZO4":
			pos.Set(model.KeyThrottle, float64(buf.ReadUnsignedByte())*0.4)
		case "ZO6":
			pos.Set(model.KeyObdSpeed, float64(buf.ReadUnsignedByte())*0.805)
		case "ZO9":
			pos.Set(model.KeyEngineLoad, float64(buf.ReadUnsignedByte())*0.5)
		case "ZO10":
			pos.Set(model.KeyFuel, float64(buf.ReadUnsignedByte())*0.5)
		case "ZO14":
			pos.Set(model.KeyCoolantTemp, buf.ReadUnsignedByte())
		case "ZH1":
			buf.ReadUnsignedShort() // cargo weight
		case "ZH2":
			pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedShort())*16.428/3600)
		case "ZH3":
			pos.Set(model.KeyRpm, float64(buf.ReadUnsignedShort())*0.25)
		case "ZL1", "ZL3":
			buf.ReadUnsignedInt() // fuel used (natural gas), vehicle hours
		case "ZL2":
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*161)
		case "ZL4":
			pos.Set(model.KeyHours, buf.ReadUnsignedInt()*5*36000)
		case "ZS1":
			pos.Set(model.KeyVin, readString(buf))
		case "JO1", "JO2", "JO3", "JO6", "JO7", "JO8", "JO9", "JO10", "JO12":
			buf.ReadUnsignedByte() // pedals, pto, pedal position, fms, drivers, system information, pto engaged
		case "JO4":
			pos.Set(model.KeyEngineLoad, buf.ReadUnsignedByte())
		case "JO5":
			pos.Set(model.KeyFuel, float64(buf.ReadUnsignedByte())*0.4)
		case "JO11":
			pos.Set(model.KeyCoolantTemp, buf.ReadUnsignedByte()-40)
		case "JH1":
			pos.Set(model.KeyObdSpeed, float64(buf.ReadUnsignedShort())/256.0)
		case "JH2":
			pos.Set(model.KeyRpm, float64(buf.ReadUnsignedShort())*0.125)
		case "JH3", "JH4", "JH5", "JH6", "JH7":
			index := kit.Int(kit.SubstringFrom(key, 2)) - 2
			pos.Set("axleWeight"+strconv.Itoa(index), float64(buf.ReadUnsignedShort())*0.5)
		case "JH8":
			pos.Set(model.KeyOdometerService, buf.ReadUnsignedShort()*5)
		case "JH9", "JH10", "JH12":
			buf.ReadUnsignedShort() // tachograph speed, ambient air temperature, fuel economy
		case "JH11":
			pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedShort())/20.0)
		case "JL1":
			pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedInt())*0.5)
		case "JL2":
			pos.Set(model.KeyHours, buf.ReadUnsignedInt()*5*36000)
		case "JL3":
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*1000)
		case "JL4":
			pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedInt())/1000.0)
		case "JS1":
			pos.Set(model.KeyVin, readString(buf))
		case "JS2":
			readString(buf) // fms version supported
		case "JS3":
			pos.Set("driver1", readString(buf))
		case "JS4":
			pos.Set("driver2", readString(buf))
		case "JN1", "JN2", "JN3", "JN4", "JN5":
			buf.ReadUnsignedInt() // cruise control distance, idling time and fuel, pto time and fuel
		case "IN0":
			pos.Set(model.KeyIgnition, buf.ReadUnsignedByte() > 0)
		case "IN1", "IN2", "IN3":
			pos.Set(model.PrefixIn+string(kit.CharAt(key, 2)), buf.ReadUnsignedByte() > 0)
		case "HA":
			if buf.ReadUnsignedByte() > 0 {
				pos.AddAlarm(model.AlarmAcceleration)
			}
		case "HB":
			if buf.ReadUnsignedByte() > 0 {
				pos.AddAlarm(model.AlarmBraking)
			}
		case "HC":
			if buf.ReadUnsignedByte() > 0 {
				pos.AddAlarm(model.AlarmCornering)
			}
		}
	}
	tower.apply(pos, true)
}

var patternInfo = kit.NewPattern().
	Text("$INFO=").
	Number("(d+),").        // unit id
	Expression("([^,]+),"). // model
	Expression("([^,]+),"). // firmware version
	Number("d+,").          // imei
	Number("d+,").          // imsi
	Number("d+,").          // sim card id
	Number("(d+),").        // power
	Number("(d+),").        // battery
	Number("(d+),").        // satellites
	Number("d+,").          // gsm status
	Number("(d+),").        // rssi
	Number("d+,").          // connection status
	Number("d+").           // antenna status
	Any().
	Compile()

func decodeInfo(m *ingest.Message, sentence string) any {
	pos := m.NewPosition()
	m.LastLocation(pos, nil) // no device yet: not outdated, as in Java

	var ds *ingest.DeviceSession
	if strings.HasPrefix(sentence, "$INFO") {
		parser := kit.NewParser(patternInfo, sentence)
		if !parser.Matches() {
			return nil
		}
		ds = m.Session(parser.Next())
		pos.Set("model", parser.Next())
		pos.Set(model.KeyVersionFw, parser.Next())
		pos.Set(model.KeyPower, float64(parser.MustInt())/10.0)
		pos.Set(model.KeyBattery, float64(parser.MustInt())/10.0)
		pos.Set(model.KeySatellites, parser.NextInt())
		pos.Set(model.KeyRssi, parser.NextInt())
	} else {
		ds = m.Session()
		pos.Set(model.KeyResult, sentence)
	}
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID
	return pos
}

var pattern = kit.NewPattern().
	Number("(d+),").      // date and time
	Number("d+,").        // rtc date and time
	Number("d+,").        // device date and time
	Number("(-?d+),").    // longitude
	Number("(-?d+),").    // latitude
	Number("(d+),").      // course
	Number("(d+),").      // report id
	Number("(d+.?d*),").  // odometer
	Number("(d+),").      // hdop
	Number("(d+),").      // inputs
	Number("(d+),").      // speed
	Number("(d+),").      // outputs
	Number("(d+),").      // adc
	Number("([^,]+)?,").  // driver
	Number("(d+),").      // temp1
	Number("(d+),").      // temp2
	Expression("[^,]*,"). // text message
	Expression("(.*)").   // custom data
	OptionalN(2).
	Compile()

var patternFuls = kit.CompilePattern("FULS:F=(\\p{XDigit}+) t=(\\p{XDigit}+) N=(\\p{XDigit}+)")

func (d *Decoder) decodeText(m *ingest.Message, sentence string) any {
	positionIndex := -1
	for i := 0; i < 5; i++ {
		positionIndex = kit.IndexOfFrom(sentence, ",", positionIndex+1)
	}
	headers := kit.Split(kit.Substring(sentence, 0, positionIndex), ",")
	id := kit.Long(headers[2])
	index := kit.Int(headers[3])
	ds := m.Session(headers[4])
	if ds == nil {
		return nil
	}
	sendResponse(m, id, index)

	positions := []*model.Position{}
	for _, line := range kit.Split(kit.SubstringFrom(sentence, positionIndex+1), "\r\n") {
		if pos := d.decodeTextLine(m, ds, line); pos != nil {
			positions = append(positions, pos)
		}
	}
	return positions
}

func (d *Decoder) decodeTextLine(m *ingest.Message, ds *ingest.DeviceSession, sentence string) *model.Position {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Valid = true

	if t := parser.Next(); kit.Len(t) >= 14 {
		pos.SetTime(dateFormat.Parse(t))
	} else {
		pos.SetTime(time.UnixMilli(kit.Long(t) * 1000))
	}

	pos.SetLongitude(float64(parser.MustInt()) / 1000000.0)
	pos.SetLatitude(float64(parser.MustInt()) / 1000000.0)
	pos.Course = float64(parser.MustInt())

	pos.Set(model.KeyEvent, parser.NextInt())
	pos.Set(model.KeyOdometer, parser.MustDouble()*100)
	pos.Set(model.KeyHdop, float64(parser.MustInt())/10.0)
	pos.Set(model.KeyInput, parser.NextInt())
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Set(model.KeyOutput, parser.NextInt())
	pos.Set(model.PrefixAdc+"1", parser.NextInt())

	if parser.HasNext() {
		pos.Set(model.KeyDriverUniqueID, parser.Next())
	}
	pos.Set(model.PrefixTemp+"1", parser.NextInt())
	pos.Set(model.PrefixTemp+"2", parser.NextInt())

	if d.Custom {
		next := parser.NextNullable()
		if next == nil {
			kit.Throw("NullPointerException: no custom data")
		}
		data := *next
		var form string
		if d.Form != nil {
			form = *d.Form
		} else {
			comma := kit.IndexOf(data, ",")
			form = kit.SubstringFrom(kit.Substring(data, 0, comma), len("%CI"))
			data = kit.SubstringFrom(data, comma+1)
		}
		d.readTextCustomData(pos, data, form)
	}
	return pos
}

func (d *Decoder) decodePhoto(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, id int64) any {
	t := buf.ReadUnsignedInt()
	index := buf.ReadUnsignedByte()
	count := buf.ReadUnsignedByte()

	if d.media == nil {
		d.media = m.NewMediaBuffer()
	}
	d.media.WriteBytes(buf.ReadBytes(buf.ReadUnsignedShort()))

	if index == count-1 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		deviceTime := time.UnixMilli(t * 1000)
		m.LastLocation(pos, &deviceTime)
		image := m.WriteMedia(strconv.FormatInt(id, 10), "jpg", d.media.Bytes())
		d.media = nil
		pos.Set(model.KeyImage, image)
		return pos
	}
	return nil
}

func (d *Decoder) decodeBinary(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) any {
	positions := []*model.Position{}
	for buf.ReadableBytes() >= minDataLength {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		if d.LongDate {
			pos.SetTime(kit.NewDateBuilder(nil).
				SetDate(buf.ReadUnsignedShort(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
				SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
				Date())
			buf.Skip(7 + 7)
		} else {
			pos.FixTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
			pos.DeviceTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
			buf.ReadUnsignedInt() // send time
		}

		pos.Valid = true
		pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
		pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
		pos.Course = float64(buf.ReadUnsignedShort())

		typ := buf.ReadUnsignedByte()
		pos.Set(model.KeyType, typ)
		pos.AddAlarm(d.AlarmMap[typ])

		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*100)
		pos.Set(model.KeyHdop, float64(buf.ReadUnsignedShort())/10.0)
		pos.Set(model.KeyInput, buf.ReadUnsignedByte())
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))
		pos.Set(model.KeyOutput, buf.ReadUnsignedByte())
		pos.Set(model.PrefixAdc+"1", float64(buf.ReadUnsignedShort())/1000.0)
		pos.Set(model.KeyDriverUniqueID, readString(buf))
		pos.Set(model.PrefixTemp+"1", float64(buf.ReadShort())/10.0)
		pos.Set(model.PrefixTemp+"2", float64(buf.ReadShort())/10.0)

		if message := readString(buf); message != nil && *message != "" {
			parser := kit.NewParser(patternFuls, *message)
			if parser.Find() {
				radix := 16
				if d.DecimalFuel {
					radix = 10
				}
				pos.Set(model.KeyFuel, float64(kit.IntRadix(parser.Group(3), radix))/10.0)
			} else {
				pos.Set("message", *message)
			}
		}

		if d.Custom {
			var form string
			if d.Form != nil {
				form = *d.Form
			} else {
				s := readString(buf)
				if s == nil {
					kit.Throw("NullPointerException: no form")
				}
				form = kit.SubstringFrom(kit.Trim(*s), len("%CI"))
			}
			d.readBinaryCustomData(pos, buf, form)
		}
		positions = append(positions, pos)
	}
	return positions
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	switch {
	case buf.GetUnsignedShort(buf.ReaderIndex()) == 0xfe02:
		m.Send(buf.Copy()) // keep-alive
		return nil
	case buf.GetByte(buf.ReaderIndex()) == '$':
		return decodeInfo(m, kit.Trim(kit.Latin1(buf.Bytes())))
	case buf.GetByte(buf.ReaderIndex()+2) == ',':
		return d.decodeText(m, kit.Trim(kit.Latin1(buf.Bytes())))
	}

	prefix := buf.ReadString(2)
	buf.ReadUnsignedShort() // checksum
	buf.ReadUnsignedShort() // length
	index := buf.ReadUnsignedShort()
	id := buf.ReadLong()
	ds := m.Session(strconv.FormatInt(id, 10))
	if ds == nil {
		return nil
	}
	sendResponse(m, id, index)
	if prefix == "@R" {
		return d.decodePhoto(m, ds, buf, id)
	}
	return d.decodeBinary(m, ds, buf)
}
