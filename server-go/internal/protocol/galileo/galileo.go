// Package galileo is the Galileo protocol (GalileoProtocol): binary tagged records over
// TCP, Iridium packets, compressed minimal data sets and photos.
package galileo

import (
	"encoding/hex"
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "galileo",
		DataCommands: protocol.DataCommands("galileo"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: &Decoder{},
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

const messageMinimumLength = 6

// Framer is GalileoFrameDecoder: Iridium packets with a big-endian length, others with a
// little-endian 15-bit length and a checksum.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < messageMinimumLength {
			return nil
		}
		var length int
		if buf.GetByte(buf.ReaderIndex()) == 0x01 && buf.GetUnsignedMedium(buf.ReaderIndex()+3) == 0x01001c {
			length = 3 + buf.GetUnsignedShort(buf.ReaderIndex()+1)
		} else {
			length = 5 + (buf.GetUnsignedShortLE(buf.ReaderIndex()+1) & 0x7fff)
		}
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

// Decoder is GalileoProtocolDecoder. Compressed is the default of galileo.extended
// (setCompressed); the decoder keeps the photo being received.
type Decoder struct {
	Compressed bool

	media *kit.Buf
}

func (d *Decoder) compressed(m *ingest.Message, deviceID int64) bool {
	if v := m.LookupBool(deviceID, m.Protocol()+".extended", false, true); v != nil {
		return *v
	}
	return d.Compressed
}

var tagLengths = map[int]int{}

func addTagLength(length int, tags ...int) {
	for _, tag := range tags {
		tagLengths[tag] = length
	}
}

func init() {
	addTagLength(1,
		0x01, 0x02, 0x35, 0x43, 0xc4, 0xc5, 0xc6, 0xc7,
		0xc8, 0xc9, 0xca, 0xcb, 0xcc, 0xcd, 0xce, 0xcf,
		0xd0, 0xd1, 0xd2, 0xd5, 0x88, 0x89, 0x8a, 0x8b, 0x8c,
		0xa0, 0xaf, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6,
		0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae)
	addTagLength(2,
		0x04, 0x10, 0x34, 0x40, 0x41, 0x42, 0x45, 0x46,
		0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x60, 0x61,
		0x62, 0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76,
		0x77, 0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6,
		0xb7, 0xb8, 0xb9, 0xd6, 0xd7, 0xd8, 0xd9, 0xda)
	addTagLength(3,
		0x63, 0x64, 0x6f, 0x5d, 0x65, 0x66, 0x67, 0x68,
		0x69, 0x6a, 0x6b, 0x6c, 0x6d, 0x6e, 0xfa,
		0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87)
	addTagLength(4,
		0x20, 0x33, 0x44, 0x90, 0xc0, 0xc2, 0xc3, 0xd3,
		0xd4, 0xdb, 0xdc, 0xdd, 0xde, 0xdf, 0xf0, 0xf9,
		0x5a, 0x47, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6,
		0xf7, 0xf8, 0xe2, 0xe9)
	tagLengths[0x5b] = 7 // variable length
	tagLengths[0x5c] = 68
	tagLengths[0xfd] = 8
}

func tagLength(tag int) int {
	length, ok := tagLengths[tag]
	if !ok {
		kit.Throw("IllegalArgumentException: %s", kit.Format("Unknown tag: 0x%02x", tag))
	}
	return length
}

func sendResponse(m *ingest.Message, header, checksum int) {
	reply := kit.Empty()
	reply.WriteByte8(header)
	reply.WriteShortLE(int(int16(checksum)))
	m.Send(reply)
}

func itoa(i int) string { return strconv.Itoa(i) }

func decodeTag(pos *model.Position, buf *kit.Buf, tag int) {
	switch {
	case tag >= 0x50 && tag <= 0x57:
		pos.Set(model.PrefixAdc+itoa(tag-0x50), buf.ReadUnsignedShortLE())
	case tag >= 0x60 && tag <= 0x62:
		pos.Set("fuel"+itoa(tag-0x60), buf.ReadUnsignedShortLE())
	case tag >= 0xa0 && tag <= 0xaf:
		pos.Set("can8BitR"+itoa(tag-0xa0+15), buf.ReadUnsignedByte())
	case tag >= 0xb0 && tag <= 0xb9:
		pos.Set("can16BitR"+itoa(tag-0xb0+5), buf.ReadUnsignedShortLE())
	case tag >= 0xc4 && tag <= 0xd2:
		pos.Set("can8BitR"+itoa(tag-0xc4), buf.ReadUnsignedByte())
	case tag >= 0xd6 && tag <= 0xda:
		pos.Set("can16BitR"+itoa(tag-0xd6), buf.ReadUnsignedShortLE())
	case tag >= 0xdb && tag <= 0xdf:
		pos.Set("can32BitR"+itoa(tag-0xdb), buf.ReadUnsignedIntLE())
	case tag >= 0xe2 && tag <= 0xe9:
		pos.Set("userData"+itoa(tag-0xe2), buf.ReadUnsignedIntLE())
	case tag >= 0xf0 && tag <= 0xf9:
		pos.Set("can32BitR"+itoa(tag-0xf0+5), buf.ReadUnsignedIntLE())
	default:
		decodeTagOther(pos, buf, tag)
	}
}

func decodeTagOther(pos *model.Position, buf *kit.Buf, tag int) {
	switch tag {
	case 0x01:
		pos.Set(model.KeyVersionHw, buf.ReadUnsignedByte())
	case 0x02:
		pos.Set(model.KeyVersionFw, buf.ReadUnsignedByte())
	case 0x04:
		pos.Set("deviceId", buf.ReadUnsignedShortLE())
	case 0x10:
		pos.Set(model.KeyIndex, buf.ReadUnsignedShortLE())
	case 0x20:
		pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
	case 0x33:
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()) / 10.0)
		pos.Course = float64(buf.ReadUnsignedShortLE()) / 10.0
	case 0x34:
		pos.Altitude = float64(buf.ReadShortLE())
	case 0x35:
		pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
	case 0x40:
		pos.Set(model.KeyStatus, buf.ReadUnsignedShortLE())
	case 0x41:
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/1000.0)
	case 0x42:
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)
	case 0x43:
		pos.Set(model.KeyDeviceTemp, int(buf.ReadSignedByte()))
	case 0x44:
		pos.Set(model.KeyAcceleration, buf.ReadUnsignedIntLE())
	case 0x45:
		pos.Set(model.KeyOutput, buf.ReadUnsignedShortLE())
	case 0x46:
		pos.Set(model.KeyInput, buf.ReadUnsignedShortLE())
	case 0x48:
		pos.Set("statusExtended", buf.ReadUnsignedShortLE())
	case 0x58:
		pos.Set("rs2320", buf.ReadUnsignedShortLE())
	case 0x59:
		pos.Set("rs2321", buf.ReadUnsignedShortLE())
	case 0x90:
		pos.Set(model.KeyDriverUniqueID, strconv.FormatInt(buf.ReadUnsignedIntLE(), 10))
	case 0xc0:
		pos.Set("fuelTotal", float64(buf.ReadUnsignedIntLE())*0.5)
	case 0xc1:
		pos.Set(model.KeyFuel, float64(buf.ReadUnsignedByte())*0.4)
		pos.Set(model.PrefixTemp+"1", buf.ReadUnsignedByte()-40)
		pos.Set(model.KeyRpm, float64(buf.ReadUnsignedShortLE())*0.125)
	case 0xc2:
		pos.Set("canB0", buf.ReadUnsignedIntLE())
	case 0xc3:
		pos.Set("canB1", buf.ReadUnsignedIntLE())
	case 0xd4:
		pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
	case 0xe0:
		pos.Set(model.KeyIndex, buf.ReadUnsignedIntLE())
	case 0xe1:
		pos.Set(model.KeyResult, buf.ReadString(buf.ReadUnsignedByte()))
	case 0xea:
		pos.Set("userDataArray", hex.EncodeToString(buf.ReadBytes(buf.ReadUnsignedByte())))
	case 0xfe:
		buf.Skip(buf.ReadUnsignedShortLE())
	default:
		buf.Skip(tagLength(tag))
	}
}

