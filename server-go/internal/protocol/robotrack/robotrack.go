// Package robotrack is the RoboTrack protocol (RoboTrackProtocol): fixed-length binary
// messages chosen by their type byte.
package robotrack

import (
	"math"
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "robotrack",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgID         = 0x00
	msgAck        = 0x80
	msgGPS        = 0x03
	msgGSM        = 0x04
	msgImageStart = 0x06
	msgImageData  = 0x07
	msgImageEnd   = 0x08
)

func messageLength(buf *kit.Buf) int {
	switch buf.GetUnsignedByte(buf.ReaderIndex()) {
	case msgID:
		return 69
	case msgAck:
		return 3
	case msgGPS, msgGSM, msgImageStart:
		return 24
	case msgImageData:
		return 8 + buf.GetUnsignedShortLE(buf.ReaderIndex()+1)
	case msgImageEnd:
		return 6
	}
	return math.MaxInt32
}

// Framer is RoboTrackFrameDecoder.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		length := messageLength(buf)
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	typ := buf.ReadUnsignedByte()

	switch typ {
	case msgID:
		buf.Skip(16) // name
		imei := buf.ReadString(15)
		if m.Session(imei) != nil {
			response := kit.Empty()
			response.WriteByte8(msgAck)
			response.WriteByte8(0x01) // success
			response.WriteByte8(0x66) // checksum
			m.Send(response)
		}
	case msgGPS, msgGSM:
		ds := m.Session()
		if ds == nil {
			return nil
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		deviceTime := time.UnixMilli(buf.ReadUnsignedIntLE() * 1000)
		pos.DeviceTime = model.NewTime(deviceTime)

		if typ == msgGPS {
			pos.Valid = true
			pos.FixTime = pos.DeviceTime
			pos.SetLatitude(float64(buf.ReadIntLE()) / 1000000.0)
			pos.SetLongitude(float64(buf.ReadIntLE()) / 1000000.0)
			pos.Speed = units.KnotsFromKph(float64(buf.ReadSignedByte()))
		} else {
			m.LastLocation(pos, &deviceTime)
			mcc := buf.ReadUnsignedShortLE()
			mnc := buf.ReadUnsignedShortLE()
			lac := buf.ReadUnsignedShortLE()
			cid := buf.ReadUnsignedShortLE()
			pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))
			buf.ReadUnsignedByte() // reserved
		}

		value := buf.ReadUnsignedByte()
		pos.Set(model.KeySatellites, kit.ToInt(value, 4))
		pos.Set(model.KeyRssi, kit.BetweenInt(value, 4, 7))
		pos.Set(model.KeyMotion, kit.Check(int64(value), 7))

		value = buf.ReadUnsignedByte()
		pos.Set(model.KeyCharge, kit.Check(int64(value), 0))
		for i := 1; i <= 4; i++ {
			pos.Set(model.PrefixIn+strconv.Itoa(i), kit.Check(int64(value), i))
		}
		pos.Set(model.KeyBatteryLevel, kit.FromInt(value, 5)*100/7)
		pos.Set(model.KeyDeviceTemp, int(buf.ReadSignedByte()))

		for i := 1; i <= 3; i++ {
			pos.Set(model.PrefixAdc+strconv.Itoa(i), buf.ReadUnsignedShortLE())
		}
		return pos
	}
	return nil
}
