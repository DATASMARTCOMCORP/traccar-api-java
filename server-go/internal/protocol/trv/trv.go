// Package trv is the TRV protocol (TrvProtocol): "#"-terminated text messages of TRV and
// IW watches over TCP.
package trv

import (
	"regexp"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "trv",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "#"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var ignoreResponse = map[string]bool{
	"AP12": true, "AP14": true, "AP33": true, "AP34": true, "AP40": true, "AP76": true,
	"AP77": true, "AP84": true, "AP85": true, "AP86": true, "AP87": true,
}

var pattern = kit.NewPattern().
	Expression("[A-Z]{2,3}").
	Expression("[A-Z]P").
	Number("dd").
	Number("(dd)(dd)(dd)"). // date (yymmdd)
	Expression("([AV])").   // validity
	Number("(dd)(dd.d+)").  // latitude
	Expression("([NS])").
	Number("(ddd)(dd.d+)"). // longitude
	Expression("([EW])").
	Number("(ddd.d)").      // speed
	Number("(dd)(dd)(dd)"). // time (hhmmss)
	Number("([d.]{6})").    // course
	Number("(ddd)").        // gsm
	Number("(ddd)").        // satellites
	Number("(ddd)").        // battery
	Number("(d)").          // acc
	Number("(dd)").         // arm status
	Number("(dd)").         // working mode
	Number("(?:d{3,5})?,").
	Number("(d+),").            // mcc
	Number("(d+),").            // mnc
	Number("(d+),").            // lac
	Number("(d+)").             // cell
	Number(",(dd)").Optional(). // alarm
	GroupBegin().
	Text(",").
	Expression("(").
	GroupBegin().
	Expression(`[^\|]+`).         // name
	Number("|xx-xx-xx-xx-xx-xx"). // mac
	Number("|d+&?").              // signal
	GroupEndWith("+").
	Expression(")").
	GroupEndWith("?").
	Any().
	Compile()

var patternCP01 = kit.NewPattern().
	Expression("[A-Z]{2,3}").
	Text("CP01,").
	Number("(ddd)"). // gsm
	Number("(ddd)"). // gps
	Number("(ddd)"). // battery
	Number("(d)").   // acc
	Number("(dd)").  // arm status
	Number("(dd)").  // working mode
	GroupBegin().
	Number("(ddd)"). // interval
	Number("d").     // vibration alarm
	Number("ddd").   // vibration sensitivity
	Number("d").     // automatic arm
	Number("dddd").  // automatic arm time
	Number("(d)").   // blocked
	Number("(d)").   // power status
	Number("(d)").   // movement status
	GroupEndWith("?").
	Any().
	Compile()

var patternAP02 = kit.NewPattern().
	Expression("[A-Z]{2,3}").
	Text("AP02,").
	Expression("[^,]+,"). // language
	Number("[01],").      // reply
	Number("d+,").        // cell count
	Number("(d+),").      // mcc
	Number("(d+),").      // mnc
	Expression("(").
	GroupBegin().
	Number("d+|"). // lac
	Number("d+|"). // cid
	Number("d+,"). // rssi
	GroupEndWith("+").
	Expression(")").
	Number("d+,").      // wifi count
	Expression("(.*)"). // wifi
	Compile()

var patternAP03 = kit.NewPattern().
	Expression("[A-Z]{2,3}").
	Text("AP03,").
	Number("(ddd)"). // rssi
	Number("(ddd)"). // satellites
	Number("(ddd)"). // battery level
	Number("d").     // space
	Number("xx").    // fortification state
	Number("dd,").   // working mode
	Number("(d+),"). // steps
	Number("d+").    // rolls frequency
	Compile()

// decodeOptionalValue is a Boolean: nil (Java's null) for 0.
func decodeOptionalValue(parser *kit.Parser, activeValue int) *bool {
	value := parser.MustInt()
	if value != 0 {
		result := value == activeValue
		return &result
	}
	return nil
}

func decodeCommon(pos *model.Position, parser *kit.Parser) {
	pos.Set(model.KeyRssi, parser.NextInt())
	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyBattery, parser.NextInt())
	pos.Set(model.KeyIgnition, decodeOptionalValue(parser, 1))
	pos.Set(model.KeyArmed, decodeOptionalValue(parser, 1))

	if mode := parser.MustInt(); mode != 0 {
		pos.Set("mode", mode)
	}
}

func decodeWifi(network *model.Network, data string) {
	for _, wifi := range kit.Split(data, "&") {
		if wifi != "" {
			values := kit.Split(wifi, "|")
			network.AddWifiAccessPoint(model.NewWifiAccessPoint(
				strings.ReplaceAll(values[1], "-", ":"), kit.Int(values[2])))
		}
	}
}

var sixDigits = regexp.MustCompile(`^\d{6}$`)

