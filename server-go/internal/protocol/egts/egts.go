// Package egts is the EGTS protocol (EgtsProtocol, GOST R 54619): binary transport
// packets with service records, over TCP.
package egts

import (
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
		Name: "egts",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: NewDecoder()}
		}}},
	}
}

// Framer is EgtsFrameDecoder: header length plus frame length plus its checksum.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}
		headerLength := buf.GetUnsignedByte(buf.ReaderIndex() + 3)
		frameLength := buf.GetUnsignedShortLE(buf.ReaderIndex() + 5)
		length := headerLength + frameLength
		if frameLength > 0 {
			length += 2
		}
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

// Packet types.
const (
	ptResponse = 0
	ptAppData  = 1
)

// Service types.
const serviceTeledata = 2

// Subrecord types.
const (
	msgRecordResponse    = 0
	msgTermIdentity      = 1
	msgResultCode        = 7
	msgPosData           = 16
	msgExtPosData        = 17
	msgADSensorsData     = 18
	msgLiquidLevelSensor = 27
	egtsEpochSeconds     = 1262304000 // 2010-01-01
	coordinateFullScale  = 0xFFFFFFFF
	responseHeaderLength = 5 + 2 + 2 + 2
	protocolVersion      = 1
)

// Decoder is EgtsProtocolDecoder: devices identify by the record's object id until a
// terminal identity arrives, and responses are numbered per connection.
type Decoder struct {
	useObjectIDAsDeviceID bool
	packetID              int
}

// NewDecoder returns a decoder in its initial state.
func NewDecoder() *Decoder { return &Decoder{useObjectIDAsDeviceID: true} }

