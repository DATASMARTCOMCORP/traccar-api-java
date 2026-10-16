// Package nyitech is the Nyitech protocol (NyitechProtocol): little-endian binary
// messages over TCP.
package nyitech

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "nyitech",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			f := ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, -4, 0)
			f.LittleEndian = true
			return ingest.Pipeline{Framer: f, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgLogin                = 0x1001
	msgComprehensiveLive    = 0x2001
	msgComprehensiveHistory = 0x2002
	msgAlarm                = 0x2003
	msgFixed                = 0x2004
)

func decodeLocation(pos *model.Position, buf *kit.Buf) {
	pos.SetTime(kit.NewDateBuilder(nil).
		SetDateReverse(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		Date())
	flags := int64(buf.ReadUnsignedByte())
	pos.Valid = kit.To(flags, 2) > 0
	lat := float64(buf.ReadUnsignedIntLE()) / 3600000.0
	lon := float64(buf.ReadUnsignedIntLE()) / 3600000.0
	if !kit.Check(flags, 2) {
		lat = -lat
	}
	if !kit.Check(flags, 3) {
		lon = -lon
	}
	pos.SetLatitude(lat)
	pos.SetLongitude(lon)
	pos.Speed = units.KnotsFromCps(float64(buf.ReadUnsignedShortLE()))
	pos.Course = float64(buf.ReadUnsignedShortLE()) / 10.0
	pos.Altitude = float64(buf.ReadShortLE()) / 10.0
}

func decodeAlarm(typ int) string {
	switch typ {
	case 0x09:
		return model.AlarmAcceleration
	case 0x0a:
		return model.AlarmBraking
	case 0x0b:
		return model.AlarmCornering
	case 0x0e:
		return model.AlarmSos
	}
	return ""
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.Skip(2)               // header
	buf.ReadUnsignedShortLE() // length
	ds := m.Session(buf.ReadString(12))
	if ds == nil {
		return nil
	}
	typ := buf.ReadUnsignedShortLE()
	if typ != msgLogin && typ != msgComprehensiveLive && typ != msgComprehensiveHistory && typ != msgAlarm && typ != msgFixed {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	switch typ {
	case msgComprehensiveLive, msgComprehensiveHistory:
		buf.Skip(6) // time
		includeLocation := buf.ReadUnsignedByte() > 0
		includeObd := buf.ReadUnsignedByte() > 0
		buf.ReadUnsignedByte() // include sensor
		if includeLocation {
			decodeLocation(pos, buf)
		} else {
			m.LastLocation(pos, nil)
		}
		if includeObd {
			for count := buf.ReadUnsignedByte(); count > 0; count-- {
				pid := buf.ReadUnsignedShortLE()
				switch length := buf.ReadUnsignedByte(); length {
				case 1:
					kit.ObdDecodeData(pid, int64(buf.ReadSignedByte()), true).Add(pos)
				case 2:
					kit.ObdDecodeData(pid, int64(buf.ReadShortLE()), true).Add(pos)
				case 4:
					kit.ObdDecodeData(pid, int64(buf.ReadIntLE()), true).Add(pos)
				default:
					buf.Skip(length)
				}
			}
		}
		pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedInt())/100.0)
		pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedInt())
	case msgAlarm:
		buf.ReadUnsignedShortLE() // random number
		buf.ReadUnsignedByte()    // tag
		pos.AddAlarm(decodeAlarm(buf.ReadUnsignedByte()))
		buf.ReadUnsignedShortLE() // threshold
		buf.ReadUnsignedShortLE() // value
		buf.Skip(6)               // time
		decodeLocation(pos, buf)
	case msgFixed:
		buf.Skip(6) // time
		decodeLocation(pos, buf)
	default:
		decodeLocation(pos, buf)
	}
	return pos
}