// Decode is GalileoProtocolDecoder.decode.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	switch buf.ReadUnsignedByte() {
	case 0x01:
		if buf.GetUnsignedMedium(buf.ReaderIndex()+2) == 0x01001c {
			if pos := d.decodeIridiumPosition(m, buf); pos != nil {
				return pos
			}
			return nil
		}
		if positions := decodePositions(m, buf); positions != nil {
			return positions
		}
		return nil
	case 0x07:
		if pos := d.decodePhoto(m, buf); pos != nil {
			return pos
		}
		return nil
	case 0x08:
		if positions := decodeCompressedPositions(m, buf); positions != nil {
			return positions
		}
		return nil
	}
	return nil
}

func decodeMinimalDataSet(m *ingest.Message, pos *model.Position, buf *kit.Buf) {
	bits := kit.NewBitBuffer(buf.ReadSlice(10))
	bits.ReadUnsigned(1)

	year := time.Date(m.Now.UTC().Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	pos.SetTime(year.Add(time.Duration(bits.ReadUnsigned(25)) * time.Second))

	pos.Valid = bits.ReadUnsigned(1) == 0
	pos.SetLongitude(float64(360*bits.ReadUnsigned(22))/4194304.0 - 180)
	pos.SetLatitude(float64(180*bits.ReadUnsigned(21))/2097152.0 - 90)
	if bits.ReadUnsigned(1) > 0 {
		pos.AddAlarm(model.AlarmGeneral)
	}
}

func decodeTags(pos *model.Position, buf *kit.Buf) {
	tags := make([]int, kit.ToInt(buf.ReadUnsignedByte(), 8))
	for i := range tags {
		tags[i] = buf.ReadUnsignedByte()
	}
	for _, tag := range tags {
		decodeTag(pos, buf, tag)
	}
}

func (d *Decoder) decodeIridiumPosition(m *ingest.Message, buf *kit.Buf) *model.Position {
	buf.ReadUnsignedShort() // length
	buf.Skip(3)             // identification header
	buf.ReadUnsignedInt()   // index

	ds := m.Session(buf.ReadString(15))
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	buf.ReadUnsignedByte() // session status
	buf.Skip(4)            // reserved
	pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))

	buf.Skip(3) // coordinates header
	flags := buf.ReadUnsignedByte()
	latitude := float64(buf.ReadUnsignedByte()) + float64(buf.ReadUnsignedShort())/60000.0
	longitude := float64(buf.ReadUnsignedByte()) + float64(buf.ReadUnsignedShort())/60000.0
	if kit.Check(int64(flags), 1) {
		latitude = -latitude
	}
	if kit.Check(int64(flags), 0) {
		longitude = -longitude
	}
	pos.SetLatitude(latitude)
	pos.SetLongitude(longitude)
	buf.ReadUnsignedInt() // accuracy

	buf.ReadUnsignedByte() // data tag header
	data := buf.ReadSlice(buf.ReadUnsignedShort())
	if d.compressed(m, ds.DeviceID) {
		decodeMinimalDataSet(m, pos, data)
		decodeTags(pos, data)
	} else {
		for data.IsReadable() {
			tag := data.ReadUnsignedByte()
			if tag == 0x30 {
				pos.Valid = data.ReadUnsignedByte()&0xf0 == 0x00
				pos.SetLatitude(float64(data.ReadIntLE()) / 1000000.0)
				pos.SetLongitude(float64(data.ReadIntLE()) / 1000000.0)
			} else {
				decodeTag(pos, data, tag)
			}
		}
	}
	return pos
}

