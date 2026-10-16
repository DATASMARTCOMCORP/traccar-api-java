// Package ruptela is the Ruptela protocol (RuptelaProtocol): length-prefixed binary
// messages with an IMEI, records of typed IO values, files and command responses.
package ruptela

import (
	"regexp"
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
		Name:         "ruptela",
		DataCommands: protocol.DataCommands("ruptela"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			d := &Decoder{}
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 0, 2, 2, 0),
				Decoder: d,
				Encoder: ingest.CommandEncoderFunc(encode),
				OnClose: func() { d.media = nil },
			}
		}}},
	}
}

const (
	msgRecords             = 1
	msgDeviceConfiguration = 2
	msgDeviceVersion       = 3
	msgFirmwareUpdate      = 4
	msgSetConnection       = 5
	msgSetOdometer         = 6
	msgSMSViaGPRSResponse  = 7
	msgSMSViaGPRS          = 8
	msgDTCs                = 9
	msgIdentification      = 15
	msgHeartbeat           = 16
	msgSetIO               = 17
	msgFiles               = 37
	msgExtendedRecords     = 68
)

// Decoder is RuptelaProtocolDecoder: it keeps the media buffer of a file transfer.
type Decoder struct {
	media *kit.Buf
}

// encodeContent is RuptelaProtocolEncoder.encodeContent: length, type + 100, content and
// a CRC16 (Kermit).
func encodeContent(typ int, content *kit.Buf) *kit.Buf {
	buf := kit.Empty()
	buf.WriteShort(1 + content.ReadableBytes())
	buf.WriteByte8(100 + typ)
	buf.WriteBuf(content)
	buf.WriteShort(kit.CRC16(kit.CRC16_KERMIT, buf.Array()[2:buf.WriterIndex()]))
	return buf
}

func decodeCommandResponse(m *ingest.Message, ds *ingest.DeviceSession, typ int, buf *kit.Buf) any {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)
	pos.Set(model.KeyType, typ)

	switch typ {
	case msgDeviceConfiguration, msgDeviceVersion, msgFirmwareUpdate, msgSMSViaGPRSResponse:
		pos.Set(model.KeyResult, kit.Trim(buf.GetString(buf.ReaderIndex(), buf.ReadableBytes()-2)))
		return pos
	case msgSetIO:
		pos.Set(model.KeyResult, strconv.Itoa(buf.ReadUnsignedByte()))
		return pos
	}
	return nil
}

func readValue(buf *kit.Buf, length int, signed bool) int64 {
	switch length {
	case 1:
		if signed {
			return int64(buf.ReadSignedByte())
		}
		return int64(buf.ReadUnsignedByte())
	case 2:
		if signed {
			return int64(buf.ReadShort())
		}
		return int64(buf.ReadUnsignedShort())
	case 4:
		if signed {
			return int64(buf.ReadInt())
		}
		return buf.ReadUnsignedInt()
	}
	return buf.ReadLong()
}

// removeLong is Position.removeLong for the Long values this decoder stores.
func removeLong(pos *model.Position, key string) *int64 {
	v, ok := pos.Attributes.Get(key)
	if !ok {
		return nil
	}
	pos.Attributes.Delete(key)
	var r int64
	switch n := v.(type) {
	case int64:
		r = n
	case int:
		r = int64(n)
	case float64:
		r = int64(n)
	case string:
		r = kit.Long(n)
	default:
		return nil
	}
	return &r
}

func decodeDriver(pos *model.Position, part1, part2 string) {
	driverIDPart1 := removeLong(pos, part1)
	driverIDPart2 := removeLong(pos, part2)
	if driverIDPart1 != nil && driverIDPart2 != nil {
		driverID := kit.Empty().WriteLong(*driverIDPart1).WriteLong(*driverIDPart2)
		pos.Set(model.KeyDriverUniqueID, kit.Latin1(driverID.Bytes()))
	}
}

func addAlarmIf(pos *model.Position, condition bool, alarm string) {
	if condition {
		pos.AddAlarm(alarm)
	}
}

