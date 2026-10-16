// Package wristband is the Wristband protocol (WristbandProtocol): "YX" text messages in a
// binary envelope over TCP.
package wristband

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "wristband",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 3, 2, 3, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

func sendResponse(m *ingest.Message, imei, version string, typ int, data string) {
	if !m.HasChannel() {
		return
	}
	sentence := kit.Format("YX%s|%s|0|{F%02d#%s}\r\n", imei, version, typ, data)
	response := kit.Empty()
	response.WriteBytes([]byte{0x00, 0x01, 0x02})
	response.WriteShort(kit.Len(sentence))
	response.WriteASCII(sentence)
	response.WriteBytes([]byte{0xFF, 0xFE, 0xFC})
	m.Send(response)
}

var pattern = kit.NewPattern().
	Expression("..").       // header
	Number("(d+)|").        // imei
	Number("([vV]d+.d+)|"). // version
	Number("d+|").          // model
	Text("{").
	Number("F(d+)"). // function
	GroupBegin().
	Text("#").
	Expression("(.*)"). // data
	GroupEndWith("?").
	Text("}").
	Text("\r\n").
	Compile()

func decodePosition(m *ingest.Message, ds *ingest.DeviceSession, sentence string) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	values := kit.Split(sentence, ",")

	pos.Valid = true
	pos.SetLongitude(kit.Double(values[0]))
	pos.SetLatitude(kit.Double(values[1]))
	pos.SetTime(kit.NewDateFormat("yyyyMMddHHmm", m.Zone()).Parse(values[2]))
	pos.Speed = units.KnotsFromKph(kit.Double(values[3]))

	return pos
}

func decodeStatus(m *ingest.Message, ds *ingest.DeviceSession, sentence string) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	m.LastLocation(pos, nil)

	pos.Set(model.KeyBatteryLevel, kit.Int(kit.Split(sentence, ",")[0]))

	return pos
}

func decodeNetwork(m *ingest.Message, ds *ingest.DeviceSession, sentence string, wifi bool) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	m.LastLocation(pos, nil)

	network := model.NewNetwork()
	fragments := kit.Split(sentence, "|")

	cells := fragments[0]
	if wifi {
		for _, item := range kit.Split(fragments[0], "_") {
			values := kit.Split(item, ",")
			network.AddWifiAccessPoint(model.NewWifiAccessPoint(values[0], kit.Int(values[1])))
		}
		cells = fragments[1]
	}

	for _, item := range kit.Split(cells, ":") {
		values := kit.Split(item, ",")
		lac := kit.Int(values[0])
		mnc := kit.Int(values[1])
		mcc := kit.Int(values[2])
		cid := kit.Int(values[3])
		rssi := kit.Int(values[4])
		network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
	}

	pos.Network = network

	return pos
}

// must is Java's dereference of a String that may be null.
func must(s *string) string {
	if s == nil {
		kit.Throw("NullPointerException")
	}
	return *s
}

func decodeMessage(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	imei := parser.Next()
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	version := parser.Next()
	typ := parser.MustInt()

	positions := []*model.Position{}
	data := parser.NextNullable()

	switch typ {
	case 90:
		sendResponse(m, imei, version, typ, m.Server(','))
	case 91:
		now := m.Now.In(m.Zone()).Format("2006-01-02-15-04-05")
		sendResponse(m, imei, version, typ, now+"|"+m.Server(','))
	case 1:
		positions = append(positions, decodeStatus(m, ds, must(data)))
		sendResponse(m, imei, version, typ, kit.Split(*data, ",")[1])
	case 2:
		for _, fragment := range kit.Split(must(data), "|") {
			positions = append(positions, decodePosition(m, ds, fragment))
		}
	case 3, 4:
		positions = append(positions, decodeNetwork(m, ds, must(data), typ == 3))
	case 64:
		text := "null"
		if data != nil {
			text = *data
		}
		sendResponse(m, imei, version, typ, text)
	}

	if len(positions) == 0 {
		return nil
	}
	return positions
}

// decode is WristbandProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.Skip(3)             // header
	buf.ReadUnsignedShort() // length

	sentence := buf.GetString(buf.ReaderIndex(), buf.ReadableBytes()-3)

	buf.Skip(3) // footer

	return decodeMessage(m, sentence)
}
