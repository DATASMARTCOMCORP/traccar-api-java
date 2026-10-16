// Package wialon is the Wialon IPS protocol (WialonProtocol): "#type#data" text lines over
// TCP and UDP.
package wialon

import (
	"regexp"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: a TCP server of lines and a UDP server. Text is
// UTF-8 either way (wialon.utf8 picks the JVM default charset otherwise, UTF-8 too).
func Protocol() ingest.Protocol {
	pipeline := func(framer ingest.Framer) ingest.Pipeline {
		return ingest.Pipeline{
			Framer:  framer,
			Text:    true,
			TextOut: true,
			Decoder: ingest.DecoderFunc(decode),
			Encoder: ingest.CommandEncoderFunc(encode),
		}
	}
	return ingest.Protocol{
		Name:         "wialon",
		DataCommands: protocol.DataCommands("wialon"),
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline { return pipeline(ingest.NewLineFramer(ingest.MaxFrameLengthLarge)) }},
			{Datagram: true, NewPipeline: func() ingest.Pipeline { return pipeline(nil) }},
		},
	}
}

var patternAny = kit.NewPattern().
	Number("d.d;").Optional().
	Expression("([^#]+)?"). // imei
	Text("#").              // start byte
	Expression("([^#]+)").  // type
	Text("#").              // separator
	Expression("(.*)").     // message
	Compile()

var patternParam = regexp.MustCompile(`^(.*):[1-3]:(.*)$`)

var pattern = kit.NewPattern().
	Number("(?:NA|(dd)(dd)(dd));"). // date (ddmmyy)
	Number("(?:NA|(dd)(dd)(dd));"). // time (hhmmss)
	Number("(?:NA|(d+)(dd.d+));").  // latitude
	Expression("(?:NA|([NS]));").
	Number("(?:NA|(d+)(dd.d+));"). // longitude
	Expression("(?:NA|([EW]));").
	Number("(?:NA|(d+.?d*))?;").  // speed
	Number("(?:NA|(d+.?d*))?;").  // course
	Number("(?:NA|(-?d+.?d*));"). // altitude
	Number("(?:NA|(d+))").        // satellites
	GroupBegin().Text(";").
	Number("(?:NA|(d+.?d*));").    // hdop
	Number("(?:NA|(d+));").        // inputs
	Number("(?:NA|(d+));").        // outputs
	Expression("(?:NA|([^;]*));"). // adc
	Expression("(?:NA|([^;]*));"). // ibutton
	Expression("(?:NA|([^;]*))").  // params
	GroupEndWith("?").
	Any().
	Compile()

func sendResponse(m *ingest.Message, typ string, number *int) {
	if !m.HasChannel() {
		return
	}
	response := "#A" + typ + "#"
	if number != nil {
		response += kit.Format("%d", *number)
	}
	m.Send(response + "\r\n")
}

func one() *int { v := 1; return &v }

// ids is the unique ids without Java's nulls (getDeviceSession filters them).
func ids(id *string) []string {
	if id == nil {
		return nil
	}
	return []string{*id}
}