func decodeParameter(pos *model.Position, id int, buf *kit.Buf, length int) {
	value := func(signed bool) int64 { return readValue(buf, length, signed) }
	switch id {
	case 2, 3, 4, 5:
		pos.Set(model.PrefixIn+strconv.Itoa(id-1), value(false))
	case 13, 173:
		pos.Set(model.KeyMotion, value(false) > 0)
	case 20:
		pos.Set(model.PrefixAdc+"3", value(false))
	case 21:
		pos.Set(model.PrefixAdc+"4", value(false))
	case 22:
		pos.Set(model.PrefixAdc+"1", value(false))
	case 23:
		pos.Set(model.PrefixAdc+"2", value(false))
	case 29:
		pos.Set(model.KeyPower, float64(value(false))/1000.0)
	case 30:
		pos.Set(model.KeyBattery, float64(value(false))/1000.0)
	case 32:
		pos.Set(model.KeyDeviceTemp, value(true))
	case 34:
		pos.Set(model.KeyDriverUniqueID, buf.ReadSlice(length).HexDump())
	case 39:
		pos.Set(model.KeyEngineLoad, value(false))
	case 65:
		pos.Set(model.KeyOdometer, value(false))
	case 74:
		pos.Set(model.PrefixTemp+"3", float64(value(true))/10.0)
	case 78, 79, 80:
		pos.Set(model.PrefixTemp+strconv.Itoa(id-78), float64(value(true))/10.0)
	case 88:
		addAlarmIf(pos, value(false) > 0, model.AlarmJamming)
	case 94:
		pos.Set(model.KeyRpm, float64(value(false))*0.25)
	case 95:
		pos.Set(model.KeyObdSpeed, value(false))
	case 98:
		pos.Set(model.KeyFuel, float64(value(false)*100)/255.0)
	case 100:
		pos.Set(model.KeyFuelConsumption, float64(value(false))/20.0)
	case 134:
		addAlarmIf(pos, value(false) > 0, model.AlarmBraking)
	case 136:
		addAlarmIf(pos, value(false) > 0, model.AlarmAcceleration)
	case 150:
		pos.Set(model.KeyOperator, value(false))
	case 163:
		pos.Set(model.KeyOdometer, value(false)*5)
	case 164:
		pos.Set(model.KeyOdometerTrip, value(false)*5)
	case 165:
		pos.Set(model.KeyObdSpeed, float64(value(false))/256.0)
	case 166, 197:
		pos.Set(model.KeyRpm, float64(value(false))*0.125)
	case 170:
		pos.Set(model.KeyCharge, value(false) > 0)
	case 205:
		pos.Set(model.KeyFuel, value(false))
	case 207:
		pos.Set(model.KeyFuelLevel, float64(value(false))*0.4)
	case 208:
		pos.Set(model.KeyFuelUsed, float64(value(false))*0.5)
	case 251, 409:
		pos.Set(model.KeyIgnition, value(false) > 0)
	case 410:
		addAlarmIf(pos, value(false) > 0, model.AlarmTow)
	case 411:
		addAlarmIf(pos, value(false) > 0, model.AlarmAccident)
	case 415:
		addAlarmIf(pos, value(false) == 0, model.AlarmGpsAntennaCut)
	case 645:
		pos.Set(model.KeyObdOdometer, value(false)*1000)
	case 758:
		addAlarmIf(pos, value(false) == 1, model.AlarmTampering)
	default:
		pos.Set(model.PrefixIo+strconv.Itoa(id), value(false))
	}
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedShort() // data length

	imei := kit.Format("%015d", buf.ReadLong())
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	typ := buf.ReadUnsignedByte()

	switch typ {
	case msgRecords, msgExtendedRecords:
		return decodeRecords(m, ds, typ, buf)

	case msgDTCs:
		positions := []*model.Position{}
		count := buf.ReadUnsignedByte()
		for range count {
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID

			buf.ReadUnsignedByte() // reserved

			pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
			pos.Valid = true
			pos.SetLongitude(float64(buf.ReadInt()) / 10000000.0)
			pos.SetLatitude(float64(buf.ReadInt()) / 10000000.0)

			if buf.ReadUnsignedByte() == 2 {
				pos.Set(model.KeyArchive, true)
			}

			pos.Set(model.KeyDtcs, buf.ReadString(5))

			positions = append(positions, pos)
		}
		m.Send(kit.Hex("00026d01c4a4"))
		return positions

	case msgFiles:
		subtype := buf.ReadUnsignedByte()
		source := buf.ReadUnsignedByte()

		if subtype == 2 {
			filename := buf.ReadSlice(8)
			total := buf.ReadUnsignedShort()
			current := buf.ReadUnsignedShort()
			if d.media == nil {
				d.media = m.NewMediaBuffer()
			}
			d.media.WriteBytes(buf.ReadBytes(buf.ReadableBytes() - 2))
			if current < total-1 {
				content := kit.Empty()
				content.WriteByte8(subtype)
				content.WriteByte8(source)
				content.WriteBuf(filename)
				content.WriteShort(current + 1)
				m.Send(encodeContent(typ, content))
			} else {
				pos := m.NewPosition()
				pos.DeviceID = ds.DeviceID
				m.LastLocation(pos, nil)
				media := d.media
				d.media = nil
				pos.Set(model.KeyImage, m.WriteMedia(imei, "jpg", media.Bytes()))
				return pos
			}
		}
		return nil

	case msgIdentification, msgHeartbeat:
		content := kit.Empty()
		content.WriteByte8(1)
		m.Send(encodeContent(typ, content))
		return nil
	}

	return decodeCommandResponse(m, ds, typ, buf)
}

