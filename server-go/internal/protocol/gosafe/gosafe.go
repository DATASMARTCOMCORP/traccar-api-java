// Package gosafe is the GoSafe protocol (GoSafeProtocol): "*GS" text reports and escaped
// binary reports (0xF8 delimited) over TCP and UDP. Every message is answered with "1234".
package gosafe

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: TCP (framed) and UDP servers.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "gosafe",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
			}},
		},
	}
}

// Framer is GoSafeFrameDecoder: text frames end with '#'; binary frames end with 0xF8 and
// are unescaped (0x1B 0x00 is 0x1B, 0x1B 0xE3 is 0xF8, 0x1B with another byte is dropped).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.GetByte(buf.ReaderIndex()) == '*' {
			if index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '#'); index != -1 {
				return buf.ReadSlice(index + 1 - buf.ReaderIndex())
			}
			return nil
		}
		index := buf.IndexOf(buf.ReaderIndex()+1, buf.WriterIndex(), 0xf8)
		if index < 0 {
			return nil
		}
		result := kit.Empty()
		for buf.ReaderIndex() <= index {
			b := buf.ReadUnsignedByte()
			if b == 0x1b {
				switch buf.ReadUnsignedByte() {
				case 0x00:
					result.WriteByte8(0x1b)
				case 0xe3:
					result.WriteByte8(0xf8)
				}
			} else {
				result.WriteByte8(b)
			}
		}
		return result
	}}
}

var pattern = kit.NewPattern().
	Text("*GS").             // header
	Number("d+,").           // protocol version
	Number("(d+),").         // imei
	Expression("([^#]*)#?"). // data
	Compile()

var patternOld = kit.NewPattern().
	Text("*GS").                   // header
	Number("d+,").                 // protocol version
	Number("(d+),").               // imei
	Text("GPS:").                  //
	Number("(dd)(dd)(dd);").       // time (hhmmss)
	Number("d;").Optional().       // fix type
	Expression("([AV]);").         // validity
	Number("([NS])(d+.d+);").      // latitude
	Number("([EW])(d+.d+);").      // longitude
	Number("(d+)?;").              // speed
	Number("(d+);").               // course
	Number("(d+.?d*)").Optional(). // hdop
	Number("(dd)(dd)(dd)").        // date (ddmmyy)
	Any().
	Compile()

var (
	timePattern = regexp.MustCompile(`^\d{10}[1-9]\d$`)
	hexPattern  = regexp.MustCompile(`^[0-9a-fA-F]+$`) // \p{XDigit}
)

// fields reads values in order; reading past the end panics, as Java's array access.
type fields struct {
	values []string
	i      int
}

func (f *fields) next() string {
	v := f.values[f.i]
	f.i++
	return v
}

func (f *fields) more() bool { return f.i < len(f.values) }

func decodeTextFragment(pos *model.Position, fragment string) {
	dataIndex := kit.IndexOf(fragment, ":")
	var values []string
	if kit.Len(fragment) != dataIndex+1 {
		values = kit.Split(kit.SubstringFrom(fragment, dataIndex+1), ";")
	}
	v := &fields{values: values}

	switch kit.Substring(fragment, 0, dataIndex) {
	case "GPS":
		pos.Valid = v.next() == "A"
		pos.Set(model.KeySatellites, kit.Int(v.next()))
		value := v.next()
		pos.SetLatitude(kit.Double(kit.SubstringFrom(value, 1)))
		if kit.CharAt(value, 0) == 'S' {
			pos.SetLatitude(-pos.Latitude)
		}
		value = v.next()
		pos.SetLongitude(kit.Double(kit.SubstringFrom(value, 1)))
		if kit.CharAt(value, 0) == 'W' {
			pos.SetLongitude(-pos.Longitude)
		}
		if value = v.next(); value != "" {
			pos.Speed = units.KnotsFromKph(float64(kit.Int(value)))
		}
		pos.Course = float64(kit.Int(v.next()))
		if v.more() {
			if value = v.next(); value != "" {
				pos.Altitude = float64(kit.Int(value))
			}
		}
		if v.more() {
			if value = v.next(); value != "" {
				pos.Set(model.KeyHdop, kit.Double(value))
			}
		}
		if v.more() {
			if value = v.next(); value != "" {
				pos.Set(model.KeyVdop, kit.Double(value))
			}
		}
	case "GSM":
		v.next() // registration status
		v.next() // signal strength
		mcc := kit.Int(v.next())
		mnc := kit.Int(v.next())
		lac := kit.IntRadix(v.next(), 16)
		cid := kit.IntRadix(v.next(), 16)
		rssi := kit.Int(v.next())
		pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
	case "COT":
		if v.more() {
			pos.Set(model.KeyOdometer, kit.Long(v.next()))
		}
		if v.more() {
			hours := kit.Split(v.values[v.i], "-")
			total := int32(kit.Int(hours[0])) * 3600
			if len(hours) > 1 {
				total += int32(kit.Int(hours[1])) * 60
			}
			if len(hours) > 2 {
				total += int32(kit.Int(hours[2]))
			}
			pos.Set(model.KeyHours, int(total*1000)) // int arithmetic, as Java's
		}
	case "ADC":
		pos.Set(model.KeyPower, kit.Double(v.next()))
		if v.more() {
			pos.Set(model.KeyBattery, kit.Double(v.next()))
		}
		if v.more() {
			pos.Set(model.PrefixAdc+"1", kit.Double(v.next()))
		}
		if v.more() {
			pos.Set(model.PrefixAdc+"2", kit.Double(v.next()))
		}
	case "DTT":
		pos.Set(model.KeyStatus, kit.IntRadix(v.next(), 16))
		if value := v.next(); value != "" {
			io := kit.IntRadix(value, 16)
			pos.Set(model.KeyIgnition, io&(1<<0) != 0)
			pos.Set(model.PrefixIn+"1", io&(1<<1) != 0)
			pos.Set(model.PrefixIn+"2", io&(1<<2) != 0)
			pos.Set(model.PrefixIn+"3", io&(1<<3) != 0)
			pos.Set(model.PrefixIn+"4", io&(1<<4) != 0)
			pos.Set(model.PrefixOut+"1", io&(1<<5) != 0)
			pos.Set(model.PrefixOut+"2", io&(1<<6) != 0)
			pos.Set(model.PrefixOut+"3", io&(1<<7) != 0)
		}
		geofence := v.next()
		pos.Set(model.KeyGeofence, geofence+v.next())
		pos.Set("eventStatus", v.next())
		if v.more() {
			pos.Set("packetType", v.next())
		}
	case "ETD":
		pos.Set("eventData", v.next())
	case "OBD":
		pos.Set("obd", v.next())
	case "TAG":
		pos.Set("tagData", v.next())
	case "IWD":
		for v.more() {
			sensorIndex := kit.Int(v.next())
			dataType := kit.Int(v.next())
			if dataType == 0 {
				pos.Set(model.KeyDriverUniqueID, v.next())
			} else if dataType == 1 {
				v.next() // temperature sensor serial number
				pos.Set(model.PrefixTemp+strconv.Itoa(sensorIndex), kit.Double(v.next()))
			}
		}
	}
}

