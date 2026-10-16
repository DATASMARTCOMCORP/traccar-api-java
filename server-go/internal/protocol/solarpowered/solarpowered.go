// Package solarpowered is the SolarPowered protocol (SolarPoweredProtocol): binary
// tag-length-value reports in JT808 frames over TCP.
package solarpowered

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/jt808"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "solarpowered",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  jt808.Framer(),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const msgActiveReporting = 0x11

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // start marker

	imei := kit.Substring(buf.ReadSlice(8).HexDump(), 0, 15)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	typ := buf.ReadUnsignedByte()
	buf.ReadUnsignedShort() // attributes

	if typ != msgActiveReporting {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	for buf.ReadableBytes() > 2 {
		tag := buf.ReadUnsignedByte()
		length := buf.ReadUnsignedByte()
		switch tag {
		case 0x81:
			status := int64(buf.ReadUnsignedByte())
			b := kit.NewDateBuilder(nil).
				SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
				SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
			pos.SetTime(b.Date())
			pos.SetLatitude(float64(buf.ReadUnsignedInt()) / 1000000.0)
			if kit.Check(status, 3) {
				pos.SetLatitude(-pos.Latitude)
			}
			pos.SetLongitude(float64(buf.ReadUnsignedInt()) / 1000000.0)
			if kit.Check(status, 2) {
				pos.SetLongitude(-pos.Longitude)
			}
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
			temperature := buf.ReadUnsignedByte()
			if kit.Check(int64(temperature), 7) {
				pos.Set(model.KeyDeviceTemp, -kit.ToInt(temperature, 7))
			} else {
				pos.Set(model.KeyDeviceTemp, kit.ToInt(temperature, 7))
			}
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedByte())/50.0)
			pos.Course = float64(buf.ReadUnsignedByte())
		case 0x82:
			alarmMask := int64(buf.ReadUnsignedByte())
			alarm := int64(buf.ReadUnsignedByte())
			if kit.Check(alarmMask, 0) && kit.Check(alarm, 0) {
				pos.AddAlarm(model.AlarmTampering)
			}
			if kit.Check(alarmMask, 1) && kit.Check(alarm, 1) {
				pos.AddAlarm(model.AlarmLowPower)
			}
			if kit.Check(alarmMask, 2) && kit.Check(alarm, 2) {
				pos.AddAlarm(model.AlarmSos)
			}
			if kit.Check(alarmMask, 3) && kit.Check(alarm, 3) {
				pos.AddAlarm(model.AlarmFallDown)
			}
			if kit.Check(alarmMask, 4) {
				pos.Set(model.KeyMotion, kit.Check(alarm, 4))
			}
		case 0x83:
			buf.ReadUnsignedInt()  // uptime
			buf.ReadUnsignedInt()  // gps count
			buf.ReadUnsignedInt()  // gsm count
			buf.ReadUnsignedByte() // positioning time
			buf.ReadUnsignedByte() // registration time
			buf.ReadUnsignedByte() // connection time
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		default:
			buf.Skip(length)
		}
	}
	return pos
}