func decodeRecords(m *ingest.Message, ds *ingest.DeviceSession, typ int, buf *kit.Buf) any {
	positions := []*model.Position{}

	buf.ReadUnsignedByte() // records left
	count := buf.ReadUnsignedByte()

	for range count {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
		buf.ReadUnsignedByte() // timestamp extension

		if typ == msgExtendedRecords {
			recordExtension := buf.ReadUnsignedByte()
			mergeRecordCount := kit.FromInt(recordExtension, 4)
			currentRecord := kit.ToInt(recordExtension, 4)

			if currentRecord > 0 && currentRecord <= mergeRecordCount {
				if len(positions) == 0 {
					m.LastLocation(pos, nil)
				} else {
					pos = positions[len(positions)-1]
					positions = positions[:len(positions)-1]
				}
			}
		}

		buf.ReadUnsignedByte() // priority (reserved)

		longitude := buf.ReadInt()
		latitude := buf.ReadInt()
		if longitude > -1<<31 && latitude > -1<<31 {
			pos.Valid = true
			pos.SetLongitude(float64(longitude) / 10000000.0)
			pos.SetLatitude(float64(latitude) / 10000000.0)
			pos.Altitude = float64(buf.ReadUnsignedShort()) / 10.0
			pos.Course = float64(buf.ReadUnsignedShort()) / 100.0
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))
			pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
		} else {
			buf.Skip(8)
			m.LastLocation(pos, nil)
		}

		if typ == msgExtendedRecords {
			pos.Set(model.KeyEvent, buf.ReadUnsignedShort())
		} else {
			pos.Set(model.KeyEvent, buf.ReadUnsignedByte())
		}

		// 1, 2, 4 and 8 byte data
		for _, length := range []int{1, 2, 4, 8} {
			valueCount := buf.ReadUnsignedByte()
			for range valueCount {
				var id int
				if typ == msgExtendedRecords {
					id = buf.ReadUnsignedShort()
				} else {
					id = buf.ReadUnsignedByte()
				}
				decodeParameter(pos, id, buf, length)
			}
		}

		decodeDriver(pos, model.PrefixIo+"126", model.PrefixIo+"127") // can driver
		decodeDriver(pos, model.PrefixIo+"155", model.PrefixIo+"156") // tco driver

		tagIDPart1 := removeLong(pos, model.PrefixIo+"760")
		tagIDPart2 := removeLong(pos, model.PrefixIo+"761")
		if tagIDPart1 != nil && tagIDPart2 != nil {
			pos.Set("tagId", strconv.FormatUint(uint64(*tagIDPart1), 16)+strconv.FormatUint(uint64(*tagIDPart2), 16))
		}

		positions = append(positions, pos)
	}

	m.Send(kit.Hex("0002640113bc"))
	return positions
}

var hexData = regexp.MustCompile(`^([0-9A-Fa-f]{2})+$`)

func commandString(c *model.Command, key string) string {
	if s := kit.CommandString(c, key); s != nil {
		return *s
	}
	return "null"
}

// encode is RuptelaProtocolEncoder.
func encode(_ *ingest.Message, c *model.Command) any {
	content := kit.Empty()
	switch c.Type {
	case model.CommandCustom:
		data := kit.CommandString(c, model.CommandKeyData)
		if data == nil {
			kit.Throw("NullPointerException: data")
		}
		if hexData.MatchString(*data) {
			content.WriteBytes(kit.ParseHex(*data))
			return content
		}
		content.WriteBytes(kit.ASCIIBytes(*data))
		return encodeContent(msgSMSViaGPRS, content)
	case model.CommandEngineStop:
		content.WriteBytes(kit.ASCIIBytes("pass immobilizer 10"))
		return encodeContent(msgSMSViaGPRS, content)
	case model.CommandEngineResume:
		content.WriteBytes(kit.ASCIIBytes("pass resetimmob"))
		return encodeContent(msgSMSViaGPRS, content)
	case model.CommandRequestPhoto:
		content.WriteByte8(1)       // sub-command
		content.WriteByte8(0)       // source
		content.WriteInt(0)         // start timestamp
		content.WriteInt(1<<31 - 1) // end timestamp
		return encodeContent(msgFiles, content)
	case model.CommandConfiguration:
		content.WriteBytes(kit.ASCIIBytes(commandString(c, model.CommandKeyData) + "\r\n"))
		return encodeContent(msgDeviceConfiguration, content)
	case model.CommandGetVersion:
		return encodeContent(msgDeviceVersion, content)
	case model.CommandFirmwareUpdate:
		content.WriteBytes(kit.ASCIIBytes("|FU_STRT*\r\n"))
		return encodeContent(msgFirmwareUpdate, content)
	case model.CommandOutputControl:
		content.WriteInt(int64(kit.CommandInt(c, model.CommandKeyIndex)))
		content.WriteInt(int64(kit.CommandInt(c, model.CommandKeyData)))
		return encodeContent(msgSetIO, content)
	case model.CommandSetConnection:
		s := commandString(c, model.CommandKeyServer) + "," + strconv.Itoa(kit.CommandInt(c, model.CommandKeyPort)) + ",TCP"
		content.WriteBytes(kit.ASCIIBytes(s))
		return encodeContent(msgSetConnection, content)
	case model.CommandSetOdometer:
		content.WriteInt(int64(kit.CommandInt(c, model.CommandKeyData)))
		return encodeContent(msgSetOdometer, content)
	}
	return nil
}
