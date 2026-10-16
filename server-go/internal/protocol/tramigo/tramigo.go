// Package tramigo is the Tramigo protocol (TramigoProtocol): binary reports (protocols
// 0x01 and 0x04) and text reports (0x80) over TCP.
package tramigo

import (
	"regexp"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "tramigo",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is TramigoFrameDecoder: the length's position and byte order depend on the
// protocol byte.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 20 {
			return nil
		}
		var length int
		switch buf.GetUnsignedByte(buf.ReaderIndex()) {
		case 0x80:
			length = buf.GetUnsignedShortLE(buf.ReaderIndex() + 6)
		case 0x02, 0x04:
			length = buf.GetUnsignedShortLE(buf.ReaderIndex() + 1)
		default:
			length = buf.GetUnsignedShort(buf.ReaderIndex() + 6)
		}
		if length <= buf.ReadableBytes() {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

var (
	directions         = []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	patternCoordinates = regexp.MustCompile(`(-?\d+\.\d+), (-?\d+\.\d+)`)
	patternDirection   = regexp.MustCompile(`([NSWE]{1,2}) with speed (\d+) km/h`)
	patternTime        = regexp.MustCompile(`(\d{1,2}:\d{2}(:\d{2})? \w{3} \d{1,2})`)
)

// decode is TramigoProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	var pos *model.Position
	switch buf.ReadUnsignedByte() {
	case 0x01:
		pos = decode01(m, buf)
	case 0x04:
		pos = decode04(m, buf)
	case 0x80:
		pos = decode80(m, buf)
	}
	if pos == nil {
		return nil
	}
	return pos
}

func decode01(m *ingest.Message, buf *kit.Buf) *model.Position {
	buf.ReadUnsignedByte() // version id
	index := buf.ReadUnsignedShortLE()
	typ := buf.ReadUnsignedShortLE()

	if typ != 0x0100 && typ != 0x00FE {
		return nil
	}

	buf.ReadUnsignedShort() // length
	buf.ReadUnsignedShort() // mask
	buf.ReadUnsignedShort() // checksum
	id := buf.ReadUnsignedIntLE()
	buf.ReadUnsignedInt() // time

	ds := m.Session(kit.Format("%d", id))
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyIndex, index)

	buf.ReadUnsignedShortLE() // report trigger
	buf.ReadUnsignedShortLE() // state flag

	pos.Valid = true
	pos.SetLatitude(float64(buf.ReadUnsignedIntLE()) / 10000000.0)
	pos.SetLongitude(float64(buf.ReadUnsignedIntLE()) / 10000000.0)

	pos.Set(model.KeyRssi, buf.ReadUnsignedShortLE())
	pos.Set(model.KeySatellites, buf.ReadUnsignedShortLE())
	pos.Set(model.KeySatellitesVisible, buf.ReadUnsignedShortLE())
	pos.Set("gpsAntennaStatus", buf.ReadUnsignedShortLE())

	pos.Speed = float64(buf.ReadUnsignedShortLE()) * 0.194384
	pos.Course = float64(buf.ReadUnsignedShortLE())

	pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())

	pos.Set(model.KeyBattery, buf.ReadUnsignedShortLE())

	pos.Set(model.KeyCharge, buf.ReadUnsignedShortLE())

	pos.SetTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))

	return pos
}

