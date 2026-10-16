// Package pt60 is the PT60 protocol (Pt60Protocol): "@G#@" and "@B#@" text messages over
// TCP.
package pt60

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "pt60",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "@R#@", "@E#@"),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgGTrack     = 6
	msgGStepCount = 13
	msgGHeartRate = 14
	msgBPosition  = 1
)

var pattern = kit.NewPattern().
	Expression("@(.)#@[,|]"). // header
	Number("V?dd[,|]").       // protocol version
	Number("(d+)[,|]").       // type
	Number("(d+)[,|]").       // imei
	Number("d+[,|]").         // imsi
	GroupBegin().
	Expression("[^,|]+[,|]").Optional(). // firmware version
	Number("[01][,|]").                  // state
	Number("d+[,|]").                    // battery
	GroupEndWith("?").
	Number("(dddd)(dd)(dd)").   // date (yyyymmdd)
	Number("(dd)(dd)(dd)[,|]"). // time (hhmmss)
	Expression("(.*)").         // data
	Expression("[,|]").
	Compile()

func sendResponse(m *ingest.Message, format string, typ int, imei string) {
	if !m.HasChannel() {
		return
	}
	t := m.Now.In(m.Zone()).Format("20060102150405")
	if format == "G" {
		m.Send(kit.Format("@G#@,V01,38,%s,@R#@", t))
	} else {
		m.Send(kit.Format("@B#@|01|%03d|%s|0|%s|@E#@", typ+1, imei, t))
	}
}

func decode(m *ingest.Message, msg any) any {
	parser := kit.NewParser(pattern, msg.(string))
	if !parser.Matches() {
		return nil
	}
	format := parser.Next()
	typ := parser.MustInt()
	imei := parser.Next()
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}
	sendResponse(m, format, typ, imei)

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	if format == "G" {
		if typ != msgGTrack && typ != msgGStepCount && typ != msgGHeartRate {
			return nil
		}
		deviceTime := parser.NextDateTimeDefault()
		pos.DeviceTime = model.NewTime(deviceTime)
		values := kit.Split(parser.Next(), ",")
		if typ == msgGTrack {
			pos.Valid = true
			pos.FixTime = pos.DeviceTime
			coordinates := kit.Split(values[0], ";")
			pos.SetLatitude(kit.Double(coordinates[0]))
			pos.SetLongitude(kit.Double(coordinates[1]))
		} else {
			m.LastLocation(pos, &deviceTime)
			switch typ {
			case msgGStepCount:
				pos.Set(model.KeySteps, kit.Int(values[0]))
			case msgGHeartRate:
				pos.Set(model.KeyHeartRate, kit.Int(values[0]))
				pos.Set(model.KeyBattery, kit.Int(values[1]))
			}
		}
		return pos
	}

	if typ != msgBPosition {
		return nil
	}
	deviceTime := parser.NextDateTimeDefault()
	pos.DeviceTime = model.NewTime(deviceTime)
	values := kit.Split(parser.Next(), "|")
	if kit.Int(values[len(values)-1]) == 2 {
		m.LastLocation(pos, &deviceTime)
		network := model.NewNetwork()
		for _, v := range values[:len(values)-1] {
			cell := kit.Split(v, ",")
			cid := kit.Long(cell[0])
			lac, mnc, mcc := int64(kit.Int(cell[1])), int64(kit.Int(cell[2])), int64(kit.Int(cell[3]))
			tower := model.CellTower{CellID: &cid, LocationAreaCode: &lac, MobileNetworkCode: &mnc, MobileCountryCode: &mcc}
			tower.SetSignalStrength(kit.Int(cell[4]))
			network.AddCellTower(tower)
		}
		pos.Network = network
	} else {
		pos.Valid = true
		pos.FixTime = pos.DeviceTime
		pos.SetLatitude(kit.Double(values[0]))
		pos.SetLongitude(kit.Double(values[1]))
	}
	return pos
}
