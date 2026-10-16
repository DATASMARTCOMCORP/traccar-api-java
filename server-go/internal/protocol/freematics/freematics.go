// Package freematics is the Freematics protocol (FreematicsProtocol): UDP text of
// "id#key:value,...*checksum" data or "id#EV=...,TS=..." events.
package freematics

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "freematics",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Text: true, TextOut: true, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

func decodeEvent(m *ingest.Message, sentence string) any {
	var ds *ingest.DeviceSession
	var event, ts, vin *string

	for _, pair := range kit.Split(sentence, ",") {
		data := kit.Split(pair, "=")
		key, value := data[0], data[1]
		switch key {
		case "ID":
			if ds == nil {
				ds = m.Session(value)
			}
		case "VIN":
			vin = &value
		case "EV":
			event = &value
		case "TS":
			ts = &value
		}
	}

	if ds != nil && event != nil && ts != nil {
		message := "1#EV=" + *event + ",RX=1,TS=" + *ts
		m.Send(message + "*" + kit.SumString(message))
	}

	if ds != nil && vin != nil {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		pos.Set(model.KeyVin, *vin)
		return pos
	}
	return nil
}

// parseHexKey is Integer.parseInt(s, 16); ok is false where it throws
// NumberFormatException.
func parseHexKey(s string) (key int, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, fault := r.(*kit.Fault); !fault {
				panic(r)
			}
			ok = false
		}
	}()
	return kit.IntRadix(s, 16), true
}

// padded is ("000..." + value).substring(value.length()).
func padded(zeros, value string) string {
	return kit.SubstringFrom(zeros+value, kit.Len(value))
}

func decodePosition(m *ingest.Message, sentence, id string) any {
	ds := m.Session(id)
	if ds == nil {
		return nil
	}

	positions := []*model.Position{}
	var pos *model.Position
	var dateBuilder *kit.DateBuilder

	for _, pair := range kit.Split(sentence, ",") {
		data := kit.SplitRegex(pair, "[=:]")
		key, ok := parseHexKey(data[0])
		if !ok {
			continue
		}
		value := data[1]
		if key == 0x0 {
			if pos != nil {
				pos.SetTime(dateBuilder.Date())
				positions = append(positions, pos)
			}
			pos = m.NewPosition()
			pos.DeviceID = ds.DeviceID
			dateBuilder = kit.NewDateBuilderAt(m.Now, nil)
		} else if pos != nil {
			switch key {
			case 0x11:
				value = padded("000000", value)
				dateBuilder.SetDateReverse(
					kit.Int(kit.Substring(value, 0, 2)),
					kit.Int(kit.Substring(value, 2, 4)),
					kit.Int(kit.SubstringFrom(value, 4)))
			case 0x10:
				value = padded("00000000", value)
				dateBuilder.SetTimeMillis(
					kit.Int(kit.Substring(value, 0, 2)),
					kit.Int(kit.Substring(value, 2, 4)),
					kit.Int(kit.Substring(value, 4, 6)),
					kit.Int(kit.SubstringFrom(value, 6))*10)
			case 0xA:
				pos.Valid = true
				pos.SetLatitude(kit.Double(value))
			case 0xB:
				pos.Valid = true
				pos.SetLongitude(kit.Double(value))
			case 0xC:
				pos.Altitude = kit.Double(value)
			case 0xD:
				pos.Speed = units.KnotsFromKph(kit.Double(value))
			case 0xE:
				pos.Course = float64(kit.Int(value))
			case 0xF:
				pos.Set(model.KeySatellites, kit.Int(value))
			case 0x12:
				pos.Set(model.KeyHdop, kit.Int(value))
			case 0x20:
				pos.Set(model.KeyAcceleration, value)
			case 0x24:
				pos.Set(model.KeyBattery, float64(kit.Int(value))/100.0)
			case 0x81:
				pos.Set(model.KeyRssi, kit.Int(value))
			case 0x82:
				pos.Set(model.KeyDeviceTemp, kit.Double(value)/10.0)
			case 0x104:
				pos.Set(model.KeyEngineLoad, kit.Int(value))
			case 0x105:
				pos.Set(model.KeyCoolantTemp, kit.Int(value))
			case 0x10c:
				pos.Set(model.KeyRpm, kit.Int(value))
			case 0x10d:
				pos.Set(model.KeyObdSpeed, kit.Int(value))
			case 0x111:
				pos.Set(model.KeyThrottle, kit.Int(value))
			default:
				pos.Set(model.PrefixIo+strconv.Itoa(key), value)
			}
		}
	}

	if pos != nil {
		if !pos.Valid {
			m.LastLocation(pos, nil)
		}
		pos.SetTime(dateBuilder.Date())
		positions = append(positions, pos)
	}

	if len(positions) == 0 {
		return nil
	}
	return positions
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)
	start := kit.IndexOf(sentence, "#")
	end := kit.IndexOf(sentence, "*")
	if start > 0 && end > 0 {
		id := kit.Substring(sentence, 0, start)
		sentence = kit.Substring(sentence, start+1, end)
		if len(sentence) >= 2 && sentence[:2] == "EV" {
			return decodeEvent(m, sentence)
		}
		return decodePosition(m, sentence, id)
	}
	return nil
}
