// Package cityeasy is the Cityeasy protocol (CityeasyProtocol): binary "TT" frames with
// text location reports over TCP.
package cityeasy

import (
	"encoding/hex"
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "cityeasy",
		DataCommands: protocol.DataCommands("cityeasy"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, -4, 0),
				Decoder: ingest.DecoderFunc(decode),
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

var pattern = kit.NewPattern().
	GroupBegin().
	Number("(dddd)(dd)(dd)").  // date (yyyymmdd)
	Number("(dd)(dd)(dd),").   // time (hhmmss)
	Number("([AV]),").         // validity
	Number("(d+),").           // satellites
	Number("([NS]),(d+.d+),"). // latitude
	Number("([EW]),(d+.d+),"). // longitude
	Number("(d+.d),").         // speed
	Number("(d+.d),").         // hdop
	Number("(d+.d)").          // altitude
	GroupEndWith("?").Text(";").
	Number("(d+),"). // mcc
	Number("(d+),"). // mnc
	Number("(d+),"). // lac
	Number("(d+)").  // cell
	Any().
	Compile()

const (
	msgAddressRequest   = 0x0001
	msgStatus           = 0x0002
	msgLocationReport   = 0x0003
	msgLocationRequest  = 0x0004
	msgLocationInterval = 0x0005
	msgPhoneNumber      = 0x0006
	msgMonitoring       = 0x0007
	msgTimezone         = 0x0008
)

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2)             // header
	buf.ReadUnsignedShort() // length

	imei := hex.EncodeToString(buf.ReadBytes(7))
	ds := m.Session(imei, imei+strconv.FormatInt(kit.Luhn(kit.Long(imei)), 10))
	if ds == nil {
		return nil
	}

	typ := buf.ReadUnsignedShort()
	if typ != msgLocationReport && typ != msgLocationRequest {
		return nil
	}

	sentence := buf.GetLatin1(buf.ReaderIndex(), buf.ReadableBytes()-8)
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if parser.HasNextN(15) {
		pos.SetTime(parser.NextDateTimeDefault())

		pos.Valid = parser.Next() == "A"
		pos.Set(model.KeySatellites, parser.NextInt())

		pos.SetLatitude(parser.NextCoordinate(kit.HemDeg))
		pos.SetLongitude(parser.NextCoordinate(kit.HemDeg))

		pos.Speed = parser.NextDoubleDefault(0)
		pos.Set(model.KeyHdop, parser.NextDoubleDefault(0))
		pos.Altitude = parser.NextDoubleDefault(0)
	} else {
		m.LastLocation(pos, nil)
	}

	pos.Network = model.NetworkOf(model.NewCellTower(
		parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0), int64(parser.NextIntDefault(0))))
	return pos
}

func encodeContent(typ int, content *kit.Buf) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8('S')
	buf.WriteByte8('S')
	buf.WriteShort(2 + 2 + 2 + content.ReadableBytes() + 4 + 2 + 2)
	buf.WriteShort(typ)
	buf.WriteBuf(content)
	buf.WriteInt(0x0B)
	buf.WriteShort(kit.CRC16(kit.CRC16_KERMIT, buf.Bytes()))
	buf.WriteByte8('\r')
	buf.WriteByte8('\n')
	return buf
}

// rawOffsetMinutes is TimeZone.getRawOffset() / 60000: the zone's standard offset.
func rawOffsetMinutes(zone *time.Location, now time.Time) int {
	year := now.In(zone).Year()
	for _, month := range []time.Month{time.January, time.July} {
		t := time.Date(year, month, 1, 0, 0, 0, 0, zone)
		if !t.IsDST() {
			_, offset := t.Zone()
			return offset / 60
		}
	}
	_, offset := now.In(zone).Zone()
	return offset / 60
}

// encode is CityeasyProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	content := kit.Empty()
	switch c.Type {
	case model.CommandPositionSingle:
		return encodeContent(msgLocationRequest, content)
	case model.CommandPositionPeriodic:
		content.WriteShort(kit.CommandInt(c, model.CommandKeyFrequency))
		return encodeContent(msgLocationInterval, content)
	case model.CommandPositionStop:
		content.WriteShort(0)
		return encodeContent(msgLocationInterval, content)
	case model.CommandSetTimezone:
		id := kit.CommandString(c, model.CommandKeyTimezone)
		if id == nil {
			kit.Throw("NullPointerException: timezone")
		}
		timezone := rawOffsetMinutes(javatime.TimeZone(*id), m.Now)
		if timezone < 0 {
			content.WriteByte8(1)
			timezone = -timezone
		} else {
			content.WriteByte8(0)
		}
		content.WriteShort(timezone)
		return encodeContent(msgTimezone, content)
	}
	return nil
}