func decodePosition(m *ingest.Message, id *string, substring string) *model.Position {
	ds := m.Session(ids(id)...)
	if ds == nil {
		return nil
	}

	parser := kit.NewParser(pattern, substring)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if parser.HasNextN(6) {
		pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	} else {
		pos.SetTime(m.Now)
	}

	if parser.HasNextAny(9) {
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
		pos.Speed = units.KnotsFromKph(parser.NextDoubleDefault(0))
		pos.Course = parser.NextDoubleDefault(0)
		pos.Altitude = parser.NextDoubleDefault(0)
	} else {
		deviceTime := pos.DeviceTime.Time
		m.LastLocation(pos, &deviceTime)
	}

	if parser.HasNext() {
		satellites := parser.NextIntDefault(0)
		pos.Valid = satellites >= 3
		pos.Set(model.KeySatellites, satellites)
	}

	pos.Set(model.KeyHdop, parser.NextDouble())
	pos.Set(model.KeyInput, parser.NextNullable())
	pos.Set(model.KeyOutput, parser.NextNullable())

	if parser.HasNext() {
		for i, value := range kit.Split(parser.Next(), ",") {
			pos.Set(model.PrefixAdc+kit.Format("%d", i+1), value)
		}
	}

	pos.Set(model.KeyDriverUniqueID, parser.NextNullable())

	if parser.HasNext() {
		for _, param := range kit.Split(parser.Next(), ",") {
			match := patternParam.FindStringSubmatch(param)
			if match == nil {
				continue
			}
			key := strings.ToLower(match[1])
			value := match[2]
			if v, err := model.ParseDouble(value); err == nil {
				pos.Set(key, v)
			} else if strings.EqualFold(value, "true") {
				pos.Set(key, true)
			} else if strings.EqualFold(value, "false") {
				pos.Set(key, false)
			} else {
				pos.Set(key, value)
			}
		}

		if pos.Attributes.Has("accuracy") {
			accuracy, err := pos.Attributes.JavaDouble("accuracy", 0)
			if err != nil {
				panic(err)
			}
			pos.Attributes.Delete("accuracy")
			pos.Accuracy = accuracy
		}

		if pos.Attributes.Has("bat") {
			pos.Set(model.KeyBatteryLevel, removeInteger(pos, "bat"))
		}

		if pos.Attributes.Has("temp") {
			pos.Set(model.KeyDeviceTemp, removeInteger(pos, "temp"))
		}

		network := model.NewNetwork()
		decodeCellData(pos, network, "")
		for i := 1; i <= 9; i++ {
			if !decodeCellData(pos, network, kit.Format("%d", i)) {
				break
			}
		}

		if network.CellTowers != nil {
			pos.Network = network
		}
	}

	return pos
}

// removeInteger is ExtendedModel.removeInteger for a present key.
func removeInteger(pos *model.Position, key string) int {
	v, err := pos.Attributes.JavaInt(key)
	if err != nil {
		panic(err)
	}
	pos.Attributes.Delete(key)
	return v
}

func decodeCellData(pos *model.Position, network *model.Network, suffix string) bool {
	a := pos.Attributes
	if !a.Has("mnc"+suffix) || !a.Has("mcc"+suffix) || !a.Has("lac"+suffix) || !a.Has("cell_id"+suffix) {
		return false
	}
	mcc := removeInteger(pos, "mcc"+suffix)
	mnc := removeInteger(pos, "mnc"+suffix)
	lac := removeInteger(pos, "lac"+suffix)
	cid, err := a.JavaLong("cell_id" + suffix)
	if err != nil {
		panic(err)
	}
	network.AddCellTower(model.NewCellTower(mcc, mnc, lac, cid))
	return true
}

// decode is WialonProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(patternAny, msg.(string))
	if !parser.Matches() {
		return nil
	}

	id := parser.NextNullable()
	typ := parser.Next()
	data := parser.Next()

	switch typ {

	case "L":
		values := kit.Split(data, ";")
		imei := values[0]
		if strings.Contains(values[0], ".") {
			imei = values[1]
		}
		if m.Session(imei) != nil {
			sendResponse(m, typ, one())
		}

	case "P":
		sendResponse(m, typ, nil) // heartbeat

	case "D", "SD":
		if pos := decodePosition(m, id, data); pos != nil {
			sendResponse(m, "D", one())
			return pos
		}

	case "B":
		messages := kit.Split(data, "|")
		positions := []*model.Position{}

		for _, message := range messages {
			if pos := decodePosition(m, id, message); pos != nil {
				pos.Set(model.KeyArchive, true)
				positions = append(positions, pos)
			}
		}

		count := len(messages)
		sendResponse(m, typ, &count)
		if len(positions) > 0 {
			return positions
		}

	case "M":
		if ds := m.Session(ids(id)...); ds != nil {
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID
			now := m.Now
			m.LastLocation(pos, &now)
			pos.Valid = false
			pos.Set(model.KeyResult, data)
			sendResponse(m, typ, one())
			return pos
		}

	}

	return nil
}

// encode is WialonProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	format := func(f string, keys ...string) string {
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), f, keys...)
	}
	switch c.Type {
	case model.CommandRebootDevice:
		return format("reboot\r\n")
	case model.CommandSendUssd:
		return format("USSD:%s\r\n", model.CommandKeyPhone)
	case model.CommandIdentification:
		return format("VER?\r\n")
	case model.CommandOutputControl:
		return format("L%s=%s\r\n", model.CommandKeyIndex, model.CommandKeyData)
	}
	return nil
}
