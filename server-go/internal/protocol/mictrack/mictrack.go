// Package mictrack is the Mictrack protocol (MictrackProtocol): "MT;" text reports,
// low-altitude NMEA batches and command results, over TCP and UDP without framing.
package mictrack

import (
	"regexp"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: TCP and UDP servers.
func Protocol() ingest.Protocol {
	pipeline := func() ingest.Pipeline {
		return ingest.Pipeline{Text: true, TextOut: true, Decoder: ingest.DecoderFunc(decode)}
	}
	return ingest.Protocol{
		Name:    "mictrack",
		Servers: []ingest.Server{{NewPipeline: pipeline}, {Datagram: true, NewPipeline: pipeline}},
	}
}

var dateFormat = kit.NewDateFormat("yyMMddHHmmss", time.UTC)

var patternLowAltitude = kit.NewPattern().
	Number("(dd)(dd)(dd).d+,"). // time (hhmmss.sss)
	Expression("([AV]),").      // validity
	Number("(d+)(dd.d+),").     // latitude
	Expression("([NS]),").
	Number("(d+)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.?d*)?,").   // speed
	Number("(d+.?d*)?,").   // course
	Number("(-?d+.?d*)?,"). // altitude
	Number("(dd)(dd)(dd)"). // date (ddmmyy)
	Compile()

func decodeAlarm(event int) string {
	switch event {
	case 0:
		return model.AlarmPowerOn
	case 5:
		return model.AlarmSos
	case 8:
		return model.AlarmLowBattery
	case 9:
		return model.AlarmGeofenceEnter
	case 10:
		return model.AlarmGeofenceExit
	case 12:
		return model.AlarmPowerOff
	}
	return ""
}

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

func decodeEvent(pos *model.Position, f *fields) {
	event := kit.Int(f.next())
	pos.AddAlarm(decodeAlarm(event))
	pos.Set(model.KeyEvent, event)
	pos.Set(model.KeyBattery, float64(kit.Int(f.next()))/1000.0)
}

func decodeLocation(pos *model.Position, data string) {
	f := &fields{values: kit.Split(data, "+")}
	pos.Set(model.KeySatellites, kit.Int(f.next()))
	pos.Valid = true
	pos.SetTime(dateFormat.Parse(f.next()))
	pos.SetLatitude(kit.Double(f.next()))
	pos.SetLongitude(kit.Double(f.next()))
	pos.Speed = units.KnotsFromKph(kit.Double(f.next()))
	pos.Course = float64(kit.Int(f.next()))
	decodeEvent(pos, f)
}

func decodeCell(network *model.Network, data string) {
	values := kit.Split(data, ",")
	length := 4
	if len(values)%5 == 0 {
		length = 5
	}
	for i := 0; i < len(values)/length; i++ {
		mnc := kit.Int(values[i*length])
		cid := kit.Int(values[i*length+1])
		lac := kit.Int(values[i*length+2])
		mcc := kit.Int(values[i*length+3])
		network.AddCellTower(model.NewCellTower(mcc, mnc, lac, int64(cid)))
	}
}

func decodeWifi(network *model.Network, data string, hasSsid bool) {
	values := kit.Split(data, ",")
	step, offset := 2, 0
	if hasSsid {
		step, offset = 3, 1
	}
	for i := 0; i < len(values)/step; i++ {
		network.AddWifiAccessPoint(model.NewWifiAccessPoint(
			values[i*step+offset], kit.Int(values[i*step+offset+1])))
	}
}

func decodeNetwork(m *ingest.Message, pos *model.Position, data string, hasWifi, hasSsid, hasCell bool) {
	f := &fields{values: kit.Split(data, "+")}
	t := dateFormat.Parse(f.next())
	m.LastLocation(pos, &t)
	network := model.NewNetwork()
	if hasWifi {
		decodeWifi(network, f.next(), hasSsid)
	}
	if hasCell {
		decodeCell(network, f.next())
	}
	pos.Network = network
	decodeEvent(pos, f)
}

func decodeStatus(m *ingest.Message, pos *model.Position, data string) {
	f := &fields{values: kit.Split(data, "+")}
	pos.Set(model.KeySatellites, kit.Int(f.next()))
	t := dateFormat.Parse(f.next())
	m.LastLocation(pos, &t)
	f.i += 4 // fix values
	decodeEvent(pos, f)
}

func decode(m *ingest.Message, msg any) any {
	sentence := kit.Trim(msg.(string))
	switch {
	case strings.HasPrefix(sentence, "MT"):
		if pos := decodeStandard(m, sentence); pos != nil {
			return pos
		}
		return nil
	case strings.Contains(sentence, "$"):
		return decodeLowAltitude(m, sentence)
	}
	if pos := decodeResult(m, sentence); pos != nil {
		return pos
	}
	return nil
}

// resultPattern is "\d{15} .+" as String.matches: '.' excludes Java's line terminators.
var resultPattern = regexp.MustCompile("^[0-9]{15} [^\n\r\u0085  ]+$")

func decodeResult(m *ingest.Message, sentence string) *model.Position {
	var ds *ingest.DeviceSession
	var result string
	if resultPattern.MatchString(sentence) {
		ds = m.Session(kit.Substring(sentence, 0, 15))
		if ds == nil {
			return nil
		}
		result = kit.Substring(sentence, 16, kit.Len(sentence)-1)
	} else {
		ds = m.Session()
		if ds == nil {
			return nil
		}
		result = kit.Substring(sentence, 0, kit.Len(sentence)-1)
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)
	pos.Set(model.KeyResult, result)
	return pos
}

func decodeStandard(m *ingest.Message, sentence string) *model.Position {
	fragments := kit.Split(sentence, ";")
	ds := m.Session(fragments[2])
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyType, kit.Int(fragments[1]))

	switch fragments[3] {
	case "R0":
		decodeLocation(pos, fragments[4])
	case "R1":
		decodeNetwork(m, pos, fragments[4], true, false, false)
	case "R2", "R3":
		decodeNetwork(m, pos, fragments[4], false, false, true)
	case "R12", "R13":
		decodeNetwork(m, pos, fragments[4], true, false, true)
	case "RH":
		decodeStatus(m, pos, fragments[4])
	case "Y1":
		decodeNetwork(m, pos, fragments[4], true, true, false)
	default:
		return nil
	}
	return pos
}

func decodeLowAltitude(m *ingest.Message, sentence string) any {
	separator := kit.IndexOf(sentence, "$")
	if separator < 0 {
		return nil
	}
	ds := m.Session(kit.Substring(sentence, 0, separator))
	if ds == nil {
		return nil
	}

	positions := []*model.Position{}
	for _, message := range kit.Split(kit.SubstringFrom(sentence, separator), "$") {
		parser := kit.NewParser(patternLowAltitude, message)
		if !parser.Matches() {
			continue
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		date := kit.NewDateBuilder(nil).SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())

		pos.Valid = parser.Next() == "A"
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
		pos.Speed = parser.NextDoubleDefault(0)
		pos.Course = parser.NextDoubleDefault(0)
		pos.Altitude = parser.NextDoubleDefault(0)

		date.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
		pos.SetTime(date.Date())

		positions = append(positions, pos)
	}
	return positions
}
