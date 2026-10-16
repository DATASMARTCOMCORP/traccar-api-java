// Package naviset is the Naviset protocol (NavisetProtocol): little-endian binary
// packets with a 12-bit length, each acknowledged with its checksum.
package naviset

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
		Name: "naviset",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is NavisetFrameDecoder: a 2-byte header holding the length, and a checksum.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		length := 2 + kit.ToInt(buf.GetUnsignedShortLE(buf.ReaderIndex()), 12) + 2
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

const (
	msgHeader = 0b00
	msgData   = 0b01
)

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	response := kit.Empty()
	response.WriteByte8(0x01)
	response.WriteShortLE(buf.GetUnsignedShortLE(buf.WriterIndex() - 2))
	m.Send(response)

	length := buf.ReadUnsignedShortLE()
	t := kit.BetweenInt(length, 14, 16)
	buf.ReadUnsignedShortLE() // device number

	switch t {
	case msgHeader:
		m.Session(buf.ReadString(15))
	case msgData:
		positions := []*model.Position{}
		ds := m.Session()
		if ds == nil {
			return nil
		}

		blockMask := buf.ReadUnsignedByte()
		check := func(v, bit int) bool { return kit.Check(int64(v), bit) }

		for buf.ReadableBytes() > 2 {
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID

			pos.Set(model.KeyIndex, buf.ReadUnsignedShortLE())
			pos.Set(model.KeyStatus, buf.ReadUnsignedByte())
			pos.Valid = true
			pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))
			pos.SetLatitude(float64(buf.ReadUnsignedIntLE()) / 1000000.0)
			pos.SetLongitude(float64(buf.ReadUnsignedIntLE()) / 1000000.0)
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()) / 10.0)

			if check(blockMask, 0) {
				dataMask := buf.ReadUnsignedByte()
				if check(dataMask, 0) {
					satellites := buf.ReadUnsignedByte()
					pos.Valid = check(satellites, 7)
					pos.Set(model.KeySatellites, kit.ToInt(satellites, 7))
				}
				if check(dataMask, 1) {
					pos.Course = float64(buf.ReadUnsignedShortLE()) / 10.0
				}
				if check(dataMask, 2) {
					pos.Altitude = float64(buf.ReadShortLE())
				}
				if check(dataMask, 3) {
					pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
				}
				if check(dataMask, 4) {
					pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/1000.0)
					pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)
				}
				if check(dataMask, 5) {
					pos.Set(model.KeyInput, buf.ReadUnsignedByte())
					pos.Set(model.KeyOutput, buf.ReadUnsignedByte())
				}
				if check(dataMask, 6) {
					pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
				}
				if check(dataMask, 7) {
					buf.Skip(6) // accelerometer
				}
			}

			if check(blockMask, 1) {
				dataMask := buf.ReadUnsignedByte()
				for i := range 8 {
					if check(dataMask, i) {
						pos.Set(model.PrefixAdc+kit.Format("%d", i+1), buf.ReadUnsignedShortLE())
					}
				}
			}

			if check(blockMask, 2) {
				dataMask := buf.ReadUnsignedByte()
				temp := func(i int) { pos.Set(model.PrefixTemp+kit.Format("%d", i), int(buf.ReadSignedByte())) }
				if check(dataMask, 0) {
					pos.Set(model.KeyDeviceTemp, int(buf.ReadSignedByte()))
				}
				if check(dataMask, 1) {
					buf.Skip(6) // key code
				}
				if check(dataMask, 2) {
					temp(1)
					temp(2)
				}
				if check(dataMask, 3) {
					temp(3)
					temp(4)
				}
				if check(dataMask, 4) {
					temp(5)
					temp(6)
					temp(7)
					temp(8)
				}
				if check(dataMask, 5) {
					pos.Set(model.KeyHours, float64(buf.ReadUnsignedShortLE())/60.0)
				}
				if check(dataMask, 6) {
					buf.ReadUnsignedByte() // extra status
				}
				if check(dataMask, 7) {
					buf.ReadUnsignedByte() // geofence
				}
			}

			if check(blockMask, 3) {
				dataMask := buf.ReadUnsignedByte()
				if check(dataMask, 0) {
					pos.Set("fuel1", buf.ReadUnsignedShortLE())
				}
				if check(dataMask, 1) {
					pos.Set("fuel2", buf.ReadUnsignedShortLE())
				}
				if check(dataMask, 2) {
					pos.Set(model.KeyFuel, buf.ReadUnsignedShortLE())
				}
				if check(dataMask, 3) {
					buf.Skip(18)
				}
				if check(dataMask, 4) {
					buf.ReadUnsignedByte() // fuel 1 temperature
				}
				if check(dataMask, 5) {
					buf.ReadUnsignedByte() // fuel 2 temperature
				}
				if check(dataMask, 6) {
					buf.ReadUnsignedShortLE() // fuel 1 frequency
				}
				if check(dataMask, 7) {
					buf.ReadUnsignedShortLE() // fuel 2 frequency
				}
			}

			if check(blockMask, 4) {
				dataMask := buf.ReadUnsignedByte()
				if check(dataMask, 0) {
					buf.ReadUnsignedByte() // fuel level (percentage)
					pos.Set(model.KeyRpm, buf.ReadUnsignedShortLE())
					pos.Set(model.KeyCoolantTemp, int(buf.ReadSignedByte()))
				}
				if check(dataMask, 1) {
					buf.ReadUnsignedIntLE() // fuel consumption
				}
				if check(dataMask, 2) {
					pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
				}
				for i := 3; i < 8; i++ {
					if check(dataMask, i) {
						buf.ReadUnsignedShortLE() // axle weight
					}
				}
			}

			// The Java decoder tests block 4 a second time (not block 5).
			if check(blockMask, 4) {
				dataMask := buf.ReadUnsignedByte()
				if check(dataMask, 0) {
					buf.ReadUnsignedByte() // speed
				}
				if check(dataMask, 1) {
					buf.ReadUnsignedMediumLE() // prefix S
				}
				if check(dataMask, 2) {
					buf.ReadUnsignedIntLE() // prefix P
				}
				if check(dataMask, 3) {
					buf.ReadUnsignedIntLE() // prefix A or B
				}
				if check(dataMask, 4) {
					buf.ReadUnsignedShortLE() // prefix R
				}
				if check(dataMask, 5) {
					buf.Skip(26)
				}
				if check(dataMask, 6) {
					buf.ReadUnsignedIntLE() // reserved
				}
				if check(dataMask, 7) {
					buf.ReadUnsignedIntLE() // reserved
				}
			}

			positions = append(positions, pos)
		}

		return positions
	}

	return nil
}
