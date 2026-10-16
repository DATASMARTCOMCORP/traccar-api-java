// Package smokey is the Smokey protocol (SmokeyProtocol): binary reports with cells and
// Wi-Fi over UDP.
package smokey

import (
	"encoding/hex"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "smokey",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgDateRecord    = 0
	msgDateRecordAck = 1
)

var epoch2000 = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func sendResponse(m *ingest.Message, id []byte, index, report int) {
	if !m.HasChannel() {
		return
	}
	response := kit.Empty().WriteASCII("SM")
	response.WriteByte8(3) // protocol version
	response.WriteByte8(msgDateRecordAck)
	response.WriteBytes(id)
	response.WriteInt(int64(int32(m.Now.Unix() - epoch2000.Unix())))
	response.WriteByte8(index)
	response.WriteByte8(report - 0x200)
	checksum := int16(-0x0A60) // (short) 0xF5A0
	for i := 0; i < response.ReadableBytes(); i += 2 {
		checksum ^= int16(response.GetShortLE(i))
	}
	response.WriteShort(int(checksum))
	m.Send(response)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.Skip(2)            // header
	buf.ReadUnsignedByte() // protocol version
	typ := buf.ReadUnsignedByte()
	id := buf.ReadBytes(8)
	ds := m.Session(hex.EncodeToString(id))
	if ds == nil || typ != msgDateRecord {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyVersionFw, buf.ReadUnsignedShort())
	pos.Set(model.KeyStatus, buf.ReadUnsignedShort())
	t := kit.NewDateBuilder(nil).SetDate(2000, 1, 1).AddSeconds(buf.ReadUnsignedInt()).Date()
	m.LastLocation(pos, &t)

	index := buf.ReadUnsignedByte()
	pos.Set(model.KeyIndex, index)
	report := buf.ReadUnsignedShort()
	buf.ReadUnsignedShort() // length
	pos.Set(model.KeyBattery, buf.ReadUnsignedShort())

	network := model.NewNetwork()
	if report != 0x0203 {
		count := 1
		if report != 0x0200 {
			count = buf.ReadUnsignedByte()
		}
		for i := 0; i < count; i++ {
			mcc, mnc := buf.ReadUnsignedShort(), buf.ReadUnsignedShort()
			lac, cid := buf.ReadUnsignedShort(), buf.ReadUnsignedShort()
			if i == 0 {
				buf.ReadSignedByte() // timing advance
			}
			network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), int(buf.ReadSignedByte())))
		}
	}
	if report == 0x0202 || report == 0x0203 {
		for count := buf.ReadUnsignedByte(); count > 0; count-- {
			buf.SetReaderIndex(buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0) + 1) // ssid
			mac := kit.Format("%02x:%02x:%02x:%02x:%02x:%02x",
				buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte(),
				buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
			network.AddWifiAccessPoint(model.NewWifiAccessPoint(mac, int(buf.ReadSignedByte())))
		}
	}
	pos.Network = network
	sendResponse(m, id, index, report)
	return pos
}