func decode04(m *ingest.Message, buf *kit.Buf) *model.Position {
	buf.ReadUnsignedShortLE() // length
	buf.ReadUnsignedShortLE() // checksum
	index := buf.ReadUnsignedShortLE()
	id1 := buf.ReadUnsignedIntLE()
	id2 := buf.ReadUnsignedIntLE()
	t := buf.ReadUnsignedIntLE()

	ds := m.Session(kit.Format("%08d%07d", id1, id2))
	if ds == nil {
		return nil
	}

	if m.HasChannel() {
		response := kit.Empty()
		response.WriteByte8(0x04) // protocol
		response.WriteShortLE(24) // length
		response.WriteShortLE(0)  // checksum
		response.WriteShortLE(index)
		response.WriteIntLE(id1)
		response.WriteIntLE(id2)
		response.WriteIntLE(t)

		response.WriteByte8(0xff) // acknowledgement
		response.WriteShortLE(index)
		response.WriteShortLE(0) // success

		response.SetShortLE(3, kit.CRC16(kit.CRC16_CCITT_FALSE, response.Bytes()))

		m.Send(response)
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyIndex, index)

	pos.DeviceTime = model.NewTime(time.UnixMilli(t * 1000))

	for buf.IsReadable() {
		typ := buf.ReadUnsignedByte()
		switch typ {
		case 0:
			pos.Set(model.KeyEvent, buf.ReadUnsignedShortLE())
			buf.ReadUnsignedIntLE() // event data

			status := buf.ReadUnsignedShortLE()
			pos.Set(model.KeyIgnition, kit.Check(int64(status), 5))
			pos.Set(model.KeyStatus, status)

			pos.Valid = true
			pos.SetLatitude(float64(buf.ReadIntLE()) / 100000.0)
			pos.SetLongitude(float64(buf.ReadIntLE()) / 100000.0)
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()))
			pos.Course = float64(buf.ReadUnsignedShortLE())

			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set(model.KeyGps, buf.ReadUnsignedByte())
			pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedShortLE())
			pos.Set("maxAcceleration", float64(buf.ReadUnsignedShortLE())/1000.0)
			pos.Set("maxDeceleration", float64(buf.ReadUnsignedShortLE())/1000.0)
			buf.ReadUnsignedShortLE() // bearing to landmark
			buf.ReadUnsignedIntLE()   // distance to landmark

			pos.FixTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedIntLE() * 1000))

			buf.ReadUnsignedByte() // reserved
		case 1:
			buf.Skip(buf.ReadUnsignedShortLE() - 3) // landmark
		case 4:
			buf.Skip(53) // trip
		case 20:
			buf.Skip(32) // extended
		case 22:
			buf.ReadUnsignedByte()              // zone flag
			buf.Skip(buf.ReadUnsignedShortLE()) // zone name
		case 30:
			buf.Skip(79) // system status
		case 40:
			buf.Skip(40) // analog
		case 50:
			buf.Skip(buf.ReadUnsignedShortLE() - 3) // console
		case 255:
			buf.Skip(4) // acknowledgement
		default:
			kit.Throw("IllegalArgumentException: Unknown type %d", typ)
		}
	}

	if !pos.Valid {
		return nil
	}
	return pos
}

func decode80(m *ingest.Message, buf *kit.Buf) *model.Position {
	buf.ReadUnsignedByte() // version id
	index := buf.ReadUnsignedShort()
	buf.ReadUnsignedShort() // type

	buf.ReadUnsignedShort() // length
	buf.ReadUnsignedShort() // mask
	buf.ReadUnsignedShort() // checksum
	id := buf.ReadUnsignedInt()
	buf.ReadUnsignedInt() // time

	ds := m.Session(kit.Format("%d", id))
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyIndex, index)

	if m.HasChannel() {
		m.Send(kit.NewBuf(kit.NettyBytes("gprs,ack," + kit.Format("%d", index))))
	}

	sentence := kit.Latin1(buf.Bytes())

	match := patternCoordinates.FindStringSubmatch(sentence)
	if match == nil {
		return nil
	}
	pos.SetLatitude(kit.Double(match[1]))
	pos.SetLongitude(kit.Double(match[2]))
	pos.Valid = true

	if match := patternDirection.FindStringSubmatch(sentence); match != nil {
		for i, direction := range directions {
			if match[1] == direction {
				pos.Course = float64(i) * 45.0
				break
			}
		}
		pos.Speed = units.KnotsFromKph(kit.Double(match[2]))
	}

	match = patternTime.FindStringSubmatch(sentence)
	if match == nil {
		return nil
	}
	zone := m.Zone()
	guess := parseTime(match[1], match[2] != "", m.Now.In(zone).Year(), zone)
	pos.SetTime(kit.CorrectYear(m.Now, guess, zone))

	if strings.Contains(sentence, "Ignition on detected") {
		pos.Set(model.KeyIgnition, true)
	} else if strings.Contains(sentence, "Ignition off detected") {
		pos.Set(model.KeyIgnition, false)
	}

	return pos
}

var months = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// timeText is what DATE_FORMAT_WITH_SECONDS ("HH:mm:ss MMM d yyyy") or
// DATE_FORMAT_NO_SECONDS ("HH:mm MMM d yyyy") accept, in English, with the year appended.
var timeText = regexp.MustCompile(`^(\d{2}):(\d{2})(?::(\d{2}))? ([A-Za-z]{3}) (\d+)$`)

// parseTime is DateUtil.parse of "<time> <year>" with the matching format in the server's
// zone (the SMART resolver: a day past the month's end is its last day).
func parseTime(text string, withSeconds bool, year int, zone *time.Location) time.Time {
	match := timeText.FindStringSubmatch(text)
	if match == nil || (match[3] != "") != withSeconds {
		kit.Throw("DateTimeParseException: Text '%s %d' could not be parsed", text, year)
	}
	month := 0
	for i, name := range months {
		if match[4] == name {
			month = i + 1
		}
	}
	hour, minute, day := kit.Int(match[1]), kit.Int(match[2]), kit.Int(match[5])
	second := 0
	if withSeconds {
		second = kit.Int(match[3])
	}
	if month == 0 || hour > 23 || minute > 59 || second > 59 || day < 1 || day > 31 {
		kit.Throw("DateTimeParseException: Text '%s %d' could not be parsed", text, year)
	}
	if last := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day(); day > last {
		day = last
	}
	return javatime.FromWall(time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC), zone)
}
