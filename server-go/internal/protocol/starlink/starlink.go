// Package starlink is the StarLink protocol (StarLinkProtocol): text lines over TCP whose
// data fields are listed by the "format" setting.
package starlink

import (
	"regexp"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

const (
	defaultFormat = "#EDT#,#EID#,#PDT#,#LAT#,#LONG#,#SPD#,#HEAD#,#ODO#," +
		"#IN1#,#IN2#,#IN3#,#IN4#,#OUT1#,#OUT2#,#OUT3#,#OUT4#,#LAC#,#CID#,#VIN#,#VBAT#,#DEST#,#IGN#,#ENG#"
	defaultDateFormat = "yyMMddHHmmss"
)

// Protocol is the protocol's definition: one TCP server of lines. The decoder reads
// starlink.format and starlink.dateFormat.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	format := cfg.String("starlink.format", defaultFormat)
	dateFormat := cfg.String("starlink.dateFormat", defaultDateFormat)
	return ingest.Protocol{
		Name: "starlink",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(1024),
				Text:    true,
				TextOut: true,
				Decoder: NewDecoder(format, dateFormat),
			}
		}}},
	}, nil
}

const msgEventReport = 6

var pattern = kit.NewPattern().
	Expression(".").         // protocol head
	Text("SLU").             // message head
	Number("(x{6}|d{15}),"). // id
	Number("(d+),").         // type
	Number("(d+),").         // index
	Expression("(.+)").      // data
	Text("*").
	Number("xx"). // checksum
	Compile()

// Decoder is StarLinkProtocolDecoder.
type Decoder struct {
	Format     string
	dateFormat *kit.DateFormat
}

// NewDecoder is the decoder with its init() settings.
func NewDecoder(format, dateFormat string) *Decoder {
	d := &Decoder{Format: format}
	d.SetDateFormat(dateFormat)
	return d
}

// SetDateFormat is setDateFormat.
func (d *Decoder) SetDateFormat(pattern string) {
	d.dateFormat = kit.NewDateFormat(pattern, time.UTC)
}

func (d *Decoder) format(m *ingest.Message, deviceID int64) []string {
	value, err := m.LookupString(deviceID, m.Protocol()+".format", false, true)
	if err != nil {
		panic(err)
	}
	if value != nil {
		return kit.Split(*value, ",")
	}
	return kit.Split(d.Format, ",")
}

func (d *Decoder) dateFormatOf(m *ingest.Message, deviceID int64) *kit.DateFormat {
	value, err := m.LookupString(deviceID, m.Protocol()+".dateFormat", false, false)
	if err != nil {
		panic(err)
	}
	if value != nil {
		return kit.NewDateFormat(*value, time.UTC)
	}
	return d.dateFormat
}

func parseCoordinate(value string) float64 {
	minutesIndex := kit.IndexOf(value, ".") - 2
	result := kit.Double(kit.Substring(value, 1, minutesIndex))
	result += kit.Double(kit.SubstringFrom(value, minutesIndex)) / 60
	if kit.CharAt(value, 0) == '+' {
		return result
	}
	return -result
}

func decodeAlarm(event int) string {
	switch event {
	case 6:
		return model.AlarmOverspeed
	case 7:
		return model.AlarmGeofenceEnter
	case 8:
		return model.AlarmGeofenceExit
	case 9:
		return model.AlarmPowerCut
	case 11:
		return model.AlarmLowBattery
	case 26:
		return model.AlarmTow
	case 36:
		return model.AlarmSos
	case 42:
		return model.AlarmJamming
	}
	return ""
}

var zeros = regexp.MustCompile(`^0+$`)

