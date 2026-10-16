// Package iotm is the IOTM protocol (IotmProtocol): binary records published over MQTT.
package iotm

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

// Protocol is the protocol's definition: MQTT over TCP.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "iotm",
		DataCommands: protocol.DataCommands("iotm"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			codec := ingest.NewMQTT()
			return ingest.Pipeline{
				Framer:  codec,
				Decoder: ingest.MQTTDecoder(codec, decode),
				Encoder: Encoder(codec),
			}
		}}},
	}
}

func readValue(buf *kit.Buf, sensorType int) any {
	switch sensorType {
	case 0:
		return false
	case 1:
		return true
	case 3:
		return int64(0)
	case 4:
		return buf.ReadUnsignedByte()
	case 5:
		return buf.ReadUnsignedShortLE()
	case 6:
		return buf.ReadUnsignedIntLE()
	case 7, 11:
		return buf.ReadLongLE()
	case 8:
		return int(buf.ReadSignedByte())
	case 9:
		return buf.ReadShortLE()
	case 10:
		return int(buf.ReadIntLE())
	case 12:
		return model.Float(buf.ReadFloatLE())
	case 13:
		return buf.ReadDoubleLE()
	case 32:
		return buf.ReadString(buf.ReadUnsignedByte())
	case 33:
		return hex.EncodeToString(buf.ReadBytes(buf.ReadUnsignedByte()))
	case 64:
		return buf.ReadString(buf.ReadUnsignedShortLE())
	case 65:
		return hex.EncodeToString(buf.ReadBytes(buf.ReadUnsignedShortLE()))
	}
	return nil
}

func itoa(i int) string { return strconv.Itoa(i) }

func decodeSensor(pos *model.Position, record *kit.Buf, sensorType, sensorID int) {
	on := sensorType > 0
	switch {
	case sensorID == 0x0002:
		pos.Set(model.KeyMotion, on)
	case sensorID == 0x0008 || sensorID == 0x009B:
		if on {
			pos.AddAlarm(model.AlarmJamming)
		}
	case sensorID >= 0x0010 && sensorID <= 0x0015:
		pos.Set(model.PrefixIn+itoa(sensorID-0x0010+2), on)
	case sensorID == 0x0062:
		pos.Set("doorFL", on)
	case sensorID == 0x0063:
		pos.Set("doorFR", on)
	case sensorID == 0x0064:
		pos.Set("doorRL", on)
	case sensorID == 0x0065:
		pos.Set("doorRR", on)
	case sensorID == 0x001E:
		pos.Set("buttonPresent", on)
	case sensorID == 0x006D:
		pos.Set(model.KeyIgnition, on)
	case sensorID == 0x008B:
		pos.Set("handBrake", on)
	case sensorID == 0x008C:
		pos.Set("footBrake", on)
	case sensorID >= 0x0094 && sensorID <= 0x0096:
		pos.Set(model.PrefixOut+itoa(sensorID-0x0094+1), on)
	case sensorID == 0x009A:
		pos.Set(model.PrefixOut+"4", on)
	case sensorID == 0x2000:
		pos.Set(model.KeyObdSpeed, record.ReadUnsignedByte())
	case sensorID == 0x2001:
		pos.Set(model.KeySatellites, record.ReadUnsignedByte())
	case sensorID == 0x2006:
		pos.Set(model.KeyThrottle, record.ReadUnsignedByte())
	case sensorID == 0x2007:
		pos.Set(model.KeyFuel, record.ReadUnsignedByte())
	case sensorID == 0x2008:
		pos.Set(model.KeyCoolantTemp, record.ReadUnsignedByte())
	case sensorID == 0x2009:
		pos.Set("fuel2", record.ReadUnsignedByte())
	case sensorID == 0x200A:
		pos.Set(model.KeyEngineLoad, record.ReadUnsignedByte())
	case sensorID == 0x2041:
		pos.Set(model.KeyBatteryLevel, record.ReadUnsignedByte())
	case sensorID == 0x3000:
		pos.Set(model.KeyPower, float64(record.ReadUnsignedShortLE())/1000.0)
	case sensorID >= 0x3001 && sensorID <= 0x3003:
		pos.Set(model.PrefixAdc+itoa(0x3003-sensorID+3), float64(record.ReadUnsignedShortLE())/1000.0)
	case sensorID == 0x3004:
		pos.Set(model.KeyBattery, float64(record.ReadUnsignedShortLE())/1000.0)
	case sensorID == 0x300C:
		pos.Set(model.KeyRpm, record.ReadUnsignedShortLE())
	case sensorID == 0x3021:
		pos.Set(model.KeyFuelConsumption, float64(record.ReadUnsignedShortLE())/20.0)
	case sensorID == 0x3037:
		pos.Set("cargoWeight", record.ReadUnsignedShortLE()*2)
	case sensorID == 0x4001:
		pos.Set(model.KeyFuelUsed, record.ReadUnsignedIntLE())
	case sensorID == 0x4002:
		pos.Set(model.KeyHours, record.ReadUnsignedIntLE())
	case sensorID == 0x4003:
		pos.Set(model.KeyOdometer, record.ReadUnsignedIntLE()*5)
	case sensorID == 0x4063:
		pos.Set(model.KeyAxleWeight, record.ReadUnsignedIntLE())
	case sensorID == 0x5000:
		if v := readValue(record, sensorType); v != nil {
			pos.Set(model.KeyDriverUniqueID, model.JavaToString(v))
		}
	case sensorID >= 0x5004 && sensorID <= 0x5007:
		pos.Set(model.PrefixTemp+itoa(sensorID-0x5004+1), record.ReadLongLE())
	case sensorID == 0x500D:
		if v := readValue(record, sensorType); v != nil {
			pos.Set("trailerId", model.JavaToString(v))
		}
	case sensorID == 0xA000:
		pos.Set(model.KeyDeviceTemp, record.ReadFloatLE())
	case sensorID == 0xA001:
		pos.Set(model.KeyAcceleration, record.ReadFloatLE())
	case sensorID == 0xA002:
		pos.Set("cornering", record.ReadFloatLE())
	case sensorID >= 0xA017 && sensorID <= 0xA01A:
		pos.Set(model.PrefixTemp+itoa(sensorID-0xA017+1), record.ReadFloatLE())
	case sensorID == 0xB002:
		pos.Set(model.KeyObdOdometer, record.ReadDoubleLE())
	default:
		// getAttributes().put: a null value is stored too.
		pos.Attributes.Set(model.PrefixIo+itoa(sensorID), readValue(record, sensorType))
	}
}

