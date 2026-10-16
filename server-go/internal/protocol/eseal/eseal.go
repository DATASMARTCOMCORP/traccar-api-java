// Package eseal is the eSeal protocol (EsealProtocol): "##S,...,E##" text lines over TCP.
package eseal

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server. eseal.config is the
// configuration sent back on startup messages.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	var deviceConfig *string
	if v, ok := cfg.Lookup("eseal.config"); ok {
		deviceConfig = &v
	}
	return ingest.Protocol{
		Name:         "eseal",
		DataCommands: protocol.DataCommands("eseal"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: &Decoder{Config: deviceConfig},
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}, nil
}

// Decoder is EsealProtocolDecoder. Config is <protocol>.config (nil when unset, sent as
// "null" as Java concatenates it).
type Decoder struct {
	Config *string
}

var pattern = kit.NewPattern().
	Text("##S,").
	Expression("[^,]+,").        // device type
	Number("(d+),").             // device id
	Number("d+,").               // customer id
	Expression("[^,]+,").        // firmware version
	Expression("([^,]+),").      // type
	Number("(d+),").             // index
	Number("(dddd)-(dd)-(dd),"). // date
	Number("(dd):(dd):(dd),").   // time
	Number("d+,").               // interval
	Expression("([AV]),").       // validity
	Number("(d+.d+)([NS]) ").    // latitude
	Number("(d+.d+)([EW]),").    // longitude
	Number("(d+),").             // course
	Number("(d+),").             // speed
	Expression("([^,]+),").      // door
	Number("(d+.d+),").          // acceleration
	Expression("([^,]+),").      // nfc
	Number("(d+.d+),").          // battery
	Number("(-?d+),").           // rssi
	Text("E##").
	Compile()

func decodeAlarm(typ string) string {
	switch typ {
	case "Event-Door":
		return model.AlarmDoor
	case "Event-Shock":
		return model.AlarmVibration
	case "Event-Drop":
		return model.AlarmFallDown
	case "Event-Lock":
		return model.AlarmLock
	case "Event-RC-Unlock":
		return model.AlarmUnlock
	}
	return ""
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	typ := parser.Next()
	prefix := kit.Substring(sentence, 0, kit.IndexOf(sentence, typ))
	index := parser.MustInt()

	pos.Set(model.KeyIndex, index)
	pos.AddAlarm(decodeAlarm(typ))

	sendResponse := func(payload string) {
		m.Send(prefix + typ + " ACK," + payload + ",E##\r\n")
	}
	switch typ {
	case "Startup":
		config := "null"
		if d.Config != nil {
			config = *d.Config
		}
		sendResponse(strconv.Itoa(index) + "," + config)
	case "Normal", "Button-Normal", "Termination", "Event-Door", "Event-Shock",
		"Event-Drop", "Event-Lock", "Event-RC-Unlock":
		sendResponse(strconv.Itoa(index))
	}

	pos.SetTime(parser.NextDateTimeDefault())
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))
	pos.Course = float64(parser.MustInt())
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))

	switch parser.Next() {
	case "Open":
		pos.Set(model.KeyDoor, true)
	case "Close":
		pos.Set(model.KeyDoor, false)
	}

	pos.Set(model.KeyAcceleration, parser.NextDouble())
	pos.Set("nfc", parser.Next())
	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyRssi, parser.NextInt())
	return pos
}

// encode is EsealProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	id := m.UniqueID(c.DeviceID)
	switch c.Type {
	case model.CommandCustom:
		return kit.FormatCommand(c, id, "##S,eSeal,%s,256,3.0.8,%s,E##", model.CommandKeyUniqueID, model.CommandKeyData)
	case model.CommandAlarmArm:
		return kit.FormatCommand(c, id, "##S,eSeal,%s,256,3.0.8,RC-Power Control,Power OFF,E##", model.CommandKeyUniqueID)
	case model.CommandAlarmDisarm:
		return kit.FormatCommand(c, id, "##S,eSeal,%s,256,3.0.8,RC-Unlock,E##", model.CommandKeyUniqueID)
	}
	return nil
}
