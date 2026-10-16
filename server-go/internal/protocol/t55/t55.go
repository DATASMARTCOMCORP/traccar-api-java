// Package t55 is the T55 protocol (T55Protocol): NMEA-like text sentences ($GPRMC, $GPGGA,
// $PUBX, QZE...) over TCP lines and UDP, with the device identified by a prefix or a
// separate identification sentence.
package t55

import (
	"net"
	"regexp"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: TCP (lines) and UDP servers.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "t55",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
					Text:    true,
					TextOut: true,
					Decoder: &Decoder{},
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Text:    true,
					TextOut: true,
					Decoder: &Decoder{Datagram: true},
				}
			}},
		},
	}
}

// Decoder is T55ProtocolDecoder: it keeps a $GPRMC position received before the device
// identified itself ($GPFID).
type Decoder struct {
	Datagram bool // the channel is a DatagramChannel
	position *model.Position
}

var patternGprmc = kit.NewPattern().
	Text("$").
	Expression("G[PLN]RMC,").
	Number("(dd)(dd)(dd).?d*,"). // time (hhmmss)
	Expression("([AV]),").       // validity
	Number("(dd)(dd.d+),").      // latitude
	Expression("([NS]),").
	Number("(d{2,3})(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.?d*)?,").    // speed
	Number("(d+.?d*)?,").    // course
	Number("(dd)(dd)(dd),"). // date (ddmmyy)
	Expression("[^*]+").
	Text("*").
	Expression("[^,]+").
	Number(",(d+)").              // satellites
	Number(",(d+)").              // imei
	Expression(",([01])").        // ignition
	Number(",(d+)").              // fuel
	Number(",(d+)").OptionalN(7). // battery
	Number("((?:,d+)+)?").        // parameters
	Any().
	Compile()

var patternGpgga = kit.NewPattern().
	Text("$").
	Expression("G[PLN]GGA,").
	Number("(dd)(dd)(dd).?d*,"). // time (hhmmss)
	Number("(d+)(dd.d+),").      // latitude
	Expression("([NS]),").
	Number("(d+)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+),").       // fix quality
	Number("(d+),").       // satellites
	Number("(d+.?d*),").   // hdop
	Number("(-?d+.?d*),"). // altitude
	Any().
	Compile()

var patternGpgll = kit.NewPattern().
	Text("$").
	Expression("G[PLN]GLL,").
	Number("(d+)(dd.d+),"). // latitude
	Expression("([NS]),").
	Number("(d+)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(dd)(dd)(dd).?d*,"). // time (hhmmss)
	Expression("([AV]),").       // validity
	Any().
	Compile()

var patternGprma = kit.NewPattern().
	Text("$GPRMA,").
	Expression("([AV]),").  // validity
	Number("(dd)(dd.d+),"). // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+),"). // longitude
	Expression("([EW]),,,").
	Number("(d+.?d*)?,"). // speed
	Number("(d+.?d*)?,"). // course
	Any().
	Compile()

var patternTrccr = kit.NewPattern().
	Text("$TRCCR,").
	Number("(dddd)(dd)(dd)").    // date (yyyymmdd)
	Number("(dd)(dd)(dd).?d*,"). // time (hhmmss)
	Expression("([AV]),").       // validity
	Number("(-?d+.d+),").        // latitude
	Number("(-?d+.d+),").        // longitude
	Number("(d+.d+),").          // speed
	Number("(d+.d+),").          // course
	Number("(-?d+.d+),").        // altitude
	Number("(d+.?d*),").         // battery
	Any().
	Compile()

var patternGpiop = kit.NewPattern().
	Text("$GPIOP,").
	Number("[01]{8},"). // inputs
	Number("[01]{8},"). // outputs
	Number("d+.d+,").   // adc 1
	Number("d+.d+,").   // adc 2
	Number("d+.d+,").   // adc 3
	Number("d+.d+,").   // adc 4
	Number("(d+.d+),"). // power
	Number("(d+.d+)").  // battery
	Any().
	Compile()

