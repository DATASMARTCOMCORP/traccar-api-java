// Package bce is the BCE protocol (BceProtocol): binary little-endian stacks of reports
// over TCP, with masks selecting the fields of each report.
package bce

import (
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
		Name:         "bce",
		DataCommands: protocol.DataCommands("bce"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

const handshakeLength = 7 // "#BCE#\r\n"

// Framer is BceFrameDecoder: a handshake, then frames of an IMEI and length-prefixed
// blocks ending where the next byte is their checksum.
func Framer() ingest.Framer {
	header := true
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if header && buf.ReadableBytes() >= handshakeLength {
			buf.Skip(handshakeLength)
			header = false
		}
		end := 8 // IMEI
		for buf.ReadableBytes() >= end+2+1+1+1 {
			end += buf.GetUnsignedShortLE(buf.ReaderIndex()+end) + 2
			if buf.ReadableBytes() > end && checksum(buf, end) == buf.GetByte(buf.ReaderIndex()+end) {
				return buf.ReadSlice(end + 1)
			}
		}
		return nil
	}}
}

func checksum(buf *kit.Buf, end int) int8 {
	var result int8
	for i := 0; i < end; i++ {
		result += buf.GetByte(buf.ReaderIndex() + i)
	}
	return result
}

const (
	dataType          = 7
	msgAsyncStack     = 0xA5
	msgStackConfirm   = 0x19
	msgTimeTriggered  = 0xA0
	msgOutputControl  = 0x41
	msgOutputControlA = 0xC1
)

func check(mask, bit int) bool { return kit.Check(int64(mask), bit) }

// setCoordinates sets longitude and latitude; an out-of-range value (Java's
// IllegalArgumentException, caught) leaves the reader after both.
func setCoordinates(buf *kit.Buf, pos *model.Position) {
	start := buf.ReaderIndex()
	defer func() {
		if r := recover(); r != nil {
			if r != model.ErrLatitudeRange && r != model.ErrLongitudeRange {
				panic(r)
			}
			buf.SetReaderIndex(start + 8)
		}
	}()
	pos.SetLongitude(float64(buf.ReadFloatLE()))
	pos.SetLatitude(float64(buf.ReadFloatLE()))
	pos.Valid = true
}

func decodeMask1(buf *kit.Buf, mask int, pos *model.Position) {
	if check(mask, 0) {
		setCoordinates(buf, pos)
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))

		status := buf.ReadUnsignedByte()
		pos.Set(model.KeySatellites, kit.ToInt(status, 4))
		pos.Set(model.KeyHdop, kit.FromInt(status, 4))

		pos.Course = float64(buf.ReadUnsignedByte() * 2)
		pos.Altitude = float64(buf.ReadUnsignedShortLE())

		pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
	}
	if check(mask, 1) {
		pos.Set(model.KeyInput, buf.ReadUnsignedShortLE())
	}
	for i := 1; i <= 8; i++ {
		if check(mask, i+1) {
			pos.Set(model.PrefixAdc+strconv.Itoa(i), buf.ReadUnsignedShortLE())
		}
	}
	if check(mask, 10) {
		buf.Skip(4)
	}
	if check(mask, 11) {
		buf.Skip(4)
	}
	if check(mask, 12) {
		pos.Set("fuel1", buf.ReadUnsignedShort())
	}
	if check(mask, 13) {
		pos.Set("fuel2", buf.ReadUnsignedShort())
	}
	if check(mask, 14) {
		mcc := buf.ReadUnsignedShortLE()
		mnc := buf.ReadUnsignedByte()
		lac := buf.ReadUnsignedShortLE()
		cid := buf.ReadUnsignedShortLE()
		buf.ReadUnsignedByte() // time advance
		rssi := -buf.ReadUnsignedByte()
		pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
	}
}

