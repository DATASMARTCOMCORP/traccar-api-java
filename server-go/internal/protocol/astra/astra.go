// Package astra is the Astra protocol (AstraProtocol): binary K and X reports over TCP
// (with a length field) and UDP, each acknowledged with 0x06.
package astra

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: TCP and UDP servers.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "astra",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 1, 2, -3, 0),
					Decoder: ingest.DecoderFunc(decode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
			}},
		},
	}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	protocol := buf.ReadSignedByte()
	buf.ReadUnsignedShort() // length

	if m.HasChannel() {
		m.Send(kit.NewBuf([]byte{0x06}))
	}

	switch protocol {
	case 'K':
		return decodeK(m, buf)
	case 'X':
		return decodeX(m, buf)
	}
	return nil
}

func readImei(buf *kit.Buf) string {
	return kit.Format("%08d%07d", buf.ReadUnsignedInt(), buf.ReadUnsignedMedium())
}

func readTime(buf *kit.Buf) time.Time {
	return kit.NewDateBuilder(nil).SetDate(1980, 1, 6).AddMillis(buf.ReadUnsignedInt() * 1000).Date()
}

// mul32 is Java's int multiplication, which overflows.
func mul32(a, b int) int { return int(int32(a * b)) }

func decodeK(m *ingest.Message, buf *kit.Buf) any {
	ds := m.Session(readImei(buf))
	if ds == nil {
		return nil
	}

	positions := []*model.Position{}
	for buf.ReadableBytes() > 2 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		buf.ReadUnsignedByte() // index

		pos.Valid = true
		pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
		pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
		pos.SetTime(readTime(buf))
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte() * 2))
		pos.Course = float64(buf.ReadUnsignedByte() * 2)

		pos.Set(model.KeyEvent, buf.ReadUnsignedMedium())

		status := buf.ReadUnsignedShort()
		pos.Set(model.KeyStatus, status)

		pos.Set(model.PrefixIo+"1", buf.ReadUnsignedByte())
		pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedByte())
		pos.Set(model.KeyBattery, buf.ReadUnsignedByte())
		pos.Set(model.KeyPower, buf.ReadUnsignedByte())

		buf.ReadUnsignedByte() // max journey speed
		buf.Skip(6)            // accelerometer
		pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedShort())
		buf.ReadUnsignedShort() // journey idle time

		pos.Altitude = float64(buf.ReadUnsignedByte() * 20)

		quality := buf.ReadUnsignedByte()
		pos.Set(model.KeySatellites, quality&0xf)
		pos.Set(model.KeyRssi, quality>>4)

		buf.ReadUnsignedByte() // geofence events

		if kit.Check(int64(status), 8) {
			pos.Set(model.KeyDriverUniqueID, buf.ReadString(7))
			pos.Set(model.KeyOdometer, mul32(buf.ReadUnsignedMedium(), 1000))
			pos.Set(model.KeyHours, units.MsFromHours(int64(buf.ReadUnsignedShort())))
		}

		if kit.Check(int64(status), 6) {
			// Java logs "Extension data is not supported" and returns this position alone.
			return pos
		}

		positions = append(positions, pos)
	}
	return positions
}

