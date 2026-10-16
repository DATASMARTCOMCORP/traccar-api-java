// Package mavlink2 is the MAVLink 2 protocol (Mavlink2Protocol): binary packets over UDP;
// GLOBAL_POSITION_INT messages (type 33) are positions.
package mavlink2

import (
	"math"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server. The Java pipeline frames the
// datagrams with a LengthFieldBasedFrameDecoder; the Go UDP server doesn't run framers, so
// the decoder does it (see datagramDecoder).
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "mavlink2",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Decoder: &datagramDecoder{framer: Framer(), decoder: ingest.DecoderFunc(decode)},
			}
		}}},
	}
}

// Framer is the pipeline's LengthFieldBasedFrameDecoder(1024, 1, 1, 10, 0).
func Framer() ingest.Framer {
	return ingest.NewLengthFramer(ingest.MaxFrameLength, 1, 1, 10, 0)
}

// datagramDecoder runs a frame decoder in front of a decoder on a UDP socket, as Netty's
// ByteToMessageDecoder does there: the datagrams accumulate in one buffer and every
// complete frame is decoded.
type datagramDecoder struct {
	framer  ingest.Framer
	decoder ingest.Decoder
	pending *kit.Buf
}

func (d *datagramDecoder) Decode(m *ingest.Message, msg any) any {
	in := msg.(*kit.Buf)
	if d.pending != nil {
		in = kit.Empty().WriteBuf(d.pending).WriteBuf(in)
		d.pending = nil
	}
	var results []any
	for in.IsReadable() {
		frame := d.framer.Frame(m, in)
		if frame == nil {
			break
		}
		results = append(results, d.decoder.Decode(m, frame))
	}
	if in.IsReadable() {
		d.pending = in.Copy()
	}
	if len(results) == 1 {
		return results[0]
	}
	// Several frames are several messages in Java; their positions are returned together.
	var positions []*model.Position
	for _, r := range results {
		switch v := r.(type) {
		case *model.Position:
			positions = append(positions, v)
		case []*model.Position:
			positions = append(positions, v...)
		}
	}
	return positions
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	if buf.ReadUnsignedByte() != 0xFD {
		return nil
	}
	buf.ReadUnsignedByte() // length
	buf.ReadUnsignedByte() // incompatibility flags
	buf.ReadUnsignedByte() // compatibility flags
	buf.ReadUnsignedByte() // index
	senderSystemID := buf.ReadUnsignedByte()
	buf.ReadUnsignedByte() // component id

	ds := m.Session(kit.Format("%d", senderSystemID))
	if ds == nil {
		return nil
	}

	if buf.ReadUnsignedMediumLE() == 33 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.Set("timeBoot", buf.ReadUnsignedIntLE()) // time since system boot

		pos.Valid = true
		pos.SetTime(m.Now)
		pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
		pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
		pos.Altitude = float64(buf.ReadIntLE()) / 1000.0
		pos.Set("relativeAltitude", float64(buf.ReadIntLE())/1000.0)

		groundSpeedX := int64(buf.ReadShortLE())
		groundSpeedY := int64(buf.ReadShortLE())
		buf.ReadShortLE() // ground speed z
		speed := math.Sqrt(float64(groundSpeedX*groundSpeedX + groundSpeedY*groundSpeedY))
		pos.Speed = units.KnotsFromCps(speed)
		pos.Course = float64(buf.ReadUnsignedShortLE()) / 100.0
		return pos
	}
	return nil
}
