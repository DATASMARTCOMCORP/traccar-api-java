// Package globalsat is the GlobalSat protocol (GlobalSatProtocol): "GS..." reports laid
// out by a configured format string, and "$..." reports, over TCP.
package globalsat

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Default formats of the <protocol>.format0 and <protocol>.format1 keys.
const (
	DefaultFormat0 = "TSPRXAB27GHKLMnaicz*U!"
	DefaultFormat1 = "SARY*U!"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	return ingest.Protocol{
		Name:         "globalsat",
		DataCommands: protocol.DataCommands("globalsat"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "!\r\n", "!"),
				Text:    true,
				TextOut: true,
				Decoder: NewDecoder(cfg, "globalsat"),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}, nil
}

// Decoder is GlobalSatProtocolDecoder: Format0 lays out GSr and GSb reports, Format1 GSh
// reports.
type Decoder struct {
	Format0 string
	Format1 string
}

// NewDecoder reads the formats from the configuration keys of the protocol name.
func NewDecoder(cfg *config.Config, name string) *Decoder {
	return &Decoder{
		Format0: cfg.String(name+".format0", DefaultFormat0),
		Format1: cfg.String(name+".format1", DefaultFormat1),
	}
}

func encode(m *ingest.Message, c *model.Command) any {
	uniqueID := m.UniqueID(c.DeviceID)
	var formatted string
	switch c.Type {
	case model.CommandCustom:
		formatted = kit.FormatCommand(c, uniqueID, "GSC,%s,%s", model.CommandKeyUniqueID, model.CommandKeyData)
	case model.CommandAlarmDismiss:
		formatted = kit.FormatCommand(c, uniqueID, "GSC,%s,Na", model.CommandKeyUniqueID)
	case model.CommandOutputControl:
		formatted = kit.FormatCommand(c, uniqueID, "GSC,%s,Lo(%s,%s)",
			model.CommandKeyUniqueID, model.CommandKeyIndex, model.CommandKeyData)
	default:
		return nil
	}
	return formatted + kit.NMEA(formatted) + "!"
}

// decodeVoltage gives nil (Java's null) for a value without a unit.
func decodeVoltage(value string) *float64 {
	var v float64
	switch {
	case strings.HasSuffix(value, "mV"):
		v = float64(kit.Int(kit.Substring(value, 0, kit.Len(value)-2))) / 1000.0
	case strings.HasSuffix(value, "V"):
		v = kit.Double(kit.Substring(value, 0, kit.Len(value)-1))
	default:
		return nil
	}
	return &v
}

func decodeAlarm(value int) string {
	check := func(i int) bool { return value&(1<<i) != 0 }
	switch {
	case check(0):
		return model.AlarmSos
	case check(3) || check(4):
		return model.AlarmGeofence
	case check(5):
		return model.AlarmOverspeed
	case check(6):
		return model.AlarmPowerCut
	case check(7):
		return model.AlarmLowPower
	}
	return ""
}