// Decode decodes one line.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, kit.Trim(msg.(string)))
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	if parser.NextIntDefault(0) != msgEventReport {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Valid = true

	pos.Set(model.KeyIndex, parser.NextIntDefault(0))

	data := kit.Split(parser.Next(), ",")
	var lac, cid *int
	event := 0

	dataTags := d.format(m, ds.DeviceID)
	dateFormat := d.dateFormatOf(m, ds.DeviceID)

	for i := 0; i < min(len(data), len(dataTags)); i++ {
		if data[i] == "" {
			continue
		}
		tag := dataTags[i]
		switch tag {
		case "#ALT#", "#ALTD#":
			pos.Altitude = kit.Double(data[i])
		case "#DAL#", "#DID#":
			pos.Set(model.KeyDriverUniqueID, data[i])
		case "#EDT#":
			pos.DeviceTime = model.NewTime(dateFormat.Parse(data[i]))
		case "#EDV1#", "#EDV2#":
			pos.Set("external"+string(kit.CharAt(tag, 4)), data[i])
		case "#EID#":
			event = kit.Int(data[i])
			pos.AddAlarm(decodeAlarm(event))
			pos.Set(model.KeyEvent, event)
			if event == 24 {
				pos.Set(model.KeyIgnition, true)
			} else if event == 25 {
				pos.Set(model.KeyIgnition, false)
			}
		case "#EDSC#":
			pos.Set("reason", data[i])
		case "#IARM#":
			pos.Set(model.KeyArmed, kit.Int(data[i]) > 0)
		case "#PDT#":
			pos.FixTime = model.NewTime(dateFormat.Parse(data[i]))
		case "#LAT#":
			pos.SetLatitude(parseCoordinate(data[i]))
		case "#LONG#":
			pos.SetLongitude(parseCoordinate(data[i]))
		case "#SPD#":
			pos.Speed = kit.Double(data[i])
		case "#SPDK#":
			pos.Speed = units.KnotsFromKph(kit.Double(data[i]))
		case "#HEAD#":
			pos.Course = float64(kit.Int(data[i]))
		case "#ODO#", "#ODOD#":
			pos.Set(model.KeyOdometer, int64(kit.Double(data[i])*1000))
		case "#BATC#":
			pos.Set(model.KeyBatteryLevel, kit.Int(data[i]))
		case "#BATH#":
			pos.Set("batteryHealth", kit.Int(data[i]))
		case "#TVI#":
			pos.Set(model.KeyDeviceTemp, kit.Double(data[i]))
		case "#CFL#":
			pos.Set(model.KeyFuel, kit.Int(data[i]))
		case "#CFL2#":
			pos.Set("fuel2", kit.Int(data[i]))
		case "#IN1#", "#IN2#", "#IN3#", "#IN4#":
			pos.Set(model.PrefixIn+string(kit.CharAt(tag, 3)), kit.Int(data[i]))
		case "#OUT1#", "#OUT2#", "#OUT3#", "#OUT4#":
			pos.Set(model.PrefixOut+string(kit.CharAt(tag, 4)), kit.Int(data[i]))
		case "#OUTA#", "#OUTB#", "#OUTC#", "#OUTD#":
			pos.Set(model.PrefixOut+kit.Format("%d", int(kit.CharAt(tag, 4)-'A'+1)), kit.Int(data[i]))
		case "#PDOP#":
			pos.Set(model.KeyPdop, kit.Double(data[i]))
		case "#LAC#":
			v := kit.Int(data[i])
			lac = &v
		case "#CID#":
			v := kit.Int(data[i])
			cid = &v
		case "#CSS#":
			pos.Set(model.KeyRssi, kit.Int(data[i]))
		case "#VIN#":
			pos.Set(model.KeyPower, kit.Double(data[i]))
		case "#VBAT#":
			pos.Set(model.KeyBattery, kit.Double(data[i]))
		case "#DEST#":
			pos.Set("destination", data[i])
		case "#IGN#", "#IGNL#", "#ENG#":
			pos.Set(model.KeyIgnition, kit.Int(data[i]) > 0)
		case "#DUR#", "#TDUR#":
			pos.Set(model.KeyHours, kit.Int(data[i]))
		case "#SAT#", "#SATN#":
			pos.Set(model.KeySatellitesVisible, kit.Int(data[i]))
		case "#SATU#":
			pos.Set(model.KeySatellites, kit.Int(data[i]))
		case "#STRT#":
			pos.Set("starter", kit.Double(data[i]))
		case "#TS1#":
			pos.Set("sensor1State", kit.Int(data[i]))
		case "#TS2#":
			pos.Set("sensor2State", kit.Int(data[i]))
		case "#V3#":
			pos.Set(model.PrefixAdc+"1", kit.Double(data[i]))
		case "#V4#":
			pos.Set(model.PrefixAdc+"2", kit.Double(data[i]))
		case "#TD1#", "#TD2#":
			td := parseTDx(kit.ParseBase64(data[i]))
			prefix := "sensor" + kit.Format("%d", td.sensorNumber)
			pos.Set(prefix+"Id", td.sensorID)
			pos.Set(prefix+"Temp", float64(td.temperature)/10.0)
			pos.Set(prefix+"Humidity", float64(td.temperature)/10.0) // Java reads the temperature
			pos.Set(prefix+"Voltage", float64(td.voltage)/1000.0)
		}
	}

	if pos.FixTime.IsZero() {
		m.LastLocation(pos, nil)
	}

	if lac != nil && cid != nil {
		pos.Network = model.NetworkOf(m.CellTowerFromLacCid(*lac, int64(*cid)))
	}

	if event == 20 {
		rfid := data[len(data)-1]
		if zeros.MatchString(rfid) {
			rfid = data[len(data)-2]
		}
		pos.Set(model.KeyDriverUniqueID, rfid)
	}

	return pos
}