var patternQze = kit.NewPattern().
	Text("QZE,").
	Number("(d{15}),").        // imei
	Number("(d+),").           // event
	Number("(dd)(dd)(dddd),"). // date (mmddyyyy)
	Number("(dd)(dd)(dd),").   // time (hhmmss)
	Number("(-?d+.d+),").      // latitude
	Number("(-?d+.d+),").      // longitude
	Number("(d+),").           // speed
	Number("(d+),").           // course
	Expression("([AV]),").     // validity
	Expression("([01])").      // ignition
	Compile()

var patternPubx = kit.NewPattern().
	Text("$PUBX,").
	Number("(d+),").                // index
	Number("(dd)(dd)(dd).d+,").     // time (hhmmss)
	Number("(dd)(dd.d+),([NS]),").  // latitude
	Number("(ddd)(dd.d+),([EW]),"). // longitude
	Number("(-?d+.d+),").           // altitude
	Expression("(..),").            // status
	Number("(d+.d+),").             // horizontal accuracy
	Number("d+.d+,").               // vertical accuracy
	Number("(d+.d+),").             // speed
	Number("(d+.d+),").             // course
	Number("-?d+.d+,").             // vertical velocity
	Expression("[^,]*,").           // corrections age
	Number("(d+.d+),").             // hdop
	Number("(d+.d+),").             // vdop
	Number("d+.d+,").               // tdop
	Number("(d+),").                // satellites
	Number("(d+),").                // device id
	Number("d+").
	Text("*").
	Number("xx"). // checksum
	Compile()

var patternGptxt = kit.NewPattern().
	Text("$GPTXT,").
	Text("NET,").
	Number("(d+),").        // device id
	Expression("([^,]+),"). // network operator
	Number("(-d+),").       // rssi
	Number("(d+) ").        // mcc
	Number("(d+)").         // mnc
	Text("*").
	Number("xx"). // checksum
	Compile()

var patternWmcs = kit.NewPattern().
	Text("$ID,").
	Number("(d+),"). // imei
	Expression(".*?").
	Expression("ALARM,(0x[0-9a-fA-F]+),").Optional(). // alarm
	Expression(".*?").
	Text("GPSE").Expression("[XHTD],").
	Expression("([AV])").                      // validity
	Number(",D,(dd)(dd)(dd)").Optional().      // date (ddmmyy)
	Number(",T,(dd)(dd)(dd)").Optional().      // time (hhmmss)
	Number(",S,(d+):(d+)").Optional().         // satellites
	Number(",La,(-?d+.d+),([NS])").Optional(). // latitude
	Number(",Lo,(-?d+.d+),([EW])").Optional(). // longitude
	Number(",H,(d+.d+)").Optional().           // heading
	Number(",V,(d+.d+)").Optional().           // speed (km/h)
	Number(",DD,(d+)").Optional().             // total distance
	Any().
	Compile()

func (d *Decoder) decodeGprmc(m *ingest.Message, ds *ingest.DeviceSession, sentence string) any {
	if ds != nil && !d.Datagram {
		if ack := m.LookupBool(ds.DeviceID, m.Protocol()+".ack", false, true); ack != nil && *ack {
			m.Send("OK1\r\n")
		}
	}

	parser := kit.NewParser(patternGprmc, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	if ds != nil {
		pos.DeviceID = ds.DeviceID
	}

	date := kit.NewDateBuilder(nil).
		SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)
	date.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(date.Date())

	if parser.HasNextN(5) {
		pos.Set(model.KeySatellites, parser.NextInt())
		ds = m.Session(parser.Next())
		if ds == nil {
			return nil
		}
		pos.DeviceID = ds.DeviceID
		pos.Set(model.KeyIgnition, parser.HasNext() && parser.Next() == "1")
		pos.Set(model.KeyFuel, parser.NextIntDefault(0))
		pos.Set(model.KeyBattery, parser.NextInt())
	}

	if parser.HasNext() {
		parameters := kit.Split(parser.Next(), ",")
		for i := 1; i < len(parameters); i++ {
			pos.Set(model.PrefixIo+kit.Format("%d", i), parameters[i])
		}
	}

	if ds != nil {
		return pos
	}
	d.position = pos // save position
	return nil
}