func decode(m *ingest.Message, ds *ingest.DeviceSession, publish *ingest.MQTTMessage) any {
	positions := []*model.Position{}
	buf := publish.Payload
	buf.ReadUnsignedByte() // structure version
	for buf.ReadableBytes() > 1 {
		typ := buf.ReadUnsignedByte()
		record := buf.ReadSlice(buf.ReadUnsignedShortLE())
		switch typ {
		case 1:
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID
			pos.SetTime(time.UnixMilli(record.ReadUnsignedIntLE() * 1000))
			for record.ReadableBytes() > 0 {
				sensorType := record.ReadUnsignedByte()
				sensorID := record.ReadUnsignedShortLE()
				if sensorType == 14 {
					pos.Valid = true
					pos.SetLatitude(float64(record.ReadFloatLE()))
					pos.SetLongitude(float64(record.ReadFloatLE()))
					pos.Speed = units.KnotsFromKph(float64(record.ReadUnsignedShortLE()))
					pos.Set(model.KeyHdop, float64(record.ReadUnsignedByte())/10.0)
					pos.Set(model.KeySatellites, record.ReadUnsignedByte())
					pos.Course = float64(record.ReadUnsignedShortLE())
					pos.Altitude = float64(record.ReadShortLE())
				} else if sensorType != 3 {
					decodeSensor(pos, record, sensorType, sensorID)
				}
			}
			positions = append(positions, pos)
		case 3:
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID
			t := time.UnixMilli(record.ReadUnsignedIntLE() * 1000)
			m.LastLocation(pos, &t)
			record.ReadUnsignedByte() // function identifier
			pos.Set(model.KeyEvent, record.ReadUnsignedByte())
			positions = append(positions, pos)
		}
	}
	buf.ReadUnsignedByte() // checksum
	if len(positions) == 0 {
		return nil
	}
	return positions
}

// Encoder is IotmProtocolEncoder: an output control as an MQTT PUBLISH to
// <uniqueId>/OUTC, encoded for the connection's MQTT version.
func Encoder(codec *ingest.MQTT) ingest.CommandEncoder {
	return ingest.CommandEncoderFunc(func(m *ingest.Message, c *model.Command) any {
		if c.Type != model.CommandOutputControl {
			return nil
		}
		uniqueID := m.UniqueID(c.DeviceID)
		buf := kit.Empty()
		buf.WriteByte8(2) // structure version
		buf.WriteByte8(2) // record type imei
		buf.WriteShortLE(8)
		buf.WriteLongLE(kit.Long(uniqueID))
		buf.WriteByte8(4) // record type output control
		buf.WriteShortLE(10)
		buf.WriteIntLE(0x7FFFFFFF) // expiration
		buf.WriteByte8(kit.CommandInt(c, model.CommandKeyIndex) - 1)
		buf.WriteByte8(0) // output command index
		buf.WriteByte8(3) // length
		buf.WriteByte8(kit.CommandInt(c, model.CommandKeyData))
		buf.WriteByte8(0xB0)
		buf.WriteByte8(0xB1)
		buf.WriteByte8(kit.Sum(buf.Bytes()))
		return codec.Publish(uniqueID+"/OUTC", 1, 0, buf.Bytes())
	})
}
