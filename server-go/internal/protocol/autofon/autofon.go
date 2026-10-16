// Package autofon is the AutoFon protocol (AutoFonProtocol): fixed-length binary
// messages over TCP, in the original and the AutoFon 4.5 formats.
package autofon

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "autofon",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgLogin      = 0x10
	msgLocation   = 0x11
	msgHistory    = 0x12
	msg45Login    = 0x41
	msg45Location = 0x02
)

// Framer is AutoFonFrameDecoder: the message type gives the length.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 12 {
			return nil
		}
		var length int
		switch buf.GetUnsignedByte(buf.ReaderIndex()) {
		case msgLogin:
			length = 12
		case msgLocation:
			length = 78
		case msgHistory:
			length = 257
		case msg45Login:
			length = 19
		case msg45Location:
			length = 34
		}
		if length != 0 && buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

func convertCoordinate(raw int32) float64 {
	degrees := raw / 1000000
	minutes := float64(raw%1000000) / 10000.0
	return float64(degrees) + minutes/60
}

func convertCoordinate45(degrees, minutes int) float64 {
	value := float64(degrees) + float64(minutes>>4)/600000.0
	if kit.Check(int64(minutes), 0) {
		return value
	}
	return -value
}

func decodePosition(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, history bool) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if !history {
		buf.ReadUnsignedByte() // interval
		buf.Skip(8)            // settings
	}
	pos.Set(model.KeyStatus, buf.ReadUnsignedByte())
	if !history {
		buf.ReadUnsignedShort()
	}
	pos.Set(model.KeyBattery, buf.ReadUnsignedByte())
	buf.Skip(6) // time

	if !history {
		for i := 0; i < 2; i++ {
			buf.Skip(5)             // time
			buf.ReadUnsignedShort() // interval
			buf.Skip(5)             // mode
		}
	}

	pos.Set(model.PrefixTemp+"1", int(buf.ReadSignedByte()))

	rssi := buf.ReadUnsignedByte()
	mcc, mnc := buf.ReadUnsignedShort(), buf.ReadUnsignedShort()
	lac, cid := buf.ReadUnsignedShort(), buf.ReadUnsignedShort()
	pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))

	valid := buf.ReadUnsignedByte()
	pos.Valid = valid&0xc0 != 0
	pos.Set(model.KeySatellites, valid&0x3f)

	date := kit.NewDateBuilder(nil).
		SetDateReverse(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	pos.SetTime(date.Date())

	pos.SetLatitude(convertCoordinate(buf.ReadInt()))
	pos.SetLongitude(convertCoordinate(buf.ReadInt()))
	pos.Altitude = float64(buf.ReadShort())
	pos.Speed = float64(buf.ReadUnsignedByte())
	pos.Course = float64(buf.ReadUnsignedByte()) * 2.0

	pos.Set(model.KeyHdop, buf.ReadUnsignedShort())

	buf.ReadUnsignedShort() // reserved
	buf.ReadUnsignedByte()  // checksum
	return pos
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	typ := buf.ReadUnsignedByte()

	if typ == msgLogin || typ == msg45Login {
		if typ == msgLogin {
			buf.ReadUnsignedByte() // hardware version
			buf.ReadUnsignedByte() // software version
		}
		imei := buf.ReadSlice(8).HexDump()[1:]
		if m.Session(imei) != nil && m.HasChannel() {
			response := kit.Empty()
			response.WriteBytes([]byte("resp_crc="))
			response.WriteByte8(int(buf.GetByte(buf.WriterIndex() - 1)))
			m.Send(response)
		}
		return nil
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}

	switch typ {
	case msgLocation:
		return decodePosition(m, ds, buf, false)
	case msgHistory:
		count := buf.ReadUnsignedByte() & 0x0f
		buf.ReadUnsignedShort() // total count
		positions := []*model.Position{}
		for i := 0; i < count; i++ {
			positions = append(positions, decodePosition(m, ds, buf, true))
		}
		return positions
	case msg45Location:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		status := buf.ReadUnsignedByte()
		if kit.Check(int64(status), 7) {
			pos.AddAlarm(model.AlarmGeneral)
		}
		pos.Set(model.KeyBattery, kit.ToInt(status, 7))

		buf.Skip(2) // remaining time

		pos.Set(model.PrefixTemp+"1", int(buf.ReadSignedByte()))

		buf.Skip(2)            // timer (interval and units)
		buf.ReadSignedByte()   // mode
		buf.ReadSignedByte()   // gprs sending interval
		buf.Skip(6)            // mcc, mnc, lac, cid

		valid := buf.ReadUnsignedByte()
		pos.Valid = valid>>6 != 0
		pos.Set(model.KeySatellites, valid>>6)

		t := buf.ReadUnsignedMedium()
		d := buf.ReadUnsignedMedium()
		date := kit.NewDateBuilder(nil).
			SetTime(t/10000, t/100%100, t%100).
			SetDateReverse(d/10000, d/100%100, d%100)
		pos.SetTime(date.Date())

		pos.SetLatitude(convertCoordinate45(buf.ReadUnsignedByte(), buf.ReadUnsignedMedium()))
		pos.SetLongitude(convertCoordinate45(buf.ReadUnsignedByte(), buf.ReadUnsignedMedium()))
		pos.Speed = float64(buf.ReadUnsignedByte())
		pos.Course = float64(buf.ReadUnsignedShort())
		return pos
	}
	return nil
}