func decodeMask2(buf *kit.Buf, mask int, pos *model.Position) {
	if check(mask, 0) {
		buf.ReadUnsignedShortLE() // wheel speed
	}
	if check(mask, 1) {
		buf.ReadUnsignedByte() // acceleration pedal
	}
	if check(mask, 2) {
		pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedIntLE())*0.5)
	}
	if check(mask, 3) {
		pos.Set(model.KeyFuel, buf.ReadUnsignedByte())
	}
	if check(mask, 4) {
		pos.Set(model.KeyRpm, float64(buf.ReadUnsignedShortLE())*0.125)
	}
	if check(mask, 5) {
		pos.Set(model.KeyHours, buf.ReadUnsignedIntLE())
	}
	if check(mask, 6) {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
	}
	if check(mask, 7) {
		pos.Set(model.KeyCoolantTemp, int(buf.ReadSignedByte())-40)
	}
	if check(mask, 8) {
		pos.Set("fuel2", buf.ReadUnsignedByte())
	}
	if check(mask, 9) {
		pos.Set(model.KeyEngineLoad, buf.ReadUnsignedByte())
	}
	if check(mask, 10) {
		pos.Set(model.KeyOdometerService, buf.ReadUnsignedShortLE())
	}
	if check(mask, 11) {
		buf.Skip(8) // sensors
	}
	if check(mask, 12) {
		buf.ReadUnsignedShortLE() // ambient air temperature
	}
	if check(mask, 13) {
		buf.Skip(8) // trailer id
	}
	if check(mask, 14) {
		pos.Set(model.KeyFuelConsumption, buf.ReadUnsignedShortLE())
	}
}

func decodeMask3(buf *kit.Buf, mask int, pos *model.Position) {
	if check(mask, 0) {
		buf.ReadUnsignedShortLE() // fuel economy
	}
	if check(mask, 1) {
		pos.Set(model.KeyFuelConsumption, buf.ReadUnsignedIntLE())
	}
	if check(mask, 2) {
		pos.Set(model.KeyAxleWeight, buf.ReadUnsignedMediumLE())
	}
	if check(mask, 3) {
		buf.ReadUnsignedByte() // mil status
	}
	if check(mask, 4) {
		buf.Skip(20) // dtc
	}
	if check(mask, 5) {
		buf.ReadUnsignedShortLE()
	}
	if check(mask, 6) {
		pos.Set(model.KeyDriverUniqueID, strconv.FormatInt(buf.ReadLongLE(), 10))
	}
	if check(mask, 7) {
		pos.Set(model.PrefixTemp+"1", float64(buf.ReadUnsignedShortLE())/10.0-273)
	}
	if check(mask, 8) {
		buf.ReadUnsignedShortLE() // dallas humidity
	}
	if check(mask, 9) {
		pos.Set("fuel1", buf.ReadUnsignedShortLE())
		pos.Set("fuelTemp1", int(buf.ReadSignedByte()))
		pos.Set("fuel2", buf.ReadUnsignedShortLE())
		pos.Set("fuelTemp2", int(buf.ReadSignedByte()))
	}
	if check(mask, 10) {
		pos.Set("fuel3", buf.ReadUnsignedShortLE())
		pos.Set("fuelTemp3", int(buf.ReadSignedByte()))
		pos.Set("fuel4", buf.ReadUnsignedShortLE())
		pos.Set("fuelTemp4", int(buf.ReadSignedByte()))
	}
	if check(mask, 11) {
		buf.Skip(21) // j1979 group 1
	}
	if check(mask, 12) {
		buf.Skip(20) // j1979 dtc
	}
	if check(mask, 13) {
		buf.Skip(9) // j1708 group 1
	}
	if check(mask, 14) {
		buf.Skip(21) // driving quality
	}
}

