// Package transync is the TranSync protocol (TranSyncProtocol): binary reports over TCP,
// ended by "##".
package transync

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "transync",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is TranSyncFrameDecoder: a fixed part, tagged values, then "##".
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 5 {
			return nil
		}
		index := 3 + buf.GetUnsignedByte(buf.ReaderIndex()+2) + buf.ReaderIndex()
		for buf.WriterIndex()-index >= 5 {
			if buf.GetUnsignedByte(index) == 0x23 {
				break
			}
			index++
			tagLength := buf.GetUnsignedByte(index)
			index++
			index += tagLength
		}
		if buf.GetUnsignedShort(index) == 0x2323 {
			index += 2
			return buf.ReadSlice(index - buf.ReaderIndex())
		}
		return nil
	}}
}

func decodeAlarm(value int) string {
	switch value {
	case 4:
		return model.AlarmLowBattery
	case 6:
		return model.AlarmPowerRestored
	case 10:
		return model.AlarmSos
	case 13:
		return model.AlarmBraking
	case 14:
		return model.AlarmAcceleration
	case 17:
		return model.AlarmOverspeed
	case 23:
		return model.AlarmAccident
	}
	return ""
}

// decode is TranSyncProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedShort() // header
	buf.ReadSignedByte()    // length

	lac := buf.ReadUnsignedShort()

	ds := m.Session(buf.ReadSlice(8).HexDump())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	buf.ReadUnsignedShort() // index
	buf.ReadUnsignedByte()  // type

	pos.SetTime(kit.NewDateBuilder(nil).
		SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		Date())

	latitude := float64(buf.ReadUnsignedInt()) / 1800000.0
	longitude := float64(buf.ReadUnsignedInt()) / 1800000.0

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
	pos.Course = float64(buf.ReadUnsignedShort())

	mnc := buf.ReadUnsignedByte()
	cid := buf.ReadUnsignedShort()
	status0 := int64(buf.ReadUnsignedByte())

	pos.Valid = kit.Check(status0, 0)
	if kit.Check(status0, 1) {
		pos.SetLatitude(latitude)
	} else {
		pos.SetLatitude(-latitude)
	}
	if kit.Check(status0, 2) {
		pos.SetLongitude(longitude)
	} else {
		pos.SetLongitude(-longitude)
	}

	pos.Set(model.PrefixOut+"1", kit.Check(status0, 7))
	pos.Set(model.PrefixOut+"2", kit.Check(status0, 6))
	pos.Set(model.PrefixIn+"3", kit.Check(status0, 5))
	if kit.Check(status0, 4) {
		pos.AddAlarm(model.AlarmPowerOff)
	}
	pos.Set(model.KeyIgnition, kit.Check(status0, 3))

	buf.ReadUnsignedByte() // reserved

	event := buf.ReadUnsignedByte()
	pos.AddAlarm(decodeAlarm(event))
	pos.Set(model.KeyEvent, event)

	status3 := int64(buf.ReadUnsignedByte())
	if kit.Check(status3, 7) {
		pos.Set(model.KeyArchive, true)
	}
	if kit.Check(status3, 5) {
		pos.AddAlarm(model.AlarmGpsAntennaCut)
	}

	rssi := buf.ReadUnsignedByte()
	cellTower := m.CellTowerFromLacCid(lac, int64(cid))
	mobileNetworkCode := int64(mnc)
	cellTower.MobileNetworkCode = &mobileNetworkCode
	cellTower.SetSignalStrength(rssi)
	pos.Network = model.NetworkOf(cellTower)

	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedByte()/10))
	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	pos.Set(model.KeyHdop, buf.ReadUnsignedByte())
	pos.Set(model.PrefixAdc+"1", int(int16(buf.ReadUnsignedShort())))

	if buf.ReadableBytes() > 5 {
		buf.ReadUnsignedByte() // odometer id
		if length := buf.ReadUnsignedByte(); length > 0 {
			pos.Set(model.KeyOdometer, int(kit.NewBuf(buf.ReadBytes(length)).ReadInt()))
		}
	}
	if buf.ReadableBytes() > 5 {
		buf.ReadUnsignedByte() // rfid id
		if length := buf.ReadUnsignedByte(); length > 0 {
			pos.Set(model.KeyDriverUniqueID, buf.ReadSlice(length).HexDump())
		}
	}
	if buf.ReadableBytes() > 5 {
		buf.ReadUnsignedByte() // adc2 id
		if length := buf.ReadUnsignedByte(); length > 0 {
			pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedShort())
		}
	}
	if buf.ReadableBytes() > 5 {
		buf.ReadUnsignedByte() // adc3 id
		if length := buf.ReadUnsignedByte(); length > 0 && length <= buf.ReadableBytes()-2 {
			pos.Set(model.PrefixAdc+"3", buf.ReadUnsignedShort())
		}
	}

	return pos
}