func decodeGpgga(m *ingest.Message, ds *ingest.DeviceSession, sentence string) any {
	parser := kit.NewParser(patternGpgga, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	date := kit.NewDateBuilder(nil).
		SetCurrentDate(m.Now).
		SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.SetTime(date.Date())
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Valid = parser.MustInt() > 0
	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyHdop, parser.NextDouble())
	pos.Altitude = parser.MustDouble()
	return pos
}

func decodeGpgll(m *ingest.Message, ds *ingest.DeviceSession, sentence string) any {
	parser := kit.NewParser(patternGpgll, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	date := kit.NewDateBuilder(nil).
		SetCurrentDate(m.Now).
		SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(date.Date())
	pos.Valid = parser.Next() == "A"
	return pos
}

func decodeGprma(m *ingest.Message, ds *ingest.DeviceSession, sentence string) any {
	parser := kit.NewParser(patternGprma, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetTime(m.Now)
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)
	return pos
}

func decodeTrccr(m *ingest.Message, ds *ingest.DeviceSession, sentence string) any {
	parser := kit.NewParser(patternTrccr, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetTime(parser.NextDateTimeDefault())
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Speed = parser.MustDouble()
	pos.Course = parser.MustDouble()
	pos.Altitude = parser.MustDouble()
	pos.Set(model.KeyBattery, parser.NextDouble())
	return pos
}

func decodeGpiop(m *ingest.Message, ds *ingest.DeviceSession, sentence string) any {
	parser := kit.NewParser(patternGpiop, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID // a null session throws, as in Java
	m.LastLocation(pos, nil)
	pos.Set(model.KeyPower, parser.NextDouble())
	pos.Set(model.KeyBattery, parser.NextDouble())
	return pos
}

func decodeQze(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternQze, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyEvent, parser.NextInt())
	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Course = float64(parser.MustInt())
	pos.Valid = parser.Next() == "A"
	pos.Set(model.KeyIgnition, parser.MustInt() > 0)
	return pos
}

func decodeGptxt(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternGptxt, sentence)
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
	pos.Set(model.KeyOperator, parser.Next())
	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set("mcc", parser.NextInt())
	pos.Set("mnc", parser.NextInt())
	return pos
}

func decodePubx(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternPubx, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.Set(model.KeyIndex, parser.NextInt())
	pos.SetTime(parser.NextDateTime(kit.HMS, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Altitude = parser.MustDouble()
	pos.Valid = parser.Next() != "NF"
	pos.Accuracy = parser.MustDouble()
	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Course = parser.MustDouble()
	pos.Set(model.KeyHdop, parser.NextDouble())
	pos.Set(model.KeyVdop, parser.NextDouble())
	pos.Set(model.KeySatellites, parser.NextInt())

	ds := m.Session(parser.Next())
	if ds != nil {
		pos.DeviceID = ds.DeviceID
		return pos
	}
	return nil
}

func decodeWmcs(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternWmcs, sentence)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if parser.HasNext() {
		pos.Set(model.KeyAlarm, parser.Next())
	}

	pos.Valid = parser.Next() == "A"

	if parser.HasNextN(6) {
		pos.SetTime(kit.NewDateBuilder(nil).
			SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt()).
			SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt()).
			Date())
	} else {
		m.LastLocation(pos, nil)
	}

	if parser.HasNextN(2) {
		pos.Set(model.KeySatellitesVisible, parser.NextInt())
		pos.Set(model.KeySatellites, parser.NextInt())
	}
	if parser.HasNextN(2) {
		pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	}
	if parser.HasNextN(2) {
		pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	}
	if parser.HasNext() {
		pos.Course = parser.MustDouble()
	}
	if parser.HasNext() {
		pos.Speed = units.KnotsFromKph(parser.MustDouble())
	}
	if parser.HasNext() {
		pos.Set(model.KeyTotalDistance, parser.NextDouble())
	}
	return pos
}

