// Package granit is the Granit protocol (GranitProtocol): "+RRCB~" (current) and
// "+DDAT~" (archive) binary reports and text replies, each ending with "\r\n".
package granit

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server. The SMS (text channel) encoder,
// GranitProtocolSmsEncoder, is SMSEncoder.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "granit",
		DataCommands: protocol.DataCommands("granit"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

// indexOf is BufferUtil.indexOf(needle, buf, start, end): it moves the buffer's indexes,
// so a start past the writer index throws (IndexOutOfBoundsException).
func indexOf(buf *kit.Buf, needle string, start, end int) int {
	if start < 0 || start > buf.WriterIndex() {
		kit.Throw("IndexOutOfBoundsException: readerIndex: %d (expected: 0 <= readerIndex <= writerIndex(%d))", start, buf.WriterIndex())
	}
	return buf.IndexOfIn(needle, start, end)
}

// Framer is GranitFrameDecoder: frames end with "\r\n", except inside the binary part
// after '~', whose length follows it.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		indexEnd := indexOf(buf, "\r\n", buf.ReaderIndex(), buf.WriterIndex())
		if indexEnd == -1 {
			return nil
		}
		indexTilde := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '~')
		if indexTilde != -1 && indexTilde < indexEnd {
			length := buf.GetUnsignedShortLE(indexTilde + 1)
			indexEnd = indexOf(buf, "\r\n", indexTilde+2+length, buf.WriterIndex())
			if indexEnd == -1 {
				return nil
			}
		}
		frame := buf.ReadSlice(indexEnd - buf.ReaderIndex())
		buf.Skip(2)
		return frame
	}}
}

const headerLength = 6

// appendChecksum is GranitProtocolDecoder.appendChecksum: '*', the XOR of the first
// length bytes in hex, "\r\n".
func appendChecksum(buf *kit.Buf, length int) {
	buf.WriteByte8('*')
	checksum := kit.XOR(buf.Array()[:length]) & 0xFF
	buf.WriteASCII(kit.Format("%02X", checksum))
	buf.WriteByte8('\r')
	buf.WriteByte8('\n')
}

func sendResponseCurrent(m *ingest.Message, deviceID int, t int64) {
	response := kit.Empty()
	response.WriteASCII("BB+UGRC~")
	response.WriteShortLE(6) // length
	response.WriteIntLE(t)
	response.WriteShortLE(deviceID)
	appendChecksum(response, 16)
	m.Send(response)
}

func sendResponseArchive(m *ingest.Message, deviceID, packNum int) {
	response := kit.Empty()
	response.WriteASCII("BB+ARCF~")
	response.WriteShortLE(4) // length
	response.WriteShortLE(packNum)
	response.WriteShortLE(deviceID)
	appendChecksum(response, 14)
	m.Send(response)
}

