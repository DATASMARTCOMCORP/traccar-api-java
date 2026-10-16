// Package oigo is the Oigo protocol (OigoProtocol): AR and MG binary messages over UDP.
package oigo

import (
	"encoding/hex"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "oigo",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgArLocation      = 0x00
	msgAcknowledgement = 0xE0
)

func decodeAr(m *ingest.Message, buf *kit.Buf) any {
	buf.Skip(1)             // header
	buf.ReadUnsignedShort() // length
	typ := buf.ReadUnsignedByte()
	tag := buf.ReadUnsignedByte()

	var ds *ingest.DeviceSession
	switch kit.ToInt(tag, 3) {
	case 0:
		ds = m.Session(kit.SubstringFrom(hex.EncodeToString(buf.ReadBytes(8)), 1))
	case 1:
		buf.Skip(1)
		ds = m.Session(buf.ReadString(14))
	default:
		ds = m.Session()
	}
	if ds == nil || typ != msgArLocation {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyEvent, buf.ReadUnsignedByte())

	mask := int64(buf.ReadInt())
	if kit.Check(mask, 0) {
		pos.Set(model.KeyIndex, buf.ReadUnsignedShort())
	}
	if kit.Check(mask, 1) {
		date := buf.ReadUnsignedByte()
		pos.SetTime(kit.NewDateBuilder(nil).
			SetDate(kit.BetweenInt(date, 4, 8)+2010, kit.ToInt(date, 4), buf.ReadUnsignedByte()).
			SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
			Date())
	}
	if kit.Check(mask, 2) {
		buf.Skip(5) // device time
	}
	if kit.Check(mask, 3) {
		pos.SetLatitude(float64(buf.ReadUnsignedInt())/1000000.0 - 90)
		pos.SetLongitude(float64(buf.ReadUnsignedInt())/1000000.0 - 180.0)
	}
	if kit.Check(mask, 4) {
		status := buf.ReadUnsignedByte()
		pos.Valid = kit.BetweenInt(status, 4, 8) != 0
		pos.Set(model.KeySatellites, kit.ToInt(status, 4))
		pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
	}
	if kit.Check(mask, 5) {
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
	}
	if kit.Check(mask, 6) {
		pos.Course = float64(buf.ReadUnsignedShort())
	}
	if kit.Check(mask, 7) {
		pos.Altitude = float64(buf.ReadShort())
	}
	if kit.Check(mask, 8) {
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
	}
	if kit.Check(mask, 9) {
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/1000.0)
	}
	if kit.Check(mask, 10) {
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
	}
	if kit.Check(mask, 11) {
		buf.Skip(2) // gpio
	}
	if kit.Check(mask, 12) {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*1000)
	}
	if kit.Check(mask, 13) {
		buf.Skip(6) // software version
	}
	if kit.Check(mask, 14) {
		buf.Skip(5) // hardware version
	}
	if kit.Check(mask, 15) {
		buf.ReadUnsignedShort() // device config
	}
	return pos
}

// convertCoordinate reads DDDMM.MMM packed as an integer.
func convertCoordinate(value int64) float64 {
	negative := value < 0
	if negative {
		value = -value
	}
	minutes := float64(value%100000) / 1000.0
	value /= 100000
	degrees := float64(value) + minutes/60
	if negative {
		return -degrees
	}
	return degrees
}

func decodeMg(m *ingest.Message, buf *kit.Buf) any {
	buf.ReadUnsignedByte() // tag
	flags := int64(buf.GetUnsignedByte(buf.ReaderIndex()))
	var ds *ingest.DeviceSession
	if kit.Check(flags, 6) {
		buf.ReadUnsignedByte() // flags
		ds = m.Session()
	} else {
		ds = m.Session(kit.SubstringFrom(hex.EncodeToString(buf.ReadBytes(8)), 1))
	}
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	buf.Skip(8) // imsi

	date := buf.ReadUnsignedShort()
	b := kit.NewDateBuilder(nil).
		SetDate(2010+kit.FromInt(date, 12), kit.BetweenInt(date, 8, 12), kit.ToInt(date, 8)).
		SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), 0)

	pos.Valid = true
	pos.SetLatitude(convertCoordinate(int64(buf.ReadInt())))
	pos.SetLongitude(convertCoordinate(int64(buf.ReadInt())))
	pos.Altitude = units.MetersFromFeet(float64(buf.ReadShort()))
	pos.Course = float64(buf.ReadUnsignedShort())
	pos.Speed = units.KnotsFromMph(float64(buf.ReadUnsignedByte()))
	pos.Set(model.KeyPower, float64(buf.ReadUnsignedByte())/10.0)
	pos.Set(model.PrefixIo+"1", buf.ReadUnsignedByte())

	b.SetSecond(buf.ReadUnsignedByte())
	pos.SetTime(b.Date())

	pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
	index := buf.ReadUnsignedByte()
	pos.Set(model.KeyVersionFw, buf.ReadUnsignedByte())
	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	pos.Set(model.KeyOdometer, javaLong(float64(buf.ReadUnsignedInt())*1609.34))

	if m.HasChannel() && kit.Check(flags, 7) {
		m.Send(kit.NewBuf([]byte{msgAcknowledgement, byte(index), 0x00}))
	}
	return pos
}

// javaLong is Java's (long) cast of a non-negative double.
func javaLong(f float64) int64 { return int64(f) }

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	if buf.GetUnsignedByte(buf.ReaderIndex()) == 0x7e {
		return decodeAr(m, buf)
	}
	return decodeMg(m, buf)
}
