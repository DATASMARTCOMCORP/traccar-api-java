// Package gps056 is the GPS056 protocol (Gps056Protocol): "$$" binary messages over TCP
// with a decimal text length, answered with "*<type><imei>...#".
package gps056

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "gps056",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const messageHeader = 4

// Framer is Gps056FrameDecoder: the length is two ASCII digits at (absolute) index 2;
// bytes up to the next '$' after a frame are dropped.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() >= messageHeader {
			length := kit.Int(buf.GetLatin1(2, 2)) + 5
			if buf.ReadableBytes() >= length {
				frame := buf.ReadSlice(length)
				for buf.IsReadable() && buf.GetUnsignedByte(buf.ReaderIndex()) != '$' {
					buf.Skip(1)
				}
				return frame
			}
		}
		return nil
	}}
}

func sendResponse(m *ingest.Message, typ, imei string, content *kit.Buf) {
	response := kit.Empty()
	response.WriteBytes(kit.ASCIIBytes("*" + typ + imei))
	if content != nil {
		response.WriteBuf(content)
	}
	response.WriteByte8('#')
	m.Send(response)
}

func decodeCoordinate(buf *kit.Buf) float64 {
	degrees := float64(buf.GetUnsignedShort(buf.ReaderIndex()) / 100)
	minutes := float64(buf.ReadUnsignedShort()%100) + float64(buf.ReadUnsignedShort())/10000.0
	degrees += minutes / 60
	hemisphere := buf.ReadSignedByte()
	if hemisphere == 'S' || hemisphere == 'W' {
		degrees = -degrees
	}
	return degrees
}

func decodeStatus(buf *kit.Buf, pos *model.Position) {
	pos.Set(model.KeyInput, buf.ReadUnsignedByte())
	pos.Set(model.KeyOutput, buf.ReadUnsignedByte())

	pos.Set(model.PrefixAdc+"1", float64(buf.ReadShortLE())*5.06) // mV

	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2) // header
	buf.Skip(2) // length

	typ := buf.ReadString(7)
	imei := buf.ReadString(15)

	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	switch {
	case strings.HasPrefix(typ, "LOGN"):
		sendResponse(m, "LGSA"+kit.SubstringFrom(typ, 4), imei, kit.NewBuf(kit.NettyBytes("1")))

	case strings.HasPrefix(typ, "GPSL"):
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		date := kit.NewDateBuilder(nil).
			SetDateReverse(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
			SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())

		pos.Valid = true
		pos.SetTime(date.Date())
		pos.SetLatitude(decodeCoordinate(buf))
		pos.SetLongitude(decodeCoordinate(buf))
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
		pos.Course = float64(buf.ReadUnsignedShort())

		decodeStatus(buf, pos)

		sendResponse(m, "GPSA"+kit.SubstringFrom(typ, 4), imei, buf.ReadSlice(2))

		return pos

	case strings.HasPrefix(typ, "SYNC"):
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		m.LastLocation(pos, nil)

		decodeStatus(buf, pos)

		sendResponse(m, "SYSA"+kit.SubstringFrom(typ, 4), imei, nil)

		return pos
	}

	return nil
}
