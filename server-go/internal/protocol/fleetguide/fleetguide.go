// Package fleetguide is the FleetGuide protocol (FleetGuideProtocol): binary UDP packets
// of typed records, optionally LZ-compressed, acknowledged per packet.
package fleetguide

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "fleetguide",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgSyncReq   = 1
	msgSyncAck   = 2
	msgDataNoAck = 4
	msgRepNoAck  = 6
)

func check(v, i int) bool { return kit.Check(int64(v), i) }

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // signature
	options := buf.ReadUnsignedShortLE()
	length := kit.ToInt(options, 11)

	var ds *ingest.DeviceSession
	var deviceID *int64
	if check(options, 11) {
		id := buf.ReadUnsignedIntLE()
		deviceID = &id
		ds = m.Session(strconv.FormatInt(id, 10))
	} else {
		ds = m.Session()
	}
	if ds == nil {
		return nil
	}

	msgType := 0
	var index *int
	if check(options, 12) {
		value := buf.ReadUnsignedByte()
		msgType = kit.ToInt(value, 4)
		i := kit.FromInt(value, 4)
		index = &i
	}

	if msgType != msgDataNoAck && msgType != msgRepNoAck {
		var responseType *int
		if msgType == msgSyncReq {
			t := msgSyncAck
			responseType = &t
		}
		sendResponse(m, deviceID, responseType, index)
	}

	if check(options, 13) {
		buf.ReadUnsignedShortLE() // acknowledgement
	}

	var data *kit.Buf
	if check(options, 14) {
		data = decompress(buf.ReadSlice(length))
	} else {
		data = buf.ReadSlice(length)
	}

	positions := []*model.Position{}
	newPosition := func() *model.Position {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		return pos
	}
	pos := newPosition()

	for data.IsReadable() {
		recordHeader := data.ReadUnsignedShortLE()
		recordLength := kit.ToInt(recordHeader, 10)
		recordType := kit.FromInt(recordHeader, 10)
		recordEnd := data.ReaderIndex() + recordLength

		if recordType == 0 && !pos.DeviceTime.IsZero() {
			positions = processPosition(m, positions, pos)
			pos = newPosition()
		}

		switch recordType {
		case 0:
			pos.SetTime(time.UnixMilli((data.ReadUnsignedIntLE() + 1262304000) * 1000)) // since 2010-01-01
		case 1:
			pos.SetLatitude(float64(data.ReadUnsignedIntLE()) * 90.0 / 0xFFFFFFFF)
			pos.SetLongitude(float64(data.ReadUnsignedIntLE()) * 180.0 / 0xFFFFFFFF)
			speed := data.ReadUnsignedShortLE()
			pos.Speed = units.KnotsFromKph(float64(kit.ToInt(speed, 14)) / 10.0)
			if check(speed, 14) {
				pos.SetLatitude(-pos.Latitude)
			}
			if check(speed, 15) {
				pos.SetLongitude(-pos.Longitude)
			}
			course := data.ReadUnsignedShortLE()
			pos.Speed = float64(kit.ToInt(course, 9)) // as Java: the course overwrites the speed
			motion := kit.BetweenInt(course, 9, 11)
			if motion > 0 {
				pos.Set(model.KeyMotion, motion == 1)
			}
			pos.Set(model.KeySatellites, kit.FromInt(course, 11))
			altitude := data.ReadUnsignedShortLE()
			pos.Altitude = float64(kit.ToInt(altitude, 14))
			if check(altitude, 14) {
				pos.Altitude = -pos.Altitude
			}
		case 3:
			powerLow := data.ReadUnsignedByte()
			powerFlags := data.ReadUnsignedByte()
			batteryHigh := data.ReadUnsignedByte()
			pos.Set(model.KeyPower, float64(powerLow+(kit.ToInt(powerFlags, 5)<<8))/100.0)
			pos.Set(model.KeyIgnition, check(powerFlags, 5))
			pos.Set(model.KeyBattery, float64(kit.FromInt(powerFlags, 6)+(batteryHigh<<2))/100.0)
			if recordLength >= 4 {
				extraFlags := data.ReadUnsignedByte()
				if check(extraFlags, 0) {
					pos.AddAlarm(model.AlarmLowPower)
				}
				if check(extraFlags, 1) {
					pos.AddAlarm(model.AlarmLowBattery)
				}
			}
		case 6:
			pos.Set(model.KeyInput, data.ReadUnsignedByte())
		case 7:
			pos.Set(model.KeyOutput, data.ReadUnsignedByte())
		case 8:
			mask := data.ReadUnsignedByte()
			for i := 0; i < 8; i++ {
				if check(mask, i) {
					pos.Set(model.PrefixAdc+strconv.Itoa(i+1), data.ReadUnsignedShortLE())
				}
			}
		case 11:
			mask := data.ReadUnsignedByte()
			for i := 1; i < 8; i++ {
				if check(mask, i) {
					pos.Set("fuel"+strconv.Itoa(i), data.ReadUnsignedShortLE())
				}
			}
		case 12:
			mask := data.ReadUnsignedByte()
			for i := 1; i < 8; i++ {
				if check(mask, i) {
					pos.Set("fuelTemp"+strconv.Itoa(i), int(data.ReadSignedByte()))
				}
			}
		case 13:
			mask := data.ReadUnsignedByte()
			for i := 0; i < 8; i++ {
				if check(mask, i) {
					pos.Set(model.PrefixTemp+strconv.Itoa(i+1), float64(data.ReadShortLE())/100.0)
				}
			}
		case 18:
			key := "sensor" + strconv.Itoa(data.ReadUnsignedByte())
			switch recordLength - 1 {
			case 1:
				pos.Set(key, data.ReadUnsignedByte())
			case 2:
				pos.Set(key, data.ReadUnsignedShortLE())
			case 4:
				pos.Set(key, data.ReadUnsignedIntLE())
			}
		}

		data.SetReaderIndex(recordEnd)
	}

	positions = processPosition(m, positions, pos)

	if len(positions) == 0 {
		return nil
	}
	return positions
}