func decodeStructure(buf *kit.Buf, pos *model.Position) {
	flags := int64(buf.ReadUnsignedByte())
	pos.Valid = kit.Check(flags, 7)
	if kit.Check(flags, 1) {
		pos.AddAlarm(model.AlarmGeneral)
	}

	satDel := buf.ReadUnsignedByte()
	pos.Set(model.KeySatellites, kit.FromInt(satDel, 4))
	pos.Set(model.KeyPdop, kit.ToInt(satDel, 4))

	lonDegrees := buf.ReadUnsignedByte()
	latDegrees := buf.ReadUnsignedByte()
	lonMinutes := buf.ReadUnsignedShortLE()
	latMinutes := buf.ReadUnsignedShortLE()

	latitude := float64(latDegrees) + float64(latMinutes)/60000.0
	longitude := float64(lonDegrees) + float64(lonMinutes)/60000.0
	if pos.Valid {
		if !kit.Check(flags, 4) {
			latitude = -latitude
		}
		if !kit.Check(flags, 5) {
			longitude = -longitude
		}
	}
	pos.SetLongitude(longitude)
	pos.SetLatitude(latitude)

	pos.Speed = float64(buf.ReadUnsignedByte())

	course := buf.ReadUnsignedByte()
	if kit.Check(flags, 6) {
		course |= 0x100
	}
	pos.Course = float64(course)

	pos.Set(model.KeyDistance, buf.ReadShortLE())

	analogIn1 := buf.ReadUnsignedByte()
	analogIn2 := buf.ReadUnsignedByte()
	analogIn3 := buf.ReadUnsignedByte()
	analogIn4 := buf.ReadUnsignedByte()
	analogInHi := buf.ReadUnsignedByte()

	analogIn1 = analogInHi<<8&0x300 | analogIn1
	analogIn2 = analogInHi<<6&0x300 | analogIn2
	analogIn3 = analogInHi<<4&0x300 | analogIn3
	analogIn4 = analogInHi<<2&0x300 | analogIn4

	pos.Set(model.PrefixAdc+"1", analogIn1)
	pos.Set(model.PrefixAdc+"2", analogIn2)
	pos.Set(model.PrefixAdc+"3", analogIn3)
	pos.Set(model.PrefixAdc+"4", analogIn4)

	pos.Altitude = float64(buf.ReadUnsignedByte() * 10)

	output := int64(buf.ReadUnsignedByte())
	for i := range 8 {
		pos.Set(model.PrefixIo+strconv.Itoa(i+1), kit.Check(output, i))
	}
	buf.ReadUnsignedByte() // status message buffer
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	indexTilde := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '~')

	ds := m.Session()
	if ds != nil && indexTilde == -1 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.SetTime(m.Now)
		now := m.Now
		m.LastLocation(pos, &now)
		pos.Valid = false
		pos.Set(model.KeyResult, kit.Latin1(buf.Bytes()))
		return pos
	}

	if buf.ReadableBytes() < headerLength {
		return nil
	}
	switch kit.Latin1(buf.ReadSlice(headerLength).Bytes()) {
	case "+RRCB~":
		buf.Skip(2) // binary length 26
		deviceID := buf.ReadUnsignedShortLE()
		ds = m.Session(strconv.Itoa(deviceID))
		if ds == nil {
			return nil
		}
		unixTime := buf.ReadUnsignedIntLE()
		sendResponseCurrent(m, deviceID, unixTime)
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.SetTime(time.UnixMilli(unixTime * 1000))
		decodeStructure(buf, pos)
		return pos

	case "+DDAT~":
		buf.Skip(2) // binary length
		deviceID := buf.ReadUnsignedShortLE()
		ds = m.Session(strconv.Itoa(deviceID))
		if ds == nil {
			return nil
		}
		if buf.ReadSignedByte() != 4 { // format
			return nil
		}
		nblocks := buf.ReadSignedByte()
		packNum := buf.ReadUnsignedShortLE()
		sendResponseArchive(m, deviceID, packNum)

		positions := []*model.Position{}
		for nblocks > 0 {
			nblocks--
			unixTime := buf.ReadUnsignedIntLE()
			timeIncrement := int64(buf.GetUnsignedShortLE(buf.ReaderIndex() + 120))
			for i := range int64(6) {
				if buf.GetUnsignedByte(buf.ReaderIndex()) != 0xFE {
					pos := m.NewPosition()
					pos.DeviceID = ds.DeviceID
					pos.SetTime(time.UnixMilli((unixTime + i*timeIncrement) * 1000))
					decodeStructure(buf, pos)
					pos.Set(model.KeyArchive, true)
					positions = append(positions, pos)
				} else {
					buf.Skip(20) // skip filled 0xFE structure
				}
			}
			buf.Skip(2) // increment
		}
		return positions
	}
	return nil
}

func encodeCommand(command string) *kit.Buf {
	buf := kit.Empty()
	buf.WriteASCII(command)
	appendChecksum(buf, kit.Len(command))
	return buf
}

// encode is GranitProtocolEncoder.
func encode(_ *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandIdentification:
		return encodeCommand("BB+IDNT")
	case model.CommandRebootDevice:
		return encodeCommand("BB+RESET")
	case model.CommandPositionSingle:
		return encodeCommand("BB+RRCD")
	}
	return nil
}

// SMSEncoder is GranitProtocolSmsEncoder, the protocol's text (SMS) command encoder.
var SMSEncoder = ingest.CommandEncoderFunc(func(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandRebootDevice:
		return "BB+RESET"
	case model.CommandPositionPeriodic:
		return kit.FormatCommand(c, m.UniqueID(c.DeviceID), "BB+BBMD=%s", model.CommandKeyFrequency)
	}
	return nil
})
