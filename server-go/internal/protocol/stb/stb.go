// Package stb is the STB protocol (StbProtocol): JSON messages over TCP, each acknowledged.
package stb

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "stb",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.JSONFramer(),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgProperty = 310
	msgAlarm    = 410
)

func decode(m *ingest.Message, msg any) any {
	root := kit.ParseJSONObject([]byte(msg.(string)))
	typ := root.Int("msgType")
	deviceID := root.String("devId")

	ds := m.Session(deviceID)
	if ds == nil {
		return nil
	}

	m.Send(kit.Format("{ \"msgType\": %d, \"devId\": \"%s\", \"result\": 1, \"txnNo\": \"%s\" }",
		typ+1, deviceID, root.String("txnNo")))

	if typ != msgProperty && typ != msgAlarm {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if typ == msgProperty {
		locationType := 0
		for _, property := range root.Array("attrList") {
			p := kit.JSONObjectOf(property)
			id := p.String("id")
			switch id {
			case "01101001":
				locationType = kit.Int(p.String("value"))
			case "01102001":
				pos.SetLongitude(kit.Double(p.String("value")))
			case "01103001":
				pos.SetLatitude(kit.Double(p.String("value")))
			case "01118001":
				pos.Set(model.KeyDeviceTemp, kit.Double(p.String("value")))
			case "01122001":
				pos.Set("batteryControl", kit.Int(p.String("value")))
			case "02301001":
				pos.Set("switchCabinetCommand", kit.Int(p.String("value")))
			default:
				key := "id" + id
				if p.Has("doorId") {
					key += "Door" + p.String("doorId")
				}
				pos.Set(key, p.String("value"))
			}
		}
		if locationType > 0 {
			pos.SetTime(m.Now)
			pos.Valid = locationType != 5
			if locationType == 2 || locationType == 4 {
				pos.SetLongitude(-pos.Longitude)
			}
			if locationType == 3 || locationType == 4 {
				pos.SetLatitude(-pos.Latitude)
			}
		} else {
			m.LastLocation(pos, nil)
		}
	}
	return pos
}