func decodeX(m *ingest.Message, buf *kit.Buf) any {
	count := buf.ReadUnsignedByte()

	ds := m.Session(readImei(buf))
	if ds == nil {
		return nil
	}

	positions := []*model.Position{}
	for i := 0; i < count; i++ {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Set(model.KeyIndex, buf.ReadUnsignedByte())

		mask := int64(buf.ReadUnsignedShort())<<32 + buf.ReadUnsignedInt()

		deviceTime := readTime(buf)
		pos.DeviceTime = model.NewTime(deviceTime)

		pos.Set(model.KeyEvent, buf.ReadUnsignedInt())
		pos.Set(model.KeyStatus, buf.ReadUnsignedShort())

		if mask&1 > 0 {
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedByte())/5.0)
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
		}

		if mask&2 > 0 {
			pos.Valid = true
			pos.FixTime = model.NewTime(readTime(buf))
			pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
			pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte() * 2))
			buf.ReadUnsignedByte() // max speed since last report
			pos.Course = float64(buf.ReadUnsignedByte() * 2)
			pos.Altitude = float64(buf.ReadUnsignedByte() * 20)
			pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedShort()*100)
		} else {
			t := pos.DeviceTime.Time
			m.LastLocation(pos, &t)
		}

		if mask&4 > 0 {
			buf.ReadUnsignedShort() // states
			buf.ReadUnsignedShort() // changes mask
		}

		if mask&8 > 0 {
			buf.ReadUnsignedShort() // adc1
			buf.ReadUnsignedShort() // adc2
		}

		if mask&16 > 0 {
			for _, key := range []string{"xMax", "xMin", "yMax", "yMin", "zMax", "zMin"} {
				pos.Set(key, int(buf.ReadSignedByte()))
			}
			pos.Set("idleHours", buf.ReadUnsignedShort())
		}

		if mask&32 > 0 {
			value := buf.ReadUnsignedByte()
			pos.Set(model.KeySatellites, kit.ToInt(value, 4))
			pos.Set(model.KeyRssi, kit.FromInt(value, 4))
		}

		if mask&64 > 0 {
			buf.ReadUnsignedShort() // mcc
			buf.ReadUnsignedShort() // mnc
		}

		if mask&128 > 0 {
			buf.ReadUnsignedByte() // geofences
		}

		if mask&256 > 0 {
			buf.ReadUnsignedByte() // source
			buf.ReadLong()         // driver id
		}

		if mask&512 > 0 {
			buf.ReadUnsignedByte() // source
			buf.Skip(10)           // trailer id
			buf.ReadUnsignedByte() // status
		}

		if mask&1024 > 0 {
			pos.Set("axleWeight", buf.ReadUnsignedShort())
		}

		if mask&2048 > 0 {
			pos.Set(model.KeyOdometer, mul32(buf.ReadUnsignedMedium(), 1000))
			pos.Set(model.KeyHours, mul32(buf.ReadUnsignedShort(), 3_600_000))
		}

		if mask&4096 > 0 {
			decodeEngine(pos, buf, true)
		}

		if mask&8192 > 0 {
			decodeEngine(pos, buf, false)
		}

		if mask&16384 > 0 {
			for j := 1; j <= 5; j++ {
				pos.Set("dtc"+strconv.Itoa(j), buf.ReadString(5))
			}
		}

		if mask&32768 > 0 {
			pos.Set(model.KeyOdometer, mul32(buf.ReadUnsignedMedium(), 1000))
			pos.Set(model.KeyHours, mul32(buf.ReadUnsignedShort(), 3_600_000))
			pos.Set("axleWeight", buf.ReadUnsignedShort())
			pos.Set("tripFuelUsed", float64(buf.ReadUnsignedShort())/10.0)
			pos.Set("tripCruise", buf.ReadUnsignedShort())
			pos.Set(model.KeyOdometerService, buf.ReadUnsignedShort()*5)
		}

		if mask&65536 > 0 {
			pos.Set(model.KeyOdometer, mul32(buf.ReadUnsignedMedium(), 1000))
			pos.Set(model.KeyHours, mul32(buf.ReadUnsignedShort(), 3_600_000))
			buf.ReadUnsignedShort() // time with mil on
			buf.ReadUnsignedShort() // distance with mil on
		}

		if mask&131072 > 0 {
			for j := 1; j <= 6; j++ {
				pos.Set(model.PrefixTemp+strconv.Itoa(j), float64(buf.ReadShort())/10.0)
			}
			for j := 1; j <= 3; j++ {
				pos.Set("setpoint"+strconv.Itoa(j), float64(buf.ReadSignedByte())*0.5)
			}
			buf.ReadUnsignedByte()   // refrigerator fuel level
			buf.ReadUnsignedShort()  // refrigerator total engine hours
			buf.ReadUnsignedShort()  // refrigerator total standby hours
			buf.ReadUnsignedShort()  // refrigerator status
			buf.ReadUnsignedMedium() // alarm flags
		}

		if mask&262144 > 0 {
			for j := 1; j <= 4; j++ {
				pos.Set(model.PrefixTemp+strconv.Itoa(j), float64(buf.ReadUnsignedShort()-550)/10.0)
			}
		}

		if mask&524288 > 0 {
			pos.Set("alarmCount", buf.ReadUnsignedByte())
			pos.Set("alarmQueue", buf.ReadSlice(16).HexDump())
		}

		if mask&4294967296 > 0 {
			for j := 1; j <= 6; j++ {
				pos.Set("sensor"+strconv.Itoa(j), buf.ReadUnsignedMedium())
			}
		}

		positions = append(positions, pos)
	}
	return positions
}

// decodeEngine is the FMS (mask 4096) or OBD (mask 8192) block.
func decodeEngine(pos *model.Position, buf *kit.Buf, fms bool) {
	pos.Set("wheelSpeedMax", buf.ReadUnsignedByte())
	pos.Set("wheelSpeedAvg", buf.ReadUnsignedByte())
	pos.Set("rpmMax", buf.ReadUnsignedByte()*32)
	pos.Set("rpmAvg", buf.ReadUnsignedByte()*32)
	pos.Set("acceleratorMax", buf.ReadUnsignedByte())
	pos.Set("acceleratorAvg", buf.ReadUnsignedByte())
	pos.Set("engineLoadMax", buf.ReadUnsignedByte())
	pos.Set("engineLoadAvg", buf.ReadUnsignedByte())
	pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedShort()*100)
	pos.Set(model.KeyCoolantTemp, int(buf.ReadSignedByte())+40)
	prefix := "obd"
	if fms {
		prefix = "fms"
	}
	pos.Set(prefix+"Status", buf.ReadUnsignedShort())
	pos.Set(prefix+"Events", buf.ReadUnsignedShort())
	pos.Set(model.KeyFuel, buf.ReadUnsignedByte())
	if fms {
		pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedInt())*0.5)
	} else {
		pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedShort())/10.0)
	}
}