func (d *Decoder) sendResponse(m *ingest.Message, packetType, index, serviceType, typ int, content *kit.Buf) {
	data := kit.Empty()
	data.WriteByte8(typ)
	data.WriteShortLE(content.ReadableBytes())
	data.WriteBuf(content)

	record := kit.Empty()
	if packetType == ptResponse {
		record.WriteShortLE(index)
		record.WriteByte8(0) // success
	}
	record.WriteShortLE(data.ReadableBytes())
	record.WriteShortLE(0)
	record.WriteByte8(0) // flags (possibly 1 << 6)
	record.WriteByte8(serviceType)
	record.WriteByte8(serviceType)
	record.WriteBuf(data)
	recordChecksum := kit.CRC16(kit.CRC16_CCITT_FALSE, record.Bytes())

	response := kit.Empty()
	response.WriteByte8(protocolVersion)      // protocol version
	response.WriteByte8(0)                    // security key id
	response.WriteByte8(0)                    // flags
	response.WriteByte8(responseHeaderLength) // header length
	response.WriteByte8(0)                    // encoding
	response.WriteShortLE(record.ReadableBytes())
	response.WriteShortLE(d.packetID)
	d.packetID++
	response.WriteByte8(packetType)
	response.WriteByte8(kit.CRC8(kit.CRC8_EGTS, response.Bytes()))
	response.WriteBuf(record)
	response.WriteShortLE(recordChecksum)

	m.Send(response)
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	var positions []*model.Position

	headerLength := buf.GetUnsignedByte(buf.ReaderIndex() + 3)
	index := buf.GetUnsignedShort(buf.ReaderIndex() + 5 + 2)
	packetType := buf.GetUnsignedByte(buf.ReaderIndex() + 5 + 2 + 2)
	buf.Skip(headerLength)

	if packetType == ptResponse {
		return nil
	}

	var objectID int64
	for buf.ReadableBytes() > 2 {
		length := buf.ReadUnsignedShortLE()
		recordIndex := buf.ReadUnsignedShortLE()
		recordFlags := int64(buf.ReadUnsignedByte())

		if kit.Check(recordFlags, 0) {
			objectID = buf.ReadUnsignedIntLE()
		}
		if kit.Check(recordFlags, 1) {
			buf.ReadUnsignedIntLE() // event id
		}
		if kit.Check(recordFlags, 2) {
			buf.ReadUnsignedIntLE() // time
		}

		serviceType := buf.ReadUnsignedByte()
		buf.ReadUnsignedByte() // recipient service type

		recordEnd := buf.ReaderIndex() + length

		pos := m.NewPosition()
		ds := m.Session()
		if ds != nil {
			pos.DeviceID = ds.DeviceID
		}

		response := kit.Empty()
		response.WriteShortLE(recordIndex)
		response.WriteByte8(0) // success
		d.sendResponse(m, ptResponse, index, serviceType, msgRecordResponse, response)

		for buf.ReaderIndex() < recordEnd {
			typ := buf.ReadUnsignedByte()
			end := buf.ReadUnsignedShortLE() + buf.ReaderIndex()

			switch typ {
			case msgTermIdentity:
				d.useObjectIDAsDeviceID = false

				buf.ReadUnsignedIntLE() // object id
				flags := int64(buf.ReadUnsignedByte())

				if kit.Check(flags, 0) {
					buf.ReadUnsignedShortLE() // home dispatcher identifier
				}
				if kit.Check(flags, 1) {
					m.Session(kit.Trim(buf.ReadString(15)))
				}
				if kit.Check(flags, 2) {
					m.Session(kit.Trim(buf.ReadString(16)))
				}
				if kit.Check(flags, 3) {
					buf.Skip(3) // language identifier
				}
				if kit.Check(flags, 5) {
					buf.Skip(3) // network identifier
				}
				if kit.Check(flags, 6) {
					buf.ReadUnsignedShortLE() // buffer size
				}
				if kit.Check(flags, 7) {
					m.Session(kit.Trim(buf.ReadString(15)))
				}

				d.sendResponse(m, ptAppData, 0, serviceType, msgResultCode, kit.Empty().WriteByte8(0)) // success

			case msgPosData:
				pos.SetTime(time.UnixMilli((buf.ReadUnsignedIntLE() + egtsEpochSeconds) * 1000))
				pos.SetLatitude(float64(buf.ReadUnsignedIntLE()) * 90.0 / coordinateFullScale)
				pos.SetLongitude(float64(buf.ReadUnsignedIntLE()) * 180.0 / coordinateFullScale)

				flags := int64(buf.ReadUnsignedByte())
				pos.Valid = kit.Check(flags, 0)
				if kit.Check(flags, 5) {
					pos.SetLatitude(-pos.Latitude)
				}
				if kit.Check(flags, 6) {
					pos.SetLongitude(-pos.Longitude)
				}

				speed := int64(buf.ReadUnsignedShortLE())
				pos.Speed = units.KnotsFromKph(float64(kit.To(speed, 14)) / 10.0)
				course := buf.ReadUnsignedByte()
				if kit.Check(speed, 15) {
					course += 0x100
				}
				pos.Course = float64(course)

				pos.Set(model.KeyOdometer, buf.ReadUnsignedMediumLE()*100)
				pos.Set(model.KeyInput, buf.ReadUnsignedByte())
				pos.Set(model.KeyEvent, buf.ReadUnsignedByte())

				if kit.Check(flags, 7) {
					pos.Altitude = float64(buf.ReadMediumLE())
				}

			case msgExtPosData:
				flags := int64(buf.ReadUnsignedByte())
				if kit.Check(flags, 0) {
					pos.Set(model.KeyVdop, buf.ReadUnsignedShortLE())
				}
				if kit.Check(flags, 1) {
					pos.Set(model.KeyHdop, buf.ReadUnsignedShortLE())
				}
				if kit.Check(flags, 2) {
					pos.Set(model.KeyPdop, buf.ReadUnsignedShortLE())
				}
				if kit.Check(flags, 3) {
					pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
				}

			case msgADSensorsData:
				inputMask := int64(buf.ReadUnsignedByte())
				pos.Set(model.KeyOutput, buf.ReadUnsignedByte())
				adcMask := int64(buf.ReadUnsignedByte())
				for i := 0; i < 8; i++ {
					if kit.Check(inputMask, i) {
						buf.ReadUnsignedByte() // input
					}
				}
				for i := 0; i < 8; i++ {
					if kit.Check(adcMask, i) {
						pos.Set(model.PrefixAdc+strconv.Itoa(i+1), buf.ReadUnsignedMediumLE())
					}
				}

			case msgLiquidLevelSensor:
				flags := int64(buf.ReadUnsignedByte())
				buf.ReadUnsignedShortLE() // address
				if kit.Check(flags, 3) {
					pos.Set("liquidRaw", buf.ReadSlice(end-buf.ReaderIndex()).HexDump())
				} else {
					pos.Set("liquid", buf.ReadUnsignedIntLE())
				}
			}

			buf.SetReaderIndex(end)
		}

		if serviceType == serviceTeledata && pos.Valid {
			if d.useObjectIDAsDeviceID && objectID != 0 {
				ds = m.Session(strconv.FormatInt(objectID, 10))
				if ds != nil {
					pos.DeviceID = ds.DeviceID
				}
			}
			if ds != nil {
				positions = append(positions, pos)
			}
		}
	}

	if len(positions) == 0 {
		return nil
	}
	return positions
}
