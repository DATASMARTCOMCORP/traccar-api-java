// Package apel is the Apel protocol (ApelProtocol): little-endian binary messages over
// TCP with a CRC-32; the decoder requests the device's log records it hasn't seen.
package apel

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "apel",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			f := ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, 4, 0)
			f.LittleEndian = true
			return ingest.Pipeline{Framer: f, Decoder: &Decoder{}}
		}}},
	}
}

// Message types.
const (
	msgTrackerID             = 11
	msgTrackerIDExt          = 12
	msgStateFullInfoT104     = 92
	msgCurrentGPSData        = 101
	msgRequestLastLogIndex   = 120
	msgLastLogIndex          = 121
	msgRequestLogRecords     = 130
	msgLogRecords            = 131
	msgAckAlarm              = 160
)

// Decoder is ApelProtocolDecoder: it keeps the connection's log indexes.
type Decoder struct {
	lastIndex int64
	newIndex  int64
}

func sendSimpleMessage(m *ingest.Message, typ int) {
	request := kit.Empty()
	request.WriteShortLE(typ)
	request.WriteShortLE(0)
	request.WriteIntLE(int64(kit.CRC32(kit.CRC32_STANDARD, request.GetBytes(0, 4))))
	m.Send(request)
}

func (d *Decoder) requestArchive(m *ingest.Message) {
	if d.lastIndex == 0 {
		d.lastIndex = d.newIndex
	} else if d.newIndex > d.lastIndex {
		request := kit.Empty()
		request.WriteShortLE(msgRequestLogRecords)
		request.WriteShortLE(6)
		request.WriteIntLE(int64(int32(d.lastIndex)))
		request.WriteShortLE(512)
		request.WriteIntLE(int64(kit.CRC32(kit.CRC32_STANDARD, request.GetBytes(0, 10))))
		m.Send(request)
	}
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	typ := buf.ReadUnsignedShortLE()
	alarm := typ&0x8000 != 0
	typ &= 0x7FFF
	buf.ReadUnsignedShortLE() // length

	if alarm {
		sendSimpleMessage(m, msgAckAlarm)
	}

	switch typ {
	case msgTrackerID:
		return nil // unsupported authentication type
	case msgTrackerIDExt:
		buf.ReadUnsignedIntLE() // id
		length := buf.ReadUnsignedShortLE()
		buf.Skip(length)
		length = buf.ReadUnsignedShortLE()
		m.Session(buf.ReadString(length))
	case msgLastLogIndex:
		if index := buf.ReadUnsignedIntLE(); index > 0 {
			d.newIndex = index
			d.requestArchive(m)
		}
	case msgCurrentGPSData, msgStateFullInfoT104, msgLogRecords:
		return d.decodePositions(m, buf, typ)
	}
	return nil
}

func (d *Decoder) decodePositions(m *ingest.Message, buf *kit.Buf, typ int) any {
	ds := m.Session()
	if ds == nil {
		return nil
	}
	positions := []*model.Position{}

	recordCount := 1
	if typ == msgLogRecords {
		recordCount = buf.ReadUnsignedShortLE()
	}
	for j := 0; j < recordCount; j++ {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		subtype := typ
		if typ == msgLogRecords {
			pos.Set(model.KeyArchive, true)
			d.lastIndex = buf.ReadUnsignedIntLE() + 1
			pos.Set(model.KeyIndex, d.lastIndex)

			subtype = buf.ReadUnsignedShortLE()
			if subtype != msgCurrentGPSData && subtype != msgStateFullInfoT104 {
				buf.Skip(buf.ReadUnsignedShortLE())
				continue
			}
			buf.ReadUnsignedShortLE() // length
		}

		pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
		pos.SetLatitude(float64(buf.ReadIntLE()) * 180.0 / 0x7FFFFFFF)
		pos.SetLongitude(float64(buf.ReadIntLE()) * 180.0 / 0x7FFFFFFF)

		if subtype == msgStateFullInfoT104 {
			speed := buf.ReadUnsignedByte()
			pos.Valid = speed != 255
			pos.Speed = units.KnotsFromKph(float64(speed))
			pos.Set(model.KeyHdop, int(buf.ReadSignedByte()))
		} else {
			speed := buf.ReadShortLE()
			pos.Valid = speed != -1
			pos.Speed = units.KnotsFromKph(float64(speed) / 100.0)
		}

		pos.Course = float64(buf.ReadShortLE()) / 100.0
		pos.Altitude = float64(buf.ReadShortLE())

		if subtype == msgStateFullInfoT104 {
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set(model.KeyEvent, buf.ReadUnsignedShortLE())
			pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
			pos.Set(model.KeyInput, buf.ReadUnsignedByte())
			pos.Set(model.KeyOutput, buf.ReadUnsignedByte())
			for i := 1; i <= 8; i++ {
				pos.Set(model.PrefixAdc+string(rune('0'+i)), buf.ReadUnsignedShortLE())
			}
			pos.Set(model.PrefixCount+"1", buf.ReadUnsignedIntLE())
			pos.Set(model.PrefixCount+"2", buf.ReadUnsignedIntLE())
			pos.Set(model.PrefixCount+"3", buf.ReadUnsignedIntLE())
		}
		positions = append(positions, pos)
	}

	buf.ReadUnsignedIntLE() // crc

	if typ == msgLogRecords {
		d.requestArchive(m)
	} else {
		sendSimpleMessage(m, msgRequestLastLogIndex)
	}
	return positions
}