var hexID = regexp.MustCompile(`^[0-9A-F]+$`)

// lastIndexOf is String.lastIndexOf(c): a UTF-16 index, -1 when absent.
func lastIndexOf(s string, c string) int {
	i := strings.LastIndex(s, c)
	if i < 0 {
		return -1
	}
	return kit.Len(s[:i])
}

// startsWithAt is String.startsWith(prefix, offset).
func startsWithAt(s, prefix string, offset int) bool {
	end := offset + kit.Len(prefix)
	return end <= kit.Len(s) && kit.Substring(s, offset, end) == prefix
}

// Decode decodes one sentence.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := kit.Trim(msg.(string))
	var ds *ingest.DeviceSession

	if !strings.HasPrefix(sentence, "$") && strings.Contains(sentence, "$") {
		index := kit.IndexOf(sentence, "$")
		id := kit.Substring(sentence, 0, index)
		if strings.HasSuffix(id, ",") {
			id = kit.Substring(id, 0, kit.Len(id)-1)
		} else if strings.HasSuffix(id, "/") {
			id = kit.Substring(id, kit.IndexOf(id, "/")+1, kit.Len(id)-1)
		}
		ds = m.Session(id)
		sentence = kit.SubstringFrom(sentence, index)
	} else {
		ds = m.Session()
		if ds == nil && m.Remote() != "" {
			if host, _, err := net.SplitHostPort(m.Remote()); err == nil {
				ds = m.Session(host)
			}
		}
	}

	switch {
	case strings.HasPrefix(sentence, "$PGID"):
		m.Session(kit.Substring(sentence, 6, kit.Len(sentence)-3))
	case strings.HasPrefix(sentence, "$DEVID"):
		m.Session(kit.Substring(sentence, 7, lastIndexOf(sentence, "*")))
	case strings.HasPrefix(sentence, "$PCPTI"):
		m.Session(kit.Substring(sentence, 7, kit.IndexOfFrom(sentence, ",", 7)))
	case strings.HasPrefix(sentence, "IMEI"):
		m.Session(kit.SubstringFrom(sentence, 5))
	case strings.HasPrefix(sentence, "$IMEI"):
		m.Session(kit.SubstringFrom(sentence, 6))
	case strings.HasPrefix(sentence, "$PSIWMDID"):
		m.Session(kit.Substring(sentence, 10, lastIndexOf(sentence, "*")))
	case strings.HasPrefix(sentence, "$CONNECT,"):
		m.Session(kit.Substring(sentence, 9, kit.IndexOfFrom(sentence, ",", 9)))
	case strings.HasPrefix(sentence, "$GPFID"):
		ds = m.Session(kit.SubstringFrom(sentence, 7))
		if ds != nil && d.position != nil {
			pos := d.position
			pos.DeviceID = ds.DeviceID
			d.position = nil
			return pos
		}
	case hexID.MatchString(sentence):
		m.Session(sentence)
	case startsWithAt(sentence, "RMC", 3):
		return d.decodeGprmc(m, ds, sentence)
	case startsWithAt(sentence, "GGA", 3) && ds != nil:
		return decodeGpgga(m, ds, sentence)
	case startsWithAt(sentence, "GLL", 3) && ds != nil:
		return decodeGpgll(m, ds, sentence)
	case strings.HasPrefix(sentence, "$GPRMA") && ds != nil:
		return decodeGprma(m, ds, sentence)
	case strings.HasPrefix(sentence, "$TRCCR") && ds != nil:
		return decodeTrccr(m, ds, sentence)
	case strings.HasPrefix(sentence, "$GPIOP"):
		return decodeGpiop(m, ds, sentence)
	case strings.HasPrefix(sentence, "QZE"):
		return decodeQze(m, sentence)
	case strings.HasPrefix(sentence, "$PUBX"):
		return decodePubx(m, sentence)
	case strings.HasPrefix(sentence, "$GPTXT"):
		return decodeGptxt(m, sentence)
	case strings.HasPrefix(sentence, "$ID,"):
		return decodeWmcs(m, sentence)
	}
	return nil
}
