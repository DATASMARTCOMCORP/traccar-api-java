// Package b2316 is the B2316 protocol (B2316Protocol): JSON reports of wearables over UDP.
package b2316

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "b2316",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Text: true, TextOut: true, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

func decodeAlarm(value int) string {
	switch value {
	case 1:
		return model.AlarmLowBattery
	case 2:
		return model.AlarmSos
	case 3:
		return model.AlarmPowerOff
	case 4:
		return model.AlarmRemoving
	}
	return ""
}

func decodeBattery(value int) *int {
	var v int
	switch value {
	case 0:
		v = 10
	case 1:
		v = 30
	case 2:
		v = 60
	case 3:
		v = 80
	case 4:
		v = 100
	default:
		return nil
	}
	return &v
}

// object is a parsed JSON object; its accessors throw as Jakarta JSON's do.
type object map[string]any

func (o object) str(key string) string {
	s, ok := o[key].(string)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	return s
}

func (o object) number(key string) string {
	n, ok := o[key].(json.Number)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	return string(n)
}

// integer is getInt: JsonNumber.intValue of an integral number.
func (o object) integer(key string) int { return int(int32(kit.Long(o.number(key)))) }

func (o object) has(key string) bool {
	_, ok := o[key]
	return ok
}

func decode(m *ingest.Message, msg any) any {
	dec := json.NewDecoder(strings.NewReader(msg.(string)))
	dec.UseNumber()
	var root object
	if err := dec.Decode(&root); err != nil {
		kit.Throw("JsonParsingException: %v", err)
	}
	ds := m.Session(root.str("imei"))
	if ds == nil {
		return nil
	}

	data, ok := root["data"].([]any)
	if !ok {
		kit.Throw("NullPointerException: data")
	}
	var positions []*model.Position
	for _, v := range data {
		it := object(v.(map[string]any))
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		network := model.NewNetwork()

		t := time.UnixMilli(kit.Long(it.number("tm")) * 1000)
		if it.has("gp") {
			coordinates := kit.Split(it.str("gp"), ",")
			pos.SetLongitude(kit.Double(coordinates[0]))
			pos.SetLatitude(kit.Double(coordinates[1]))
			pos.Valid = true
			pos.SetTime(t)
		} else {
			m.LastLocation(pos, &t)
		}

		if it.has("ci") {
			cell := kit.Split(it.str("ci"), ",")
			network.AddCellTower(model.NewCellTowerRSSI(
				kit.Int(cell[0]), kit.Int(cell[1]), kit.Int(cell[2]), int64(kit.Int(cell[3])), kit.Int(cell[4])))
		}
		if it.has("wi") {
			for _, point := range kit.Split(it.str("wi"), ";") {
				values := kit.Split(point, ",")
				mac := pairsWithColons(values[0])
				network.AddWifiAccessPoint(model.NewWifiAccessPoint(
					kit.Substring(mac, 0, kit.Len(mac)-1), kit.Int(values[1])))
			}
		}

		if it.has("wn") {
			pos.AddAlarm(decodeAlarm(it.integer("wn")))
		}
		if it.has("ic") {
			pos.Set(model.KeyIccid, it.str("ic"))
		}
		if it.has("ve") {
			pos.Set(model.KeyVersionFw, it.str("ve"))
		}
		if it.has("te") {
			for j, temperature := range kit.Split(it.str("te"), ",") {
				pos.Set(model.PrefixTemp+kit.Format("%d", j+1), float64(kit.Int(temperature))/10.0)
			}
		}
		if it.has("st") {
			pos.Set(model.KeySteps, it.integer("st"))
		}
		if it.has("ba") {
			pos.Set(model.KeyBatteryLevel, decodeBattery(it.integer("ba")))
		}
		if it.has("sn") {
			pos.Set(model.KeyRssi, it.integer("sn"))
		}
		if it.has("hr") {
			pos.Set(model.KeyHeartRate, it.integer("hr"))
		}

		if network.CellTowers != nil || network.WifiAccessPoints != nil {
			pos.Network = network
		}
		positions = append(positions, pos)
	}
	if len(positions) == 0 {
		return nil
	}
	return positions
}

// pairsWithColons is replaceAll("(..)", "$1:"): a colon after every two characters.
func pairsWithColons(s string) string {
	r := []rune(s)
	var b strings.Builder
	i := 0
	for ; i+1 < len(r); i += 2 {
		b.WriteString(string(r[i : i+2]))
		b.WriteByte(':')
	}
	b.WriteString(string(r[i:]))
	return b.String()
}