func decodePositions(m *ingest.Message, buf *kit.Buf) []*model.Position {
	endIndex := (buf.ReadUnsignedShortLE() & 0x7fff) + buf.ReaderIndex()

	var positions []*model.Position
	tags := map[int]bool{}
	hasLocation := false

	var ds *ingest.DeviceSession
	pos := m.NewPosition()

	for buf.ReaderIndex() < endIndex {
		tag := buf.ReadUnsignedByte()
		if tags[tag] {
			if hasLocation && !pos.FixTime.IsZero() {
				positions = append(positions, pos)
			}
			clear(tags)
			hasLocation = false
			pos = m.NewPosition() // new position starts
		}
		tags[tag] = true

		switch tag {
		case 0x03:
			ds = m.Session(buf.ReadString(15))
		case 0x30:
			hasLocation = true
			pos.Valid = buf.ReadUnsignedByte()&0xf0 == 0x00
			pos.SetLatitude(float64(buf.ReadIntLE()) / 1000000.0)
			pos.SetLongitude(float64(buf.ReadIntLE()) / 1000000.0)
		default:
			decodeTag(pos, buf, tag)
		}
	}

	if ds == nil {
		ds = m.Session()
		if ds == nil {
			return nil
		}
	}

	if hasLocation && !pos.FixTime.IsZero() {
		positions = append(positions, pos)
	} else if _, ok := pos.Attributes.Get(model.KeyResult); ok {
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		positions = append(positions, pos)
	}

	sendResponse(m, 0x02, buf.ReadUnsignedShortLE())

	for _, p := range positions {
		p.DeviceID = ds.DeviceID
	}
	return positions
}