func (d *Decoder) decodeOriginal(m *ingest.Message, sentence string) any {
	m.Send("ACK\r")

	var format string
	switch {
	case strings.HasPrefix(sentence, "GSr") || strings.HasPrefix(sentence, "GSb"):
		format = d.Format0
	case strings.HasPrefix(sentence, "GSh"):
		format = d.Format1
	default:
		return nil
	}

	// Check that message contains required parameters
	if !strings.Contains(format, "B") || !strings.Contains(format, "S") ||
		!strings.ContainsAny(format, "123") || !strings.ContainsAny(format, "678") {
		return nil
	}

	if strings.Contains(format, "*") {
		format = kit.Substring(format, 0, kit.IndexOf(format, "*"))
		sentence = kit.Substring(sentence, 0, kit.IndexOf(sentence, "*"))
	}
	values := kit.Split(sentence, ",")

	pos := m.NewPosition()

	var cell model.CellTower

	for formatIndex, valueIndex := 0, 1; formatIndex < kit.Len(format) && valueIndex < len(values); formatIndex++ {
		value := strings.ReplaceAll(values[valueIndex], "\"", "")

		switch kit.CharAt(format, formatIndex) {
		case 'S':
			ds := m.Session(value)
			if ds == nil {
				return nil
			}
			pos.DeviceID = ds.DeviceID
		case 'A':
			if value == "" {
				pos.Valid = false
			} else {
				pos.Valid = kit.Int(value) != 1
			}
		case 'B':
			date := kit.NewDateBuilder(nil).
				SetDay(kit.Int(kit.Substring(value, 0, 2))).
				SetMonth(kit.Int(kit.Substring(value, 2, 4))).
				SetYear(kit.Int(kit.SubstringFrom(value, 4)))
			valueIndex++
			value = values[valueIndex]
			date.
				SetHour(kit.Int(kit.Substring(value, 0, 2))).
				SetMinute(kit.Int(kit.Substring(value, 2, 4))).
				SetSecond(kit.Int(kit.SubstringFrom(value, 4)))
			pos.SetTime(date.Date())
		case 'C':
			valueIndex++
		case '1':
			longitude := kit.Double(kit.SubstringFrom(value, 1))
			if kit.CharAt(value, 0) == 'W' {
				longitude = -longitude
			}
			pos.SetLongitude(longitude)
		case '2':
			longitude := kit.Double(kit.SubstringFrom(value, 4)) / 60
			longitude += float64(kit.Int(kit.Substring(value, 1, 4)))
			if kit.CharAt(value, 0) == 'W' {
				longitude = -longitude
			}
			pos.SetLongitude(longitude)
		case '3':
			pos.SetLongitude(kit.Double(value) / 1000000.0)
		case '6':
			latitude := kit.Double(kit.SubstringFrom(value, 1))
			if kit.CharAt(value, 0) == 'S' {
				latitude = -latitude
			}
			pos.SetLatitude(latitude)
		case '7':
			latitude := kit.Double(kit.SubstringFrom(value, 3)) / 60
			latitude += float64(kit.Int(kit.Substring(value, 1, 3)))
			if kit.CharAt(value, 0) == 'S' {
				latitude = -latitude
			}
			pos.SetLatitude(latitude)
		case '8':
			pos.SetLatitude(kit.Double(value) / 1000000.0)
		case 'G':
			pos.Altitude = kit.Double(value)
		case 'H':
			pos.Speed = kit.Double(value)
		case 'I':
			pos.Speed = units.KnotsFromKph(kit.Double(value))
		case 'J':
			pos.Speed = units.KnotsFromMph(kit.Double(value))
		case 'K':
			pos.Course = kit.Double(value)
		case 'L':
			pos.Set(model.KeySatellites, kit.Int(value))
		case 'P':
			if kit.Len(value) == 4 {
				pos.AddAlarm(decodeAlarm(kit.IntRadix(value, 16)))
			}
		case 'Z':
			if value != "" {
				pos.Set("geofence", value)
			}
		case 'Y':
			io := kit.IntRadix(value, 16)
			pos.Set(model.PrefixIn+"1", io&(1<<1) != 0)
			pos.Set(model.KeyMotion, io&(1<<7) != 0)
			pos.Set(model.PrefixOut+"1", io&(1<<9) != 0)
			pos.Set(model.KeyIgnition, io&(1<<13) != 0)
			pos.Set(model.KeyCharge, io&(1<<15) != 0)
		case 'c':
			cell.SetSignalStrength(kit.Int(value))
		case 'm':
			pos.Set(model.KeyPower, decodeVoltage(value))
		case 'n', 'N':
			pos.Set(model.KeyBattery, decodeVoltage(value))
		case 't':
			v := int64(kit.Int(value))
			cell.MobileCountryCode = &v
		case 'u':
			v := int64(kit.Int(value))
			cell.MobileNetworkCode = &v
		case 'v':
			v := int64(kit.IntRadix(value, 16))
			cell.LocationAreaCode = &v
		case 'w':
			v := kit.LongRadix(value, 16)
			cell.CellID = &v
		}

		valueIndex++
	}

	if cell.CellID != nil {
		pos.Network = model.NetworkOf(cell)
	}

	return pos
}

var pattern = kit.NewPattern().
	Text("$").
	Number("(d+),").         // imei
	Number("d+,").           // mode
	Number("(d+),").         // fix
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Expression("([EW])").
	Number("(ddd)(dd.d+),"). // longitude (dddmm.mmmm)
	Expression("([NS])").
	Number("(dd)(dd.d+),"). // latitude (ddmm.mmmm)
	Number("(d+.?d*),").    // altitude
	Number("(d+.?d*),").    // speed
	Number("(d+.?d*)?,").   // course
	Number("(d+)[,*]").     // satellites
	Number("(d+.?d*)").     // hdop
	Compile()

func decodeAlternative(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	pos.Valid = parser.Next() != "1"
	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLongitude(parser.NextCoordinate(kit.HemDegMin))
	pos.SetLatitude(parser.NextCoordinate(kit.HemDegMin))
	pos.Altitude = parser.NextDoubleDefault(0)
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)

	pos.Set(model.KeySatellites, parser.NextIntDefault(0))
	pos.Set(model.KeyHdop, parser.NextDouble())

	return pos
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	switch {
	case strings.HasPrefix(sentence, "GS"):
		return d.decodeOriginal(m, sentence)
	case strings.HasPrefix(sentence, "$"):
		return decodeAlternative(m, sentence)
	}
	return nil
}
