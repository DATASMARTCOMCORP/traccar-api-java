// Package cellocator is the Cellocator protocol (CellocatorProtocol): binary "MCGP"
// messages over TCP and UDP.
package cellocator

import (
	"encoding/hex"
	"math"
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: a TCP and a UDP server.
func Protocol() ingest.Protocol {
	pipeline := func(framer bool) func() ingest.Pipeline {
		return func() ingest.Pipeline {
			pl := ingest.Pipeline{Decoder: ingest.DecoderFunc(decode), Encoder: ingest.CommandEncoderFunc(encode)}
			if framer {
				pl.Framer = Framer()
			}
			return pl
		}
	}
	return ingest.Protocol{
		Name:         "cellocator",
		DataCommands: protocol.DataCommands("cellocator"),
		Servers:      []ingest.Server{{NewPipeline: pipeline(true)}, {Datagram: true, NewPipeline: pipeline(false)}},
	}
}

const (
	msgClientStatus      = 0
	msgClientProgramming = 3
	msgClientSerialLog   = 7
	msgClientSerial      = 8
	msgClientModular     = 9
	msgClientModularExt  = 11
	msgServerAcknowledge = 4
	messageMinimumLength = 15
)

// Framer is CellocatorFrameDecoder: the length depends on the message type. Like the
// Java decoder, the type is read at the absolute index 4.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < messageMinimumLength {
			return nil
		}
		length := 0
		switch buf.GetUnsignedByte(4) {
		case msgClientStatus:
			length = 70
		case msgClientProgramming:
			length = 31
		case msgClientSerialLog:
			length = 70
		case msgClientSerial:
			if buf.ReadableBytes() >= 19 {
				length = 19 + buf.GetUnsignedShortLE(buf.ReaderIndex()+16)
			}
		case msgClientModular:
			length = 15 + buf.GetUnsignedByte(buf.ReaderIndex()+13)
		case msgClientModularExt:
			length = 16 + buf.GetUnsignedShortLE(buf.ReaderIndex()+13)
		}
		if length > 0 && buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

// encodeContent is CellocatorProtocolDecoder/Encoder.encodeContent.
func encodeContent(typ int, uniqueID int32, packetNumber int, content *kit.Buf) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8('M')
	buf.WriteByte8('C')
	buf.WriteByte8('G')
	buf.WriteByte8('P')
	buf.WriteByte8(typ)
	buf.WriteIntLE(int64(uniqueID))
	buf.WriteByte8(packetNumber)
	buf.WriteIntLE(0) // authentication code
	buf.WriteBuf(content)

	var checksum int8
	for i := 4; i < buf.WriterIndex(); i++ {
		checksum += buf.GetByte(i)
	}
	buf.WriteByte8(int(checksum))
	return buf
}

func sendResponse(m *ingest.Message, deviceID int64, packetNumber int8) {
	content := kit.Empty()
	content.WriteByte8(0)
	content.WriteByte8(int(packetNumber))
	content.WriteZero(11)
	m.Send(encodeContent(msgServerAcknowledge, int32(deviceID), 0, content))
}

func sendModuleResponse(m *ingest.Message, deviceID int64, packetNumber int8) {
	content := kit.Empty()
	content.WriteByte8(0x80)
	content.WriteShortLE(10) // modules length
	content.WriteIntLE(0)    // reserved
	content.WriteByte8(9)    // ack module type
	content.WriteShortLE(3)  // module length
	content.WriteByte8(0)    // ack
	content.WriteShortLE(0)  // reserved
	m.Send(encodeContent(msgClientModularExt, int32(deviceID), int(packetNumber), content))
}

func decodeAlarm(reason int) string {
	switch reason {
	case 70:
		return model.AlarmSos
	case 80:
		return model.AlarmPowerCut
	case 81:
		return model.AlarmLowPower
	}
	return ""
}

// radians is Java's "value / Math.PI * 180 / 100000000".
func radians(value int32) float64 { return float64(value) / math.Pi * 180 / 100000000 }