func decodeMask4(buf *kit.Buf, mask int, pos *model.Position) {
	if check(mask, 0) {
		buf.ReadUnsignedIntLE()
	}
	if check(mask, 1) {
		buf.Skip(30) // lls group 3
	}
	if check(mask, 2) {
		buf.ReadUnsignedIntLE() // instant fuel consumption
	}
	if check(mask, 3) {
		buf.Skip(10) // axle weight group
	}
	if check(mask, 4) {
		buf.ReadUnsignedByte()
	}
	if check(mask, 5) {
		buf.ReadUnsignedShortLE()
	}
	if check(mask, 6) {
		pos.Set("maxAcceleration", float64(buf.ReadUnsignedByte())/50.0)
		pos.Set("maxBraking", float64(buf.ReadUnsignedByte())/50.0)
		pos.Set("maxCornering", float64(buf.ReadUnsignedByte())/50.0)
	}
	if check(mask, 7) {
		buf.Skip(16)
	}
	if check(mask, 8) {
		for i := 1; i <= 4; i++ {
			temperature := buf.ReadUnsignedShortLE()
			if temperature > 0 {
				pos.Set(model.PrefixTemp+strconv.Itoa(i), float64(temperature)/10.0-273)
			}
			buf.Skip(8)
		}
	}
	if check(mask, 9) {
		pos.Set("driver1", kit.Trim(buf.ReadString(16)))
		pos.Set("driver2", kit.Trim(buf.ReadString(16)))
	}
	if check(mask, 10) {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
	}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	imei := kit.Format("%015d", buf.ReadLongLE())
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	positions := []*model.Position{}
	for buf.ReadableBytes() > 1 {
		dataEnd := buf.ReadUnsignedShortLE() + buf.ReaderIndex()
		typ := buf.ReadUnsignedByte()
		if typ != msgAsyncStack && typ != msgTimeTriggered {
			return nil
		}
		confirmKey := buf.ReadUnsignedByte() & 0x7F

		for buf.ReaderIndex() < dataEnd {
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID

			structEnd := buf.ReadUnsignedByte() + buf.ReaderIndex()

			t := buf.ReadUnsignedIntLE()
			if t&0x0f == dataType {
				t = t >> 4 << 1
				t += 0x47798280 // 01/01/2008
				threshold := m.Now.UnixMilli()/1000 - 3650*24*3600
				for t < threshold {
					t += 0x0FFFFFFF * 2
				}
				pos.SetTime(time.UnixMilli(t * 1000))

				var masks []int
				for {
					mask := buf.ReadUnsignedShortLE()
					masks = append(masks, mask)
					if !check(mask, 15) {
						break
					}
				}
				decodeMask1(buf, masks[0], pos)
				if len(masks) >= 2 {
					decodeMask2(buf, masks[1], pos)
				}
				if len(masks) >= 3 {
					decodeMask3(buf, masks[2], pos)
				}
				if len(masks) >= 4 {
					decodeMask4(buf, masks[3], pos)
				}
			}

			buf.SetReaderIndex(structEnd)

			if pos.Valid {
				positions = append(positions, pos)
			} else if pos.Attributes.Len() > 0 {
				m.LastLocation(pos, nil)
				positions = append(positions, pos)
			}
		}

		if typ == msgAsyncStack && m.HasChannel() {
			response := kit.Empty()
			response.WriteLongLE(kit.Long(imei))
			response.WriteShortLE(2)
			response.WriteByte8(msgStackConfirm)
			response.WriteByte8(confirmKey)
			sum := 0
			for i := 0; i < response.WriterIndex(); i++ {
				sum += response.GetUnsignedByte(i)
			}
			response.WriteByte8(sum)
			m.Send(response)
		}
	}
	return positions
}

// encode is BceProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	if c.Type != model.CommandOutputControl {
		return nil
	}
	buf := kit.Empty()
	buf.WriteLongLE(kit.Long(m.UniqueID(c.DeviceID)))
	buf.WriteShortLE(1 + 1 + 3 + 1) // length
	buf.WriteByte8(msgOutputControl)
	if kit.CommandInt(c, model.CommandKeyIndex) == 1 {
		buf.WriteByte8(0x0A)
	} else {
		buf.WriteByte8(0x0B)
	}
	buf.WriteByte8(0xFF) // index
	buf.WriteByte8(0x00) // form id
	data := kit.CommandString(c, model.CommandKeyData)
	if data == nil {
		kit.Throw("NumberFormatException: Cannot parse null string: null")
	}
	if kit.Int(*data) > 0 {
		buf.WriteShortLE(0x0055)
	} else {
		buf.WriteShortLE(0x0000)
	}
	buf.WriteByte8(kit.Sum(buf.Bytes()))
	return buf
}