func processPosition(m *ingest.Message, positions []*model.Position, pos *model.Position) []*model.Position {
	if pos.Attributes.Len() > 0 {
		if pos.FixTime.IsZero() {
			pos.SetTime(m.Now)
		}
		if _, ok := pos.Attributes.Get(model.KeySatellites); !ok {
			m.LastLocation(pos, nil)
		}
		positions = append(positions, pos)
	}
	return positions
}

func sendResponse(m *ingest.Message, deviceID *int64, msgType, index *int) {
	response := kit.Empty()
	response.WriteByte8(0x53) // signature

	options := 0
	if deviceID != nil {
		options |= 1 << 11
	}
	if msgType != nil {
		options |= 1 << 12
	}
	if index != nil {
		options |= 1 << 13
	}
	response.WriteShortLE(options)

	if deviceID != nil {
		response.WriteIntLE(*deviceID)
	}
	if msgType != nil {
		response.WriteByte8(*msgType)
	}
	if index != nil {
		response.WriteShortLE((1 << (*index + 1)) - 1)
	}
	response.WriteShortLE(kit.CRC16(kit.CRC16_CCITT_FALSE, response.Bytes()[1:]))

	m.Send(response)
}

func readVarSize(buf *kit.Buf) int {
	y := 0
	for {
		b := buf.ReadUnsignedByte()
		y = int(int32(y<<7) | int32(b&0x7f))
		if b&0x80 == 0 {
			return y
		}
	}
}

func decompress(in *kit.Buf) *kit.Buf {
	out := kit.Empty()
	if in.ReadableBytes() < 1 {
		return out
	}
	marker := in.ReadUnsignedByte()
	for {
		symbol := in.ReadUnsignedByte()
		if symbol == marker {
			if in.GetUnsignedByte(in.ReaderIndex()) == 0 {
				out.WriteByte8(marker)
				in.Skip(1)
			} else {
				length := readVarSize(in)
				offset := readVarSize(in)
				for i := 0; i < length; i++ {
					out.WriteByte8(out.GetUnsignedByte(out.WriterIndex() - offset))
				}
			}
		} else {
			out.WriteByte8(symbol)
		}
		if !in.IsReadable() {
			return out
		}
	}
}