func decodeStatus(m *ingest.Message, buf *kit.Buf, ds *ingest.DeviceSession, alternative bool) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyVersionHw, buf.ReadUnsignedByte())
	pos.Set(model.KeyVersionFw, buf.ReadUnsignedByte())
	buf.ReadUnsignedByte() // protocol version

	pos.Set(model.KeyStatus, buf.ReadUnsignedByte()&0x0f)

	buf.ReadUnsignedByte() // operator / configuration flags
	buf.ReadUnsignedByte() // reason data
	event := buf.ReadUnsignedByte()
	pos.AddAlarm(decodeAlarm(event))
	pos.Set(model.KeyEvent, event)

	pos.Set("mode", buf.ReadUnsignedByte())

	input := buf.ReadUnsignedInt()
	pos.Set(model.KeyIgnition, kit.Check(input, 3*8+5))
	pos.Set(model.KeyDoor, kit.Check(input, 3*8))
	pos.Set(model.KeyCharge, kit.Check(input, 7))
	pos.Set(model.KeyInput, input)

	if alternative {
		buf.ReadUnsignedByte() // input
		pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedShortLE())
		pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedShortLE())
	} else {
		buf.ReadUnsignedByte() // operator
		pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedByte())
		pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedByte())
		pos.Set(model.PrefixAdc+"3", buf.ReadUnsignedByte())
		pos.Set(model.PrefixAdc+"4", buf.ReadUnsignedByte())
	}

	pos.Set(model.KeyOdometer, buf.ReadUnsignedMediumLE())
	pos.Set(model.KeyDriverUniqueID, hex.EncodeToString(buf.ReadBytes(6)))

	buf.ReadUnsignedShortLE() // fix time
	buf.ReadUnsignedByte()    // location status
	buf.ReadUnsignedByte()    // mode 1
	buf.ReadUnsignedByte()    // mode 2

	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())

	pos.Valid = true

	if alternative {
		pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
		pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
	} else {
		pos.SetLongitude(radians(buf.ReadIntLE()))
		pos.SetLatitude(radians(buf.ReadIntLE()))
	}

	pos.Altitude = float64(buf.ReadIntLE()) / 100.0

	if alternative {
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedIntLE()))
		pos.Course = float64(buf.ReadUnsignedShortLE()) / 1000.0
	} else {
		pos.Speed = units.KnotsFromMps(float64(buf.ReadUnsignedIntLE()) / 100.0)
		pos.Course = float64(buf.ReadUnsignedShortLE()) / math.Pi * 180.0 / 1000.0
	}

	date := kit.NewDateBuilder(nil).
		SetTimeReverse(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		SetDateReverse(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedShortLE())
	pos.SetTime(date.Date())

	return pos
}

func decodeModular(m *ingest.Message, buf *kit.Buf, ds *ingest.DeviceSession) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	buf.ReadUnsignedByte()    // packet control
	buf.ReadUnsignedShortLE() // length
	buf.ReadUnsignedShortLE() // reserved
	buf.ReadUnsignedShortLE() // reserved

	for buf.ReadableBytes() > 3 {
		moduleType := buf.ReadUnsignedByte()
		endIndex := buf.ReadUnsignedShortLE() + buf.ReaderIndex()

		switch moduleType {
		case 2:
			buf.ReadUnsignedShortLE() // operator id
			buf.ReadUnsignedIntLE()   // pl signature
			count := buf.ReadUnsignedByte()
			for i := 0; i < count; i++ {
				id := buf.ReadUnsignedShortLE()
				buf.ReadUnsignedByte() // variable length
				pos.Set(model.PrefixIo+strconv.Itoa(id), buf.ReadUnsignedIntLE())
			}
		case 6:
			buf.ReadUnsignedByte() // hdop
			buf.ReadUnsignedByte() // mode 1
			buf.ReadUnsignedByte() // mode 2
			buf.ReadUnsignedByte() // satellites
			pos.SetLongitude(radians(buf.ReadIntLE()))
			pos.SetLatitude(radians(buf.ReadIntLE()))
			pos.Altitude = float64(buf.ReadIntLE()) / 100.0
			pos.Speed = units.KnotsFromMps(float64(buf.ReadUnsignedByte()) / 100.0)
			pos.Course = float64(buf.ReadUnsignedShortLE()) / math.Pi * 180.0 / 1000.0
		case 7:
			buf.ReadUnsignedByte() // valid
			date := kit.NewDateBuilder(nil).
				SetTimeReverse(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
				SetDateReverse(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
			pos.SetTime(date.Date())
		}

		buf.SetReaderIndex(endIndex)
	}
	return pos
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	alternative := buf.GetByte(buf.ReaderIndex()+3) != 'P'

	buf.Skip(4) // system code
	typ := buf.ReadUnsignedByte()

	deviceUniqueID := buf.ReadUnsignedIntLE()
	ds := m.Session(strconv.FormatInt(deviceUniqueID, 10))
	if ds == nil {
		return nil
	}

	if typ != msgClientSerial {
		buf.ReadUnsignedShortLE() // communication control
	}
	packetNumber := buf.ReadSignedByte()

	if typ == msgClientModularExt {
		sendModuleResponse(m, deviceUniqueID, packetNumber)
	} else {
		sendResponse(m, deviceUniqueID, packetNumber)
	}

	switch typ {
	case msgClientStatus:
		return decodeStatus(m, buf, ds, alternative)
	case msgClientModularExt:
		return decodeModular(m, buf, ds)
	}
	return nil
}

func encodeCommand(m *ingest.Message, deviceID int64, command, data1, data2 int) *kit.Buf {
	content := kit.Empty()
	content.WriteByte8(command)
	content.WriteByte8(command)
	content.WriteByte8(data1)
	content.WriteByte8(data1)
	content.WriteByte8(data2)
	content.WriteByte8(data2)
	content.WriteIntLE(0) // command specific data
	return encodeContent(0, int32(kit.Int(m.UniqueID(deviceID))), 0, content)
}

// encode is CellocatorProtocolEncoder. Like Java's Integer.parseInt, a unique id beyond
// the int range throws.
func encode(m *ingest.Message, c *model.Command) any {
	if c.Type != model.CommandOutputControl {
		return nil
	}
	data := kit.CommandString(c, model.CommandKeyData)
	if data == nil {
		kit.Throw("NumberFormatException: Cannot parse null string: null")
	}
	value := int(int32(kit.Int(*data))<<4) + kit.CommandInt(c, model.CommandKeyIndex)
	return encodeCommand(m, c.DeviceID, 0x03, value, 0)
}