// decode is TrvProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	var id string
	if strings.HasPrefix(sentence, "TRV") {
		id = kit.Substring(sentence, 0, 3)
	} else {
		id = kit.Substring(sentence, 0, 2)
	}
	typ := kit.Substring(sentence, kit.Len(id), kit.Len(id)+4)

	if m.HasChannel() {
		responseHeader := id + string(rune(kit.CharAt(typ, 0)+1)) + kit.SubstringFrom(typ, 1)
		if typ == "AP00" && id == "IW" {
			now := m.Now.In(m.Zone()).Format("20060102150405")
			m.Send(responseHeader + "," + now + ",0#")
		} else if typ == "AP14" && id != "IW" {
			m.Send(responseHeader + ",0.000,0.000#")
		} else if !ignoreResponse[typ] &&
			!sixDigits.MatchString(kit.SubstringFrom(sentence, kit.Len(responseHeader)+1)) {
			m.Send(responseHeader + "#")
		}
	}

	if typ == "AP00" {
		imei := kit.SubstringFrom(kit.Split(sentence, ",")[0], kit.Len(id)+kit.Len(typ))
		m.Session(imei)
		return nil
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}

	switch typ {
	case "CP01":

		parser := kit.NewParser(patternCP01, sentence)
		if !parser.Matches() {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		m.LastLocation(pos, nil)

		decodeCommon(pos, parser)

		if parser.HasNextN(3) {
			pos.Set(model.KeyBlocked, decodeOptionalValue(parser, 2))
			pos.Set(model.KeyCharge, decodeOptionalValue(parser, 1))
			pos.Set(model.KeyMotion, decodeOptionalValue(parser, 1))
		}

		return pos

	case "AP01", "AP10", "YP03", "YP14":

		parser := kit.NewParser(pattern, sentence)
		if !parser.Matches() {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		dateBuilder := kit.NewDateBuilder(nil).
			SetDate(parser.MustInt(), parser.MustInt(), parser.MustInt())

		pos.Valid = parser.Next() == "A"
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
		pos.Speed = units.KnotsFromKph(parser.MustDouble())

		dateBuilder.SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())
		pos.SetTime(dateBuilder.Date())

		pos.Course = parser.MustDouble()

		decodeCommon(pos, parser)

		network := model.NewNetwork()

		mcc, mnc, lac := parser.MustInt(), parser.MustInt(), parser.MustInt()
		network.AddCellTower(model.NewCellTower(mcc, mnc, lac, int64(parser.MustInt())))

		if parser.HasNext() {
			switch parser.MustInt() {
			case 1:
				pos.AddAlarm(model.AlarmSos)
			case 5, 6:
				pos.AddAlarm(model.AlarmFallDown)
			}
		}

		if parser.HasNext() {
			decodeWifi(network, parser.Next())
		}

		pos.Network = network

		return pos

	case "AP02":

		parser := kit.NewParser(patternAP02, sentence)
		if !parser.Matches() {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		m.LastLocation(pos, nil)

		mcc := parser.MustInt()
		mnc := parser.MustInt()

		network := model.NewNetwork()

		for _, cell := range kit.Split(parser.Next(), ",") {
			if cell != "" {
				values := kit.Split(cell, "|")
				network.AddCellTower(model.NewCellTowerRSSI(
					mcc, mnc, kit.Int(values[0]), int64(kit.Int(values[1])), kit.Int(values[2])))
			}
		}

		decodeWifi(network, parser.Next())

		pos.Network = network

		return pos

	case "AP03":

		parser := kit.NewParser(patternAP03, sentence)
		if !parser.Matches() {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		m.LastLocation(pos, nil)

		pos.Set(model.KeyRssi, parser.NextInt())
		pos.Set(model.KeySatellites, parser.NextInt())
		pos.Set(model.KeyBatteryLevel, parser.NextInt())
		pos.Set(model.KeySteps, parser.NextInt())

		return pos

	case "AP49", "APHT", "APHP", "AP50":

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		m.LastLocation(pos, nil)

		values := kit.Split(sentence, ",")

		switch typ {
		case "AP49":
			pos.Set(model.KeyHeartRate, kit.Int(values[1]))
		case "APHT":
			pos.Set(model.KeyHeartRate, kit.Int(values[1]))
			pos.Set("pressureSystolic", kit.Int(values[2]))
			pos.Set("pressureDiastolic", kit.Int(values[3]))
		case "APHP":
			pos.Set(model.KeyHeartRate, kit.Int(values[1]))
			pos.Set("pressureSystolic", kit.Int(values[2]))
			pos.Set("pressureDiastolic", kit.Int(values[3]))
			pos.Set("spo2", kit.Int(values[4]))
			pos.Set("bloodSugar", kit.Double(values[5]))
			pos.Set("temperature", kit.Double(values[6]))
		case "AP50":
			pos.Set("temperature", kit.Double(values[1]))
			pos.Set(model.KeyBatteryLevel, kit.Int(values[2]))
		}

		return pos

	}

	return nil
}