func decodeTextPosition(m *ingest.Message, ds *ingest.DeviceSession, sentence string) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	fragments := kit.Split(sentence, ",")
	index := 0

	if timePattern.MatchString(fragments[index]) {
		pos.SetTime(kit.NewDateFormat("HHmmssddMMyy", m.Zone()).Parse(fragments[index]))
	} else {
		m.LastLocation(pos, nil)
		pos.Set(model.KeyResult, fragments[index])
	}
	index++

	for ; index < len(fragments); index++ {
		if fragments[index] != "" {
			if hexPattern.MatchString(fragments[index]) {
				pos.Set(model.KeyEvent, kit.IntRadix(fragments[index], 16))
			} else {
				decodeTextFragment(pos, fragments[index])
			}
		}
	}

	return pos
}

func decode(m *ingest.Message, msg any) any {
	m.Send(kit.NewBuf(kit.NettyBytes("1234")))

	buf := msg.(*kit.Buf)
	if buf.GetByte(buf.ReaderIndex()) == '*' {
		return decodeText(m, kit.Latin1(buf.Bytes()))
	}
	return decodeBinary(m, buf)
}

func decodeText(m *ingest.Message, sentence string) any {
	p := pattern
	if strings.HasPrefix(sentence, "*GS02") {
		p = patternOld
	}

	parser := kit.NewParser(p, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	if p == patternOld {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		date := kit.NewDateBuilder(nil).
			SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

		pos.Valid = parser.Next() == "A"
		pos.SetLatitude(parser.NextCoordinate(kit.HemDeg))
		pos.SetLongitude(parser.NextCoordinate(kit.HemDeg))
		pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
		pos.Course = parser.NextDoubleDefault(0)

		pos.Set(model.KeyHdop, parser.NextNullable())

		date.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
		pos.SetTime(date.Date())

		return pos
	}

	positions := []*model.Position{}
	for _, item := range kit.Split(parser.Next(), "$") {
		positions = append(positions, decodeTextPosition(m, ds, item))
	}
	return positions
}

func decodeBinary(m *ingest.Message, buf *kit.Buf) any {
	buf.ReadUnsignedByte() // header
	buf.ReadUnsignedByte() // protocol version
	typ := buf.ReadUnsignedByte()

	imei := strconv.FormatInt(buf.ReadUnsignedInt()<<24|int64(buf.ReadUnsignedMedium()), 10)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	seconds := buf.ReadUnsignedInt() + 946684800 // from 2000-01-01
	pos.SetTime(time.UnixMilli(seconds * 1000))

	if typ == 0x41 {
		buf.ReadUnsignedByte() // event id
	}

	mask := buf.ReadUnsignedShort()
	check := func(v, i int) bool { return v&(1<<i) != 0 }

	if check(mask, 0) {
		buf.Skip(buf.ReadUnsignedByte()) // SYS
	}

	if check(mask, 1) {
		buf.ReadUnsignedByte() // length
		fragmentMask := buf.ReadUnsignedShort()

		if check(fragmentMask, 0) {
			flags := buf.ReadUnsignedByte()
			pos.Valid = kit.BetweenInt(flags, 5, 7) > 0
			pos.Set(model.KeySatellites, kit.ToInt(flags, 5))
		}
		if check(fragmentMask, 1) {
			pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
			pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
		}
		if check(fragmentMask, 2) {
			pos.Speed = units.KnotsFromKph(float64(buf.ReadShort()))
		}
		if check(fragmentMask, 3) {
			pos.Course = float64(buf.ReadUnsignedShort())
		}
		if check(fragmentMask, 4) {
			pos.Altitude = float64(buf.ReadShort())
		}
		if check(fragmentMask, 5) {
			pos.Set(model.KeyHdop, float64(buf.ReadUnsignedShort())/100.0)
		}
		if check(fragmentMask, 6) {
			pos.Set(model.KeyVdop, float64(buf.ReadUnsignedShort())/100.0)
		}
	} else {
		t := pos.DeviceTime.Time
		m.LastLocation(pos, &t)
	}

	for i := 2; i <= 7; i++ { // GSM, COT, ADC, DTT, IWD, ETD
		if check(mask, i) {
			buf.Skip(buf.ReadUnsignedByte())
		}
	}

	return pos
}