func (d *Decoder) decodePhoto(m *ingest.Message, buf *kit.Buf) *model.Position {
	length := buf.ReadUnsignedShortLE()

	var pos *model.Position
	if d.media == nil {
		d.media = m.NewMediaBuffer()
	}
	buf.ReadUnsignedByte() // part number

	if length > 1 {
		d.media.WriteBytes(buf.ReadBytes(length - 1))
	} else {
		ds := m.Session()
		if ds == nil {
			kit.Throw("NullPointerException: deviceSession")
		}
		pos = m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		media := d.media
		d.media = nil
		pos.Set(model.KeyImage, m.WriteMedia(ds.UniqueID, "jpg", media.Bytes()))
	}

	sendResponse(m, 0x07, buf.ReadUnsignedShortLE())
	return pos
}

func decodeCompressedPositions(m *ingest.Message, buf *kit.Buf) []*model.Position {
	buf.ReadUnsignedShortLE() // length

	ds := m.Session()
	if ds == nil {
		return nil
	}

	var positions []*model.Position
	for buf.ReadableBytes() > 2 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		decodeMinimalDataSet(m, pos, buf)
		decodeTags(pos, buf)
		positions = append(positions, pos)
	}

	sendResponse(m, 0x02, buf.ReadUnsignedShortLE())
	return positions
}

func encodeText(uniqueID, text string) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8(0x01)
	buf.WriteShortLE(kit.Len(uniqueID) + kit.Len(text) + 11)

	buf.WriteByte8(0x03) // imei tag
	buf.WriteBytes(kit.ASCIIBytes(uniqueID))

	buf.WriteByte8(0x04) // device id tag
	buf.WriteShortLE(0)  // not needed if imei provided

	buf.WriteByte8(0xE0) // index tag
	buf.WriteIntLE(0)    // index

	buf.WriteByte8(0xE1) // command text tag
	buf.WriteByte8(kit.Len(text))
	buf.WriteBytes(kit.ASCIIBytes(text))

	buf.WriteShortLE(kit.CRC16(kit.CRC16_MODBUS, buf.Bytes()))
	return buf
}

func encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandCustom:
		data := kit.CommandString(c, model.CommandKeyData)
		if data == nil {
			kit.Throw("NullPointerException: data")
		}
		return encodeText(m.UniqueID(c.DeviceID), *data)
	case model.CommandOutputControl:
		data := "null"
		if v := kit.CommandString(c, model.CommandKeyData); v != nil {
			data = *v
		}
		return encodeText(m.UniqueID(c.DeviceID),
			"Out "+itoa(kit.CommandInt(c, model.CommandKeyIndex))+","+data)
	}
	return nil
}
