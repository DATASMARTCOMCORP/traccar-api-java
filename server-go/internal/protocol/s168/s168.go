// Package s168 is the S168 protocol (S168Protocol): '$'-delimited text messages with
// GDATA, CELL, WIFI and STATUS fragments.
package s168

import (
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "s168",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "$"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var dateFormat = kit.NewDateFormat("yyMMddHHmmss", time.UTC)

func decode(m *ingest.Message, msg any) any {
	values := kit.Split(msg.(string), "#")

	ds := m.Session(values[1])
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	network := model.NewNetwork()

	content := values[4]
	for _, fragment := range kit.Split(content, ";") {
		if fragment == "" {
			continue
		}
		dataIndex := kit.IndexOf(fragment, ":")
		typ := kit.Substring(fragment, 0, dataIndex)
		values = kit.Split(kit.SubstringFrom(fragment, dataIndex+1), ",")
		index := 0
		next := func() string {
			v := values[index]
			index++
			return v
		}
		switch typ {
		case "GDATA":
			pos.Valid = next() == "A"
			pos.Set(model.KeySatellites, kit.Int(next()))
			pos.SetTime(dateFormat.Parse(next()))
			pos.SetLatitude(kit.Double(next()))
			pos.SetLongitude(kit.Double(next()))
			pos.Speed = units.KnotsFromKph(kit.Double(next()))
			pos.Course = float64(kit.Int(next()))
			pos.Altitude = float64(kit.Int(next()))
		case "CELL":
			cellCount := kit.Int(next())
			mcc := kit.IntRadix(next(), 16)
			mnc := kit.IntRadix(next(), 16)
			for range cellCount {
				lac := kit.IntRadix(next(), 16)
				cid := kit.IntRadix(next(), 16)
				rssi := kit.IntRadix(next(), 16)
				network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
			}
		case "WIFI":
			wifiCount := kit.Int(next())
			for range wifiCount {
				mac := strings.ReplaceAll(next(), "-", ":")
				network.AddWifiAccessPoint(model.NewWifiAccessPoint(mac, kit.Int(next())))
			}
		case "STATUS":
			pos.Set(model.KeyBatteryLevel, kit.Int(next()))
			pos.Set(model.KeyRssi, kit.Int(next()))
		}
	}

	if network.CellTowers != nil || network.WifiAccessPoints != nil {
		pos.Network = network
	}

	if !pos.Attributes.Has(model.KeySatellites) {
		m.LastLocation(pos, nil)
	}

	if pos.Network != nil || pos.Attributes.Len() > 0 {
		return pos
	}
	return nil
}
