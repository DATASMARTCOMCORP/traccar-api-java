// Package topin is the Topin protocol (TopinProtocol): 0x7878 binary messages, similar to
// GT06, without framing.
package topin

import (
	"fmt"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/gt06"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server. topin.disableCommands drops the
// data commands.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	p := ingest.Protocol{
		Name: "topin",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
	if !cfg.Bool("topin.disableCommands", false) {
		p.DataCommands = protocol.DataCommands("topin")
	}
	return p, nil
}

const (
	msgLogin          = 0x01
	msgGps2           = 0x08
	msgGpsOffline2    = 0x09
	msgGps            = 0x10
	msgGpsOffline     = 0x11
	msgStatus         = 0x13
	msgWifiOffline    = 0x17
	msgLbsWifi        = 0x18
	msgLbsWifiOffline = 0x19
	msgLbsWifi2       = 0x1A
	msgTimeUpdate     = 0x30
	msgSosNumber      = 0x41
	msgWifi           = 0x69
	msgVibration      = 0x94
	msgSosAlarm       = 0x99
)

func sendResponse(m *ingest.Message, length, t int, content []byte) {
	response := kit.Empty()
	response.WriteShort(0x7878)
	response.WriteByte8(length)
	response.WriteByte8(t)
	response.WriteBytes(content)
	response.WriteByte8('\r')
	response.WriteByte8('\n')
	m.Send(response)
}

func updateTime(m *ingest.Message, t int) {
	now := m.Now.UTC()
	date := kit.Empty()
	date.WriteShort(now.Year())
	date.WriteByte8(int(now.Month()))
	date.WriteByte8(now.Day())
	date.WriteByte8(now.Hour())
	date.WriteByte8(now.Minute())
	date.WriteByte8(now.Second())
	sendResponse(m, date.ReadableBytes(), t, date.Bytes())
}

func readCoordinate(buf *kit.Buf) float64 {
	degrees := buf.ReadUnsignedByte()
	negative := buf.GetUnsignedByte(buf.ReaderIndex())&0xf0 > 0
	decimal := buf.ReadUnsignedMedium() & 0x0fffff
	result := float64(degrees) + float64(decimal)/1000000.0
	if negative {
		return -result
	}
	return result
}

func decodeAlarm(alarms int) string {
	a := int64(alarms)
	switch {
	case kit.Check(a, 0):
		return model.AlarmVibration
	case kit.Check(a, 1):
		return model.AlarmOverspeed
	case kit.Check(a, 4):
		return model.AlarmLowPower
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // header
	length := buf.ReadUnsignedByte()
	t := buf.ReadUnsignedByte()

	if t == msgLogin {
		imei := buf.ReadSlice(8).HexDump()[1:]
		ds := m.Session(imei)
		status := 0x44
		if ds != nil {
			status = 0x01
		}
		sendResponse(m, length, t, []byte{byte(status)})
		updateTime(m, msgTimeUpdate)
		return nil
	}
	ds := m.Session()
	if ds == nil {
		return nil
	}

	switch t {
	case msgGps2, msgGpsOffline2:
		if buf.ReadableBytes() <= 2 {
			return nil
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		b := kit.NewDateBuilder(nil)
		b.SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
		b.SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
		pos.SetTime(b.Date())

		pos.Valid = t == msgGps2
		pos.SetLatitude(readCoordinate(buf))
		pos.SetLongitude(readCoordinate(buf))

		buf.Skip(4 + 4) // second coordinates

		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
		pos.Course = float64(buf.ReadUnsignedByte() * 2)

		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		return pos

	case msgGps, msgGpsOffline:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		timeBytes := buf.GetBytes(buf.ReaderIndex(), 6)

		gt06.DecodeGps(pos, buf, false, time.UTC)

		if buf.ReadableBytes() >= 5 {
			pos.Altitude = float64(buf.ReadShort())
			pos.AddAlarm(decodeAlarm(buf.ReadUnsignedByte()))
		}

		sendResponse(m, length, t, timeBytes)
		return pos

	case msgTimeUpdate:
		updateTime(m, t)
		return nil

	case msgStatus:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)

		content := buf.GetBytes(buf.ReaderIndex(), buf.ReadableBytes()-2)

		pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
		pos.Set(model.KeyVersionFw, buf.ReadUnsignedByte())
		buf.ReadUnsignedByte() // timezone
		buf.ReadUnsignedByte() // interval
		if buf.ReadableBytes() >= 1+2 {
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		}
		if buf.ReadableBytes() >= 3+2 {
			buf.Skip(3) // temperature
		}
		if buf.ReadableBytes() >= 1+2 {
			pos.Set(model.KeyCharge, buf.ReadUnsignedByte() > 0)
		}
		if buf.ReadableBytes() >= 1+2 {
			pos.Set(model.KeyHeartRate, buf.ReadUnsignedByte())
		}

		sendResponse(m, length, t, content)
		return pos

	case msgWifi, msgWifiOffline, msgLbsWifi, msgLbsWifi2, msgLbsWifiOffline:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		timeBytes := buf.GetBytes(buf.ReaderIndex(), 6)
		timeBuf := buf.ReadSlice(6)
		b := kit.NewDateBuilder(nil)
		b.SetYear(kit.ReadBCD(timeBuf, 2))
		b.SetMonth(kit.ReadBCD(timeBuf, 2))
		b.SetDay(kit.ReadBCD(timeBuf, 2))
		b.SetHour(kit.ReadBCD(timeBuf, 2))
		b.SetMinute(kit.ReadBCD(timeBuf, 2))
		b.SetSecond(kit.ReadBCD(timeBuf, 2))
		date := b.Date()
		m.LastLocation(pos, &date)

		network := model.NewNetwork()
		for range length {
			mac := fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
				buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte(),
				buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
			network.AddWifiAccessPoint(model.NewWifiAccessPoint(mac, buf.ReadUnsignedByte()))
		}

		cellCount := buf.ReadUnsignedByte()
		mcc := buf.ReadUnsignedShort()
		mnc := buf.ReadUnsignedByte()
		for range cellCount {
			network.AddCellTower(model.NewCellTowerRSSI(
				mcc, mnc, buf.ReadUnsignedShort(), int64(buf.ReadUnsignedShort()), buf.ReadUnsignedByte()))
		}

		if buf.ReadableBytes() > 2 {
			pos.AddAlarm(decodeAlarm(buf.ReadUnsignedByte()))
		}

		pos.Network = network

		sendResponse(m, length, t, timeBytes)
		return pos

	case msgVibration, msgSosAlarm:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		if t == msgVibration {
			pos.AddAlarm(model.AlarmVibration)
		} else {
			pos.AddAlarm(model.AlarmSos)
		}
		return pos
	}
	return nil
}

// encode is TopinProtocolEncoder.
func encode(_ *ingest.Message, c *model.Command) any {
	if c.Type != model.CommandSosNumber {
		return nil
	}
	phone := kit.CommandString(c, model.CommandKeyPhone)
	if phone == nil {
		kit.Throw("NullPointerException: phone")
	}
	content := kit.NettyBytes(*phone)
	buf := kit.Empty()
	buf.WriteByte8(0x78)
	buf.WriteByte8(0x78)
	buf.WriteByte8(1 + len(content)) // message length
	buf.WriteByte8(msgSosNumber)
	buf.WriteBytes(content)
	buf.WriteByte8('\r')
	buf.WriteByte8('\n')
	return buf
}
