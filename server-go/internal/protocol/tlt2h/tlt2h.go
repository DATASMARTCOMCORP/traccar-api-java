// Package tlt2h is the TLT-2H protocol (Tlt2hProtocol): a "#imei#..." header line
// followed by $GPRMC or $WIFI records, frames ending with "##\r\n".
package tlt2h

import (
	"regexp"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: a TCP and a UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "tlt2h",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLengthLarge, "##\r\n"),
					Text:    true,
					TextOut: true,
					Decoder: ingest.DecoderFunc(decode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Text: true, TextOut: true, Decoder: ingest.DecoderFunc(decode)}
			}},
		},
	}
}

var patternHeader = kit.NewPattern().
	Number("#(d+)").      // imei
	Expression("#[^#]*"). // user
	Number("#d*").        // password
	GroupBegin().
	Number("#([01])"). // door
	Number("#(d+)").   // fuel voltage
	Number("#(d+)").   // power
	Number("#(d+)").   // battery
	Number("#(d+)").   // temperature
	GroupEndWith("?").
	Expression("#([^#]+)"). // status
	Number("#d+").          // number of records
	Compile()

var patternPosition = kit.NewPattern().
	Text("#").
	Number("(?:(dd|dddd)|x*)"). // cell or voltage
	GroupBegin().
	Text("#").
	GroupBegin().
	Number("(d+),"). // mcc
	Number("(d+),"). // mnc
	Number("(x+),"). // lac
	Number("(x+)").  // cell id
	GroupEndWith("?").
	GroupEndWith("?").
	Text("$GPRMC,").
	Number("(?:(dd)(dd)(dd).d+)?,"). // time (hhmmss.sss)
	Expression("([AVL]),").          // validity
	GroupBegin().
	Number("(d+)(dd.d+),"). // latitude
	Expression("([NS]),").
	Number("(d+)(dd.d+),"). // longitude
	Number("([EW]),").
	Number("(d+.?d*)?,").   // speed
	Number("(d+.?d*)?,").   // course
	Number("(dd)(dd)(dd)"). // date (ddmmyy)
	GroupEndWith("?").
	Any().
	Compile()

var patternWifi = kit.NewPattern().
	Text("#").
	Number("(?:(dd|dddd)|x+)"). // cell or voltage
	Expression("#?").
	GroupBegin().
	Number("(d+),"). // mcc
	Number("(d+),"). // mnc
	Number("(x+),"). // lac
	Number("(x+)").  // cell id
	GroupEndWith("?").
	Text("$WIFI,").
	Number("(dd)(dd)(dd).d+,"). // time (hhmmss.sss)
	Expression("[AVL],").       // validity
	Expression("(.*)").         // access points
	Number("(dd)(dd)(dd)").     // date (ddmmyy)
	Text("*").
	Number("xx"). // checksum
	Compile()

func decodeStatus(pos *model.Position, status string) {
	switch status {
	case "AUTOSTART", "AUTO":
		pos.Set(model.KeyIgnition, true)
	case "AUTOSTOP", "AUTOLOW":
		pos.Set(model.KeyIgnition, false)
	case "TOWED":
		pos.AddAlarm(model.AlarmTow)
	case "SHAKE":
		pos.AddAlarm(model.AlarmVibration)
	case "SOS":
		pos.AddAlarm(model.AlarmSos)
	case "DEF":
		pos.AddAlarm(model.AlarmPowerCut)
	case "BLP":
		pos.AddAlarm(model.AlarmLowBattery)
	case "CLP":
		pos.AddAlarm(model.AlarmLowPower)
	case "OS":
		pos.AddAlarm(model.AlarmGeofenceExit)
	case "RS":
		pos.AddAlarm(model.AlarmGeofenceEnter)
	case "OVERSPEED":
		pos.AddAlarm(model.AlarmOverspeed)
	}
}

func voltage(v int) float64 {
	if v > 100 {
		return float64(v) / 1000.0
	}
	return float64(v) / 10.0
}

var pairs = regexp.MustCompile(`(..)`)

func decode(m *ingest.Message, msg any) any {
	sentence := kit.Trim(msg.(string))

	header := kit.Substring(sentence, 0, kit.IndexOf(sentence, "\r"))
	parser := kit.NewParser(patternHeader, header)
	if !parser.Matches() {
		return nil
	}

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	var door *bool
	var adc, power, battery, temperature *float64
	if parser.HasNextN(5) {
		d := parser.MustInt() == 1
		a := float64(parser.MustInt()) / 10.0
		p := float64(parser.MustInt()) / 10.0
		b := float64(parser.MustInt()) / 10.0
		t := float64(parser.MustInt()) / 10.0
		door, adc, power, battery, temperature = &d, &a, &p, &b, &t
	}

	status := parser.Next()

	end := kit.Len(sentence)
	if strings.HasSuffix(sentence, "##") {
		end -= 4
	}
	messages := kit.Split(kit.Substring(sentence, kit.IndexOf(sentence, "\n")+1, end), "\r\n")
	positions := []*model.Position{}

	for _, message := range messages {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		switch {
		case strings.Contains(message, "$GPRMC"):
			parser = kit.NewParser(patternPosition, message)
			if !parser.Matches() {
				continue
			}
			if parser.HasNext() {
				pos.Set(model.KeyBattery, voltage(parser.MustInt()))
			}
			if parser.HasNextN(4) {
				mcc, mnc := parser.MustInt(), parser.MustInt()
				lac, cid := parser.MustHexInt(), parser.MustHexInt()
				pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))
			}

			db := kit.NewDateBuilder(nil)
			if parser.HasNextN(3) {
				db.SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())
			}

			pos.Valid = parser.Next() == "A"

			if parser.HasNext() {
				pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
				pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
				pos.Speed = parser.NextDoubleDefault(0)
				pos.Course = parser.NextDoubleDefault(0)
				db.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
				pos.SetTime(db.Date())
			} else {
				m.LastLocation(pos, nil)
			}

		case strings.Contains(message, "$WIFI"):
			parser = kit.NewParser(patternWifi, message)
			if !parser.Matches() {
				continue
			}
			if parser.HasNext() {
				pos.Set(model.KeyBattery, voltage(parser.MustInt()))
			}

			network := model.NewNetwork()
			if parser.HasNextN(4) {
				mcc, mnc := parser.MustInt(), parser.MustInt()
				lac, cid := parser.MustHexInt(), parser.MustHexInt()
				network.AddCellTower(model.NewCellTower(mcc, mnc, lac, int64(cid)))
			}

			db := kit.NewDateBuilder(nil).SetTime(parser.MustInt(), parser.MustInt(), parser.MustInt())

			values := kit.Split(parser.Next(), ",")
			for i := 0; i < len(values)/2; i++ {
				mac := kit.Substring(pairs.ReplaceAllString(values[i*2+1], "$1:"), 0, 17)
				network.AddWifiAccessPoint(model.NewWifiAccessPoint(mac, kit.Int(values[i*2])))
			}
			pos.Network = network

			db.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
			t := db.Date()
			m.LastLocation(pos, &t)

		default:
			m.LastLocation(pos, nil)
		}

		pos.Set(model.KeyDoor, door)
		pos.Set(model.PrefixAdc+"1", adc)
		pos.Set(model.KeyPower, power)
		pos.Set(model.KeyBattery, battery)
		pos.Set(model.PrefixTemp+"1", temperature)
		decodeStatus(pos, status)

		positions = append(positions, pos)
	}

	if len(positions) == 0 {
		return nil
	}
	return positions
}
